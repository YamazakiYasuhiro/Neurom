package monitor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log"
	"math"
	"sync"

	"golang.org/x/mobile/app"
	"golang.org/x/mobile/event/lifecycle"
	"golang.org/x/mobile/event/paint"
	"golang.org/x/mobile/event/size"
	"golang.org/x/mobile/gl"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/stats"
)

const directColorMarker = 0xFF

// VRAMAccessor provides direct read access to the VRAM module's buffers.
type VRAMAccessor interface {
	VRAMBuffer() []uint8
	VRAMColorBuffer() []uint8
	VRAMWidth() int
	VRAMHeight() int
	VRAMPalette() [256][4]uint8
	DisplayPage() int
	ViewportOffset() (int16, int16)
}

// MonitorStats holds the monitor performance statistics for multi-window display.
type MonitorStats = stats.MonitorStats

type MonitorConfig struct {
	Headless    bool
	RefreshRate int
	OnClose     func()
}

type MonitorModule struct {
	config       MonitorConfig
	bus          bus.Bus
	wg           sync.WaitGroup
	vramAccessor VRAMAccessor
	stats        *stats.Collector
	displayPage  int

	mu      sync.Mutex
	vram    []uint8
	rgba    []byte
	palette [256][4]uint8
	dirty   bool
	width   int
	height  int

	appObj app.App
	glctx  gl.Context

	program  gl.Program
	tex      gl.Texture
	position gl.Attrib
	texcoord gl.Attrib
	texLoc   gl.Uniform
	quadVBO  gl.Buffer
}

func New(cfg MonitorConfig) *MonitorModule {
	m := &MonitorModule{
		config: cfg,
		width:  256,
		height: 212,
		vram:   make([]uint8, 256*212),
		rgba:   make([]byte, 256*212*4),
		stats:  stats.NewCollector(),
	}
	for i := range 256 {
		p := uint8(i)
		m.palette[p][0] = byte((int((p >> 5) & 7) * 255) / 7)
		m.palette[p][1] = byte((int((p >> 2) & 7) * 255) / 7)
		m.palette[p][2] = byte((int(p & 3) * 255) / 3)
		m.palette[p][3] = 255
	}
	return m
}

// SetVRAMAccessor sets the direct VRAM accessor for rendering.
func (m *MonitorModule) SetVRAMAccessor(a VRAMAccessor) {
	m.vramAccessor = a
}

// RecordFrame records a single frame for FPS counting.
func (m *MonitorModule) RecordFrame() {
	m.stats.Record("frame", 0)
}

// GetMonitorStats returns the monitor performance statistics.
func (m *MonitorModule) GetMonitorStats() MonitorStats {
	snap := m.stats.Snapshot()
	frameStat := snap["frame"]
	return MonitorStats{
		FPS1s:  float64(frameStat.Last1s.Count),
		FPS10s: float64(frameStat.Last10s.Count) / 10.0,
		FPS30s: float64(frameStat.Last30s.Count) / 30.0,
	}
}

func (m *MonitorModule) publishMonitorStats() {
	ms := m.GetMonitorStats()
	data, _ := json.Marshal(ms)
	m.bus.Publish("monitor_update", &bus.BusMessage{
		Target: "stats_data", Operation: bus.OpCommand, Data: data, Source: m.Name(),
	})
}

func (m *MonitorModule) Name() string { return "Monitor" }

func (m *MonitorModule) Start(ctx context.Context, b bus.Bus) error {
	m.bus = b
	ch, err := b.Subscribe("vram_update")
	if err != nil {
		return err
	}
	sysCh, err := b.Subscribe("system")
	if err != nil {
		return err
	}
	monCh, err := b.Subscribe("monitor")
	if err != nil {
		return err
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.receiveLoop(ctx, ch, sysCh, monCh)
	}()
	return nil
}

