package cpu

import (
	"context"
	"encoding/binary"
	"log"
	"math"
	"sync"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

const (
	SceneDuration = 3 * time.Second
	TickInterval  = 8 * time.Millisecond
	VRAMWidth     = 256
	VRAMHeight    = 212
)

type scene struct {
	name   string
	init   func(c *CPUModule, b bus.Bus)
	update func(c *CPUModule, b bus.Bus, frame int)
}

type CPUModule struct {
	wg       sync.WaitGroup
	frame    int
	sceneIdx int
}

func New() *CPUModule {
	return &CPUModule{}
}

func (c *CPUModule) Name() string {
	return "CPU"
}

func (c *CPUModule) Start(ctx context.Context, b bus.Bus) error {
	sysCh, err := b.Subscribe("system")
	if err != nil {
		return err
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.run(ctx, b, sysCh)
	}()
	return nil
}

func (c *CPUModule) Stop() error {
	log.Println("[CPU] Stop: waiting for run goroutine...")
	c.wg.Wait()
	log.Println("[CPU] Stop: run goroutine finished.")
	return nil
}

func (c *CPUModule) run(ctx context.Context, b bus.Bus, sysCh <-chan *bus.BusMessage) {
	_ = b.Publish("vram", &bus.BusMessage{
		Target: "mode", Operation: bus.OpCommand,
		Data: []byte{0x00, 0x01, 0x00}, Source: c.Name(),
	})
	time.Sleep(300 * time.Millisecond)

	scenes := []scene{
		{"Palette+Clear", scene1Init, scene1Update},
		{"BlitRect", scene2Init, scene2Update},
		{"Transform", scene3Init, scene3Update},
		{"AlphaBlend", scene4Init, scene4Update},
		{"Scroll", scene5Init, scene5Update},
		{"PageComposite", scene6Init, scene6Update},
		{"LargeMapScroll", scene7Init, scene7Update},
	}

	c.sceneIdx = 0
	c.frame = 0
	log.Printf("[CPU] Scene %d: %s", c.sceneIdx+1, scenes[c.sceneIdx].name)
	scenes[c.sceneIdx].init(c, b)

	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	sceneStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			log.Println("[CPU] run: ctx.Done received, exiting")
			return
		case msg := <-sysCh:
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("[CPU] run: shutdown command received, exiting")
				return
			}
		case <-ticker.C:
			scenes[c.sceneIdx].update(c, b, c.frame)
			c.frame++

			if time.Since(sceneStart) >= SceneDuration {
				c.resetScene(b)
				c.sceneIdx = (c.sceneIdx + 1) % len(scenes)
				c.frame = 0
				sceneStart = time.Now()
				log.Printf("[CPU] Scene %d: %s", c.sceneIdx+1, scenes[c.sceneIdx].name)
				scenes[c.sceneIdx].init(c, b)
			}
		}
	}
}

func (c *CPUModule) resetScene(b bus.Bus) {
	c.publishSetViewport(b, 0, 0)
	c.publishSetDisplayPage(b, 0)
	c.publishSetPageCount(b, 1)
	c.publishClearVRAM(b, 0, 0)
}

// --- Helper functions ---

func (c *CPUModule) publishClearVRAM(b bus.Bus, page, paletteIdx uint8) {
	b.Publish("vram", &bus.BusMessage{
		Target: "clear_vram", Operation: bus.OpCommand,
		Data: []byte{page, paletteIdx}, Source: c.Name(),
	})
}

