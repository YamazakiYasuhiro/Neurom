package monitor

import (
	"context"
	"encoding/binary"
	"math"
	"sync"

	"golang.org/x/mobile/app"
	"golang.org/x/mobile/event/lifecycle"
	"golang.org/x/mobile/event/paint"
	"golang.org/x/mobile/event/size"
	"golang.org/x/mobile/gl"

	"github.com/axsh/neurom/internal/bus"
)

type MonitorConfig struct {
	Headless bool
}

type MonitorModule struct {
	config MonitorConfig
	bus    bus.Bus

	mu      sync.Mutex
	vram    []uint8
	rgba    []byte
	palette [256][3]uint8
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
	}
	for i := 0; i < 256; i++ {
		p := uint8(i)
		m.palette[p][0] = byte((int((p >> 5) & 7) * 255) / 7)
		m.palette[p][1] = byte((int((p >> 2) & 7) * 255) / 7)
		m.palette[p][2] = byte((int(p & 3) * 255) / 3)
	}
	return m
}

func (m *MonitorModule) Name() string {
	return "Monitor"
}

func (m *MonitorModule) Start(ctx context.Context, b bus.Bus) error {
	m.bus = b
	ch, err := b.Subscribe("vram_update")
	if err != nil {
		return err
	}

	go m.receiveLoop(ctx, ch)
	return nil
}

func (m *MonitorModule) Stop() error {
	return nil
}

func (m *MonitorModule) RunMain() {
	if m.config.Headless {
		return
	}
	app.Main(func(a app.App) {
		m.appObj = a
		for e := range a.Events() {
			switch e := a.Filter(e).(type) {
			case lifecycle.Event:
				switch e.Crosses(lifecycle.StageVisible) {
				case lifecycle.CrossOn:
					m.glctx, _ = e.DrawContext.(gl.Context)
					m.onStart(m.glctx)
					a.Send(paint.Event{})
				case lifecycle.CrossOff:
					if m.glctx != nil {
						m.glctx.DeleteProgram(m.program)
						m.glctx.DeleteTexture(m.tex)
						m.glctx = nil
					}
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
	})
}

func (m *MonitorModule) receiveLoop(ctx context.Context, ch <-chan *bus.BusMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			if msg.Target == "palette_updated" && len(msg.Data) >= 4 {
				index := msg.Data[0]
				m.mu.Lock()
				m.palette[index][0] = msg.Data[1]
				m.palette[index][1] = msg.Data[2]
				m.palette[index][2] = msg.Data[3]

				wasDirty := m.dirty
				m.dirty = true
				m.mu.Unlock()

				if !wasDirty && m.appObj != nil && !m.config.Headless {
					m.appObj.Send(paint.Event{})
				}

			} else if msg.Target == "vram_updated" && len(msg.Data) >= 5 {
				x := int(msg.Data[0])<<8 | int(msg.Data[1])
				y := int(msg.Data[2])<<8 | int(msg.Data[3])
				p := msg.Data[4]

				if x < m.width && y < m.height {
					m.mu.Lock()
					m.vram[y*m.width+x] = p
					wasDirty := m.dirty
					m.dirty = true
					m.mu.Unlock()

					if !wasDirty && m.appObj != nil && !m.config.Headless {
						m.appObj.Send(paint.Event{})
					}
				}
			}
		}
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

	// Create VBO for quad
	m.quadVBO = glctx.CreateBuffer()
	glctx.BindBuffer(gl.ARRAY_BUFFER, m.quadVBO)
	quadData := []float32{
		// x, y, u, v
		-1, -1, 0, 1,
		1, -1, 1, 1,
		-1, 1, 0, 0,
		1, 1, 1, 0,
	}
	glctx.BufferData(gl.ARRAY_BUFFER, f32Bytes(quadData), gl.STATIC_DRAW)
}

func (m *MonitorModule) onPaint(glctx gl.Context) {
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

	// offset 0 for position, 8 for texcoord, stride=16
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
	for i := 0; i < len(m.vram); i++ {
		p := m.vram[i]
		m.rgba[i*4] = m.palette[p][0]
		m.rgba[i*4+1] = m.palette[p][1]
		m.rgba[i*4+2] = m.palette[p][2]
		m.rgba[i*4+3] = 255
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
	// Assumes little-endian host is compatible with x/mobile/gl expectations.
	buf := make([]byte, len(val)*4)
	for i, v := range val {
		u := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], u)
	}
	return buf
}