func (m *MonitorModule) Stop() error {
	log.Println("[Monitor] Stop: waiting for receiveLoop goroutine...")
	m.wg.Wait()
	log.Println("[Monitor] Stop: receiveLoop goroutine finished.")
	return nil
}

func (m *MonitorModule) RunMain() {
	if m.config.Headless {
		return
	}
	app.Main(func(a app.App) {
		log.Println("[Monitor] RunMain: app.Main callback started")
		m.appObj = a
	eventLoop:
		for e := range a.Events() {
			switch e := a.Filter(e).(type) {
			case lifecycle.Event:
				log.Printf("[Monitor] RunMain: lifecycle event: From=%v To=%v Crosses(Visible)=%v", e.From, e.To, e.Crosses(lifecycle.StageVisible))
				switch e.Crosses(lifecycle.StageVisible) {
				case lifecycle.CrossOn:
					m.glctx, _ = e.DrawContext.(gl.Context)
					m.onStart(m.glctx)
					a.Send(paint.Event{})
				case lifecycle.CrossOff:
					log.Println("[Monitor] RunMain: CrossOff — cleaning up GL resources")
					if m.glctx != nil {
						m.glctx.DeleteProgram(m.program)
						m.glctx.DeleteTexture(m.tex)
						m.glctx = nil
					}
				}
				if e.To == lifecycle.StageDead {
					log.Println("[Monitor] RunMain: StageDead detected, breaking event loop")
					m.publishShutdown()
					if m.config.OnClose != nil {
						m.config.OnClose()
					}
					break eventLoop
				}
			case size.Event:
				if m.glctx != nil {
					m.glctx.Viewport(0, 0, e.WidthPx, e.HeightPx)
				}
			case paint.Event:
				if m.glctx == nil || e.External {
					continue
				}
				m.onPaint(m.glctx)
				a.Publish()
			}
		}
		log.Println("[Monitor] RunMain: event loop exited")
		m.publishShutdown()
		log.Println("[Monitor] RunMain: app.Main callback returning")
	})
	log.Println("[Monitor] RunMain: app.Main returned")
}

func (m *MonitorModule) publishShutdown() {
	log.Println("[Monitor] publishShutdown: called")
	if m.bus != nil {
		err := m.bus.Publish("system", &bus.BusMessage{
			Target: bus.TargetSystem, Operation: bus.OpCommand,
			Data: []byte(bus.CmdShutdown), Source: m.Name(),
		})
		if err != nil {
			log.Printf("[Monitor] publishShutdown: publish failed: %v", err)
		} else {
			log.Println("[Monitor] publishShutdown: shutdown message published successfully")
		}
	} else {
		log.Println("[Monitor] publishShutdown: bus is nil, cannot publish")
	}
}

func (m *MonitorModule) receiveLoop(ctx context.Context, ch <-chan *bus.BusMessage, sysCh <-chan *bus.BusMessage, monCh <-chan *bus.BusMessage) {
	log.Println("[Monitor] receiveLoop: started")
	defer log.Println("[Monitor] receiveLoop: exited")
	for {
		select {
		case <-ctx.Done():
			log.Println("[Monitor] receiveLoop: ctx.Done received")
			return
		case msg := <-sysCh:
			log.Printf("[Monitor] receiveLoop: system message received: Target=%s Data=%s", msg.Target, string(msg.Data))
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("[Monitor] receiveLoop: shutdown command matched, exiting")
				return
			}
		case msg := <-monCh:
			if msg != nil && msg.Target == "get_stats" && msg.Operation == bus.OpCommand {
				m.publishMonitorStats()
			}
		case msg := <-ch:
			m.handleVRAMEvent(msg)
		}
	}
}