func (c *CPUModule) publishBlitRect(b bus.Bus, page uint8, x, y, w, h uint16, blendMode uint8, pixels []uint8) {
	data := make([]byte, 10+len(pixels))
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], x)
	binary.BigEndian.PutUint16(data[3:], y)
	binary.BigEndian.PutUint16(data[5:], w)
	binary.BigEndian.PutUint16(data[7:], h)
	data[9] = blendMode
	copy(data[10:], pixels)
	b.Publish("vram", &bus.BusMessage{
		Target: "blit_rect", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}

func (c *CPUModule) publishBlitRectTransform(b bus.Bus, page uint8, x, y, srcW, srcH, pivotX, pivotY uint16, rotation uint8, scaleX, scaleY uint16, blendMode uint8, pixels []uint8) {
	data := make([]byte, 19+len(pixels))
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], x)
	binary.BigEndian.PutUint16(data[3:], y)
	binary.BigEndian.PutUint16(data[5:], srcW)
	binary.BigEndian.PutUint16(data[7:], srcH)
	binary.BigEndian.PutUint16(data[9:], pivotX)
	binary.BigEndian.PutUint16(data[11:], pivotY)
	data[13] = rotation
	binary.BigEndian.PutUint16(data[14:], scaleX)
	binary.BigEndian.PutUint16(data[16:], scaleY)
	data[18] = blendMode
	copy(data[19:], pixels)
	b.Publish("vram", &bus.BusMessage{
		Target: "blit_rect_transform", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}

func (c *CPUModule) publishCopyRect(b bus.Bus, srcPage, dstPage uint8, srcX, srcY, dstX, dstY, w, h uint16) {
	data := make([]byte, 14)
	data[0] = srcPage
	data[1] = dstPage
	binary.BigEndian.PutUint16(data[2:], srcX)
	binary.BigEndian.PutUint16(data[4:], srcY)
	binary.BigEndian.PutUint16(data[6:], dstX)
	binary.BigEndian.PutUint16(data[8:], dstY)
	binary.BigEndian.PutUint16(data[10:], w)
	binary.BigEndian.PutUint16(data[12:], h)
	b.Publish("vram", &bus.BusMessage{
		Target: "copy_rect", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}

func (c *CPUModule) publishSetPaletteBlock(b bus.Bus, start, count uint8, colors [][4]uint8) {
	data := make([]byte, 2+int(count)*4)
	data[0] = start
	data[1] = count
	for i := range int(count) {
		off := 2 + i*4
		data[off] = colors[i][0]
		data[off+1] = colors[i][1]
		data[off+2] = colors[i][2]
		data[off+3] = colors[i][3]
	}
	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette_block", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}

func (c *CPUModule) publishSetPageCount(b bus.Bus, count uint8) {
	b.Publish("vram", &bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand,
		Data: []byte{count}, Source: c.Name(),
	})
}

func (c *CPUModule) publishSetDisplayPage(b bus.Bus, page uint8) {
	b.Publish("vram", &bus.BusMessage{
		Target: "set_display_page", Operation: bus.OpCommand,
		Data: []byte{page}, Source: c.Name(),
	})
}

func (c *CPUModule) publishSwapPages(b bus.Bus, page1, page2 uint8) {
	b.Publish("vram", &bus.BusMessage{
		Target: "swap_pages", Operation: bus.OpCommand,
		Data: []byte{page1, page2}, Source: c.Name(),
	})
}

func (c *CPUModule) publishCopyPage(b bus.Bus, src, dst uint8) {
	b.Publish("vram", &bus.BusMessage{
		Target: "copy_page", Operation: bus.OpCommand,
		Data: []byte{src, dst}, Source: c.Name(),
	})
}

func (c *CPUModule) publishSetPageSize(b bus.Bus, page uint8, w, h uint16) {
	data := make([]byte, 5)
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], w)
	binary.BigEndian.PutUint16(data[3:], h)
	b.Publish("vram", &bus.BusMessage{
		Target: "set_page_size", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}

func (c *CPUModule) publishSetViewport(b bus.Bus, offX, offY int16) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:], uint16(offX))
	binary.BigEndian.PutUint16(data[2:], uint16(offY))
	b.Publish("vram", &bus.BusMessage{
		Target: "set_viewport", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}

// --- Scene 1: Palette+Clear (HSV cycling) ---

func scene1Init(c *CPUModule, b bus.Bus) {
	colors := make([][4]uint8, 256)
	for i := range 256 {
		hue := float64(i) / 256.0
		r, g, bl := hsvToRGB(hue, 1.0, 1.0)
		colors[i] = [4]uint8{r, g, bl, 255}
	}
	// set_palette_block max 256 entries: set in 2 blocks of 128
	c.publishSetPaletteBlock(b, 0, 128, colors[:128])
	c.publishSetPaletteBlock(b, 128, 128, colors[128:])
}

func scene1Update(c *CPUModule, b bus.Bus, frame int) {
	idx := uint8(frame % 256)
	c.publishClearVRAM(b, 0, idx)
}

// --- Scene 2: BlitRect (Checker pattern) ---

func scene2Init(c *CPUModule, b bus.Bus) {
	colors := [][4]uint8{
		{40, 40, 60, 255},
		{220, 180, 60, 255},
		{60, 120, 200, 255},
	}
	c.publishSetPaletteBlock(b, 0, 3, colors)
	c.publishClearVRAM(b, 0, 0)
}

func scene2Update(c *CPUModule, b bus.Bus, frame int) {
	shift := frame % 2
	pixels := make([]byte, 8*8)
	for y := range 8 {
		for x := range 8 {
			if (x+y+shift)%2 == 0 {
				pixels[y*8+x] = 1
			} else {
				pixels[y*8+x] = 2
			}
		}
	}
	// Tile the screen with the pattern
	for ty := 0; ty < VRAMHeight; ty += 8 {
		for tx := 0; tx < VRAMWidth; tx += 8 {
			c.publishBlitRect(b, 0, uint16(tx), uint16(ty), 8, 8, 0, pixels)
		}
	}
}

// --- Scene 3: Transform (Rotating sprites) ---

var diamondSprite = func() []byte {
	s := make([]byte, 8*8)
	for y := range 8 {
		for x := range 8 {
			dx := x - 3
			dy := y - 3
			if dx < 0 {
				dx = -dx
			}
			if dy < 0 {
				dy = -dy
			}
			if dx+dy <= 3 {
				s[y*8+x] = 1
			}
		}
	}
	return s
}()

func scene3Init(c *CPUModule, b bus.Bus) {
	colors := [][4]uint8{
		{20, 20, 40, 255},
		{255, 100, 50, 255},
	}
	c.publishSetPaletteBlock(b, 0, 2, colors)
}

func scene3Update(c *CPUModule, b bus.Bus, frame int) {
	c.publishClearVRAM(b, 0, 0)
	rot := uint8((frame * 2) % 256)

	// Center: normal rotation
	c.publishBlitRectTransform(b, 0, 128, 106, 8, 8, 4, 4, rot, 0x0100, 0x0100, 0, diamondSprite)
	// Left: bottom pivot
	c.publishBlitRectTransform(b, 0, 64, 106, 8, 8, 4, 7, rot, 0x0100, 0x0100, 0, diamondSprite)
	// Right: 2x scale
	c.publishBlitRectTransform(b, 0, 192, 106, 8, 8, 4, 4, rot, 0x0200, 0x0200, 0, diamondSprite)
}

// --- Scene 4: AlphaBlend (Blend modes demo) ---

func scene4Init(c *CPUModule, b bus.Bus) {
	colors := [][4]uint8{
		{0, 0, 0, 255},       // 0: black bg
		{255, 0, 0, 255},     // 1: red
		{0, 255, 0, 255},     // 2: green
		{0, 0, 255, 255},     // 3: blue
		{0, 0, 0, 0},         // 4: transparent
		{0, 0, 0, 0},         // 5
		{0, 0, 0, 0},         // 6
		{0, 0, 0, 0},         // 7
		{0, 0, 0, 0},         // 8
		{0, 0, 0, 0},         // 9
		{255, 255, 255, 128}, // 10: white semi-transparent
	}
	c.publishSetPaletteBlock(b, 0, 11, colors)
	c.publishClearVRAM(b, 0, 0)

	// Draw 3 vertical color bars
	barW := uint16(80)
	barH := uint16(VRAMHeight)
	for _, bar := range []struct {
		x   uint16
		idx byte
	}{
		{0, 1},   // red
		{80, 2},  // green
		{160, 3}, // blue
	} {
		pixels := make([]byte, int(barW)*int(barH))
		for i := range pixels {
			pixels[i] = bar.idx
		}
		c.publishBlitRect(b, 0, bar.x, 0, barW, barH, 0, pixels)
	}
}

func scene4Update(c *CPUModule, b bus.Bus, frame int) {
	// 5 blend modes side by side
	rectW := uint16(40)
	rectH := uint16(40)
	bounce := int(20.0 * math.Sin(float64(frame)*0.08))
	baseY := uint16(86 + bounce)

	pixels := make([]byte, int(rectW)*int(rectH))
	for i := range pixels {
		pixels[i] = 10
	}

	for mode := range 5 {
		x := uint16(mode*50 + 5)
		c.publishBlitRect(b, 0, x, baseY, rectW, rectH, uint8(mode), pixels)
	}
}

// --- Scene 5: Scroll (Vertical scroll) ---

func scene5Init(c *CPUModule, b bus.Bus) {
	colors := make([][4]uint8, 17)
	colors[0] = [4]uint8{0, 0, 0, 255}
	for i := 1; i <= 16; i++ {
		hue := float64(i-1) / 16.0
		r, g, bl := hsvToRGB(hue, 0.8, 1.0)
		colors[i] = [4]uint8{r, g, bl, 255}
	}
	c.publishSetPaletteBlock(b, 0, 17, colors)

	// Fill screen with horizontal stripes
	pixels := make([]byte, VRAMWidth*VRAMHeight)
	for y := range VRAMHeight {
		idx := byte(y%16 + 1)
		for x := range VRAMWidth {
			pixels[y*VRAMWidth+x] = idx
		}
	}
	c.publishBlitRect(b, 0, 0, 0, VRAMWidth, VRAMHeight, 0, pixels)
}

func scene5Update(c *CPUModule, b bus.Bus, frame int) {
	// Scroll up by 1 pixel
	c.publishCopyRect(b, 0, 0, 0, 1, 0, 0, VRAMWidth, VRAMHeight-1)

	// Fill bottom row
	row := make([]byte, VRAMWidth)
	idx := byte((frame)%16 + 1)
	for x := range VRAMWidth {
		row[x] = idx
	}
	c.publishBlitRect(b, 0, 0, VRAMHeight-1, VRAMWidth, 1, 0, row)
}

// --- Scene 6: PageComposite ---

func scene6Init(c *CPUModule, b bus.Bus) {
	c.publishSetPageCount(b, 2)

	colors := make([][4]uint8, 17)
	colors[0] = [4]uint8{0, 0, 0, 255}
	for i := 1; i <= 16; i++ {
		hue := float64(i-1) / 16.0
		r, g, bl := hsvToRGB(hue, 0.6, 0.8)
		colors[i] = [4]uint8{r, g, bl, 255}
	}
	c.publishSetPaletteBlock(b, 0, 17, colors)

	// Page 0: background stripes
	pixels := make([]byte, VRAMWidth*VRAMHeight)
	for y := range VRAMHeight {
		idx := byte(y%16 + 1)
		for x := range VRAMWidth {
			pixels[y*VRAMWidth+x] = idx
		}
	}
	c.publishBlitRect(b, 0, 0, 0, VRAMWidth, VRAMHeight, 0, pixels)

	// Page 1: clear
	c.publishClearVRAM(b, 1, 0)
}

func scene6Update(c *CPUModule, b bus.Bus, frame int) {
	// Scroll page 0 left by 1 pixel
	c.publishCopyRect(b, 0, 0, 1, 0, 0, 0, VRAMWidth-1, VRAMHeight)

	// Fill right column of page 0
	col := make([]byte, VRAMHeight)
	idx := byte((frame)%16 + 1)
	for i := range col {
		col[i] = idx
	}
	c.publishBlitRect(b, 0, VRAMWidth-1, 0, 1, VRAMHeight, 0, col)

	// Page 1: clear and draw bouncing sprite
	c.publishClearVRAM(b, 1, 0)
	spriteX := uint16(124 + int(60*math.Sin(float64(frame)*0.05)))
	spriteY := uint16(100 + int(40*math.Cos(float64(frame)*0.07)))
	c.publishBlitRect(b, 1, spriteX, spriteY, 8, 8, 0, diamondSprite)

	// Composite: copy page 1 non-zero onto page 0
	c.publishCopyRect(b, 1, 0, 0, 0, 0, 0, VRAMWidth, VRAMHeight)
	c.publishSetDisplayPage(b, 0)
}

// --- Scene 7: LargeMapScroll (512x512 with Lissajous viewport) ---

func scene7Init(c *CPUModule, b bus.Bus) {
	c.publishSetPageCount(b, 2)
	c.publishSetPageSize(b, 0, 512, 512)

	colors := make([][4]uint8, 32)
	colors[0] = [4]uint8{10, 10, 30, 255}
	for i := 1; i < 32; i++ {
		hue := float64(i) / 32.0
		r, g, bl := hsvToRGB(hue, 0.7, 0.9)
		colors[i] = [4]uint8{r, g, bl, 255}
	}
	c.publishSetPaletteBlock(b, 0, 32, colors)

	// Fill 512x512 page with geometric tile pattern
	const mapW, mapH = 512, 512
	const tileSize = 16
	pixels := make([]byte, mapW*mapH)
	for ty := 0; ty < mapH/tileSize; ty++ {
		for tx := 0; tx < mapW/tileSize; tx++ {
			idx := byte(((tx ^ ty) % 31) + 1)
			for py := range tileSize {
				for px := range tileSize {
					pixels[(ty*tileSize+py)*mapW+(tx*tileSize+px)] = idx
				}
			}
		}
	}
	c.publishBlitRect(b, 0, 0, 0, mapW, mapH, 0, pixels)
	c.publishSetDisplayPage(b, 0)
}

func scene7Update(c *CPUModule, b bus.Bus, frame int) {
	offsetX := int16(128 + int(128*math.Sin(float64(frame)*0.02)))
	offsetY := int16(106 + int(106*math.Sin(float64(frame)*0.03)))
	c.publishSetViewport(b, offsetX, offsetY)
}

// --- Utility ---

func hsvToRGB(h, s, v float64) (byte, byte, byte) {
	i := int(h * 6)
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)

	var r, g, bl float64
	switch i % 6 {
	case 0:
		r, g, bl = v, t, p
	case 1:
		r, g, bl = q, v, p
	case 2:
		r, g, bl = p, v, t
	case 3:
		r, g, bl = p, q, v
	case 4:
		r, g, bl = t, p, v
	case 5:
		r, g, bl = v, p, q
	}
	return byte(r * 255), byte(g * 255), byte(bl * 255)
}