func (m *MonitorModule) handleVRAMEvent(msg *bus.BusMessage) {
	switch msg.Target {
	case "palette_updated":
		if len(msg.Data) < 4 {
			return
		}
		index := msg.Data[0]
		a := uint8(255)
		if len(msg.Data) >= 5 {
			a = msg.Data[4]
		}
		m.mu.Lock()
		m.palette[index] = [4]uint8{msg.Data[1], msg.Data[2], msg.Data[3], a}
		m.markDirty()
		m.mu.Unlock()

	case "vram_updated":
		if len(msg.Data) < 6 {
			return
		}
		page := int(msg.Data[0])
		if page != m.displayPage {
			return
		}
		x := int(binary.BigEndian.Uint16(msg.Data[1:]))
		y := int(binary.BigEndian.Uint16(msg.Data[3:]))
		p := msg.Data[5]
		if x < m.width && y < m.height {
			m.mu.Lock()
			m.vram[y*m.width+x] = p
			m.markDirty()
			m.mu.Unlock()
		}

	case "vram_cleared":
		if len(msg.Data) >= 1 {
			page := int(msg.Data[0])
			if page == m.displayPage {
				m.mu.Lock()
				m.markDirty()
				m.mu.Unlock()
			}
		}

	case "rect_updated", "rect_copied", "page_size_changed", "viewport_changed",
		"palette_block_updated":
		m.mu.Lock()
		m.markDirty()
		m.mu.Unlock()

	case "display_page_changed":
		if len(msg.Data) >= 1 {
			m.displayPage = int(msg.Data[0])
			m.mu.Lock()
			m.markDirty()
			m.mu.Unlock()
		}
	}
}

func (m *MonitorModule) markDirty() {
	wasDirty := m.dirty
	m.dirty = true
	if !wasDirty && m.appObj != nil && !m.config.Headless {
		m.appObj.Send(paint.Event{})
	}
}

const vertexShader = `
attribute vec4 position;
attribute vec2 texcoord;
varying vec2 v_texcoord;
void main() {
	gl_Position = position;
	v_texcoord = texcoord;
}
`

const fragShader = `
precision mediump float;
varying vec2 v_texcoord;
uniform sampler2D tex;
void main() {
	gl_FragColor = texture2D(tex, v_texcoord);
}
`

func (m *MonitorModule) onStart(glctx gl.Context) {
	var err error
	m.program, err = compileProgram(glctx, vertexShader, fragShader)
	if err != nil {
		panic(err)
	}

	m.position = glctx.GetAttribLocation(m.program, "position")
	m.texcoord = glctx.GetAttribLocation(m.program, "texcoord")
	m.texLoc = glctx.GetUniformLocation(m.program, "tex")

	m.tex = glctx.CreateTexture()
	glctx.BindTexture(gl.TEXTURE_2D, m.tex)
	glctx.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	glctx.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)

	m.quadVBO = glctx.CreateBuffer()
	glctx.BindBuffer(gl.ARRAY_BUFFER, m.quadVBO)
	quadData := []float32{
		-1, -1, 0, 1,
		1, -1, 1, 1,
		-1, 1, 0, 0,
		1, 1, 1, 0,
	}
	glctx.BufferData(gl.ARRAY_BUFFER, f32Bytes(quadData), gl.STATIC_DRAW)
}

func (m *MonitorModule) onPaint(glctx gl.Context) {
	m.RecordFrame()
	glctx.ClearColor(0, 0, 0, 1)
	glctx.Clear(gl.COLOR_BUFFER_BIT)

	glctx.UseProgram(m.program)

	m.mu.Lock()
	if m.dirty {
		m.buildFrame()
		glctx.BindTexture(gl.TEXTURE_2D, m.tex)
		glctx.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, m.width, m.height, gl.RGBA, gl.UNSIGNED_BYTE, m.rgba)
		m.dirty = false
	}
	m.mu.Unlock()

	glctx.ActiveTexture(gl.TEXTURE0)
	glctx.BindTexture(gl.TEXTURE_2D, m.tex)
	glctx.Uniform1i(m.texLoc, 0)

	glctx.BindBuffer(gl.ARRAY_BUFFER, m.quadVBO)
	glctx.EnableVertexAttribArray(m.position)
	glctx.EnableVertexAttribArray(m.texcoord)
	glctx.VertexAttribPointer(m.position, 2, gl.FLOAT, false, 16, 0)
	glctx.VertexAttribPointer(m.texcoord, 2, gl.FLOAT, false, 16, 8)
	glctx.DrawArrays(gl.TRIANGLE_STRIP, 0, 4)
	glctx.DisableVertexAttribArray(m.position)
	glctx.DisableVertexAttribArray(m.texcoord)
}

func compileProgram(glctx gl.Context, vSrc, fSrc string) (gl.Program, error) {
	vShader, err := compileShader(glctx, gl.VERTEX_SHADER, vSrc)
	if err != nil {
		return gl.Program{}, err
	}
	fShader, err := compileShader(glctx, gl.FRAGMENT_SHADER, fSrc)
	if err != nil {
		return gl.Program{}, err
	}
	program := glctx.CreateProgram()
	glctx.AttachShader(program, vShader)
	glctx.AttachShader(program, fShader)
	glctx.LinkProgram(program)
	return program, nil
}

func compileShader(glctx gl.Context, shaderType gl.Enum, src string) (gl.Shader, error) {
	shader := glctx.CreateShader(shaderType)
	glctx.ShaderSource(shader, src)
	glctx.CompileShader(shader)
	return shader, nil
}

func (m *MonitorModule) buildFrame() {
	if m.vramAccessor != nil {
		m.refreshFromVRAM()
		return
	}
	for i := range len(m.vram) {
		p := m.vram[i]
		m.rgba[i*4] = m.palette[p][0]
		m.rgba[i*4+1] = m.palette[p][1]
		m.rgba[i*4+2] = m.palette[p][2]
		m.rgba[i*4+3] = m.palette[p][3]
	}
}

func (m *MonitorModule) refreshFromVRAM() {
	v := m.vramAccessor
	index := v.VRAMBuffer()
	color := v.VRAMColorBuffer()
	pal := v.VRAMPalette()
	vpX, vpY := v.ViewportOffset()
	vw, vh := v.VRAMWidth(), v.VRAMHeight()

	for y := range m.height {
		for x := range m.width {
			vramX := int(vpX) + x
			vramY := int(vpY) + y
			dstOff := (y*m.width + x) * 4

			if vramX < 0 || vramX >= vw || vramY < 0 || vramY >= vh {
				m.rgba[dstOff] = pal[0][0]
				m.rgba[dstOff+1] = pal[0][1]
				m.rgba[dstOff+2] = pal[0][2]
				m.rgba[dstOff+3] = pal[0][3]
				continue
			}

			srcIdx := vramY*vw + vramX
			p := index[srcIdx]
			if p == directColorMarker {
				copy(m.rgba[dstOff:dstOff+4], color[srcIdx*4:srcIdx*4+4])
			} else {
				m.rgba[dstOff] = pal[p][0]
				m.rgba[dstOff+1] = pal[p][1]
				m.rgba[dstOff+2] = pal[p][2]
				m.rgba[dstOff+3] = pal[p][3]
			}
		}
	}
}

// GetPixel returns the RGBA color at the given coordinates.
func (m *MonitorModule) GetPixel(x, y int) (r, g, b, a byte) {
	if x < 0 || x >= m.width || y < 0 || y >= m.height {
		return 0, 0, 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirty {
		m.buildFrame()
		m.dirty = false
	}
	idx := (y*m.width + x) * 4
	return m.rgba[idx], m.rgba[idx+1], m.rgba[idx+2], m.rgba[idx+3]
}

func f32Bytes(val []float32) []byte {
	buf := make([]byte, len(val)*4)
	for i, v := range val {
		u := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], u)
	}
	return buf
}
