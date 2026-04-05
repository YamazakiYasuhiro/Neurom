package vram

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/stats"
)

const (
	DirectColorMarker = 0xFF
	DefaultPageWidth  = 256
	DefaultPageHeight = 212
)

type pageBuffer struct {
	width  int
	height int
	index  []uint8
	color  []uint8
}

func newPageBuffer(w, h int) pageBuffer {
	return pageBuffer{
		width:  w,
		height: h,
		index:  make([]uint8, w*h),
		color:  make([]uint8, w*h*4),
	}
}

type Config struct {
	Workers  int    // 1-256, default 1
	Strategy string // "static" or "dynamic", default "dynamic"
}

type VRAMModule struct {
	mu          sync.RWMutex
	pages       []pageBuffer
	displayPage int
	viewportX   int16
	viewportY   int16
	palette     [256][4]uint8
	bus         bus.Bus
	wg          sync.WaitGroup
	stats       *stats.Collector
	pool        *workerPool
	strategy    dispatchStrategy
}

func New(cfgs ...Config) *VRAMModule {
	cfg := Config{Workers: 1}
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	if cfg.Workers < 1 || cfg.Workers > 256 {
		cfg.Workers = 1
	}
	v := &VRAMModule{
		pages: []pageBuffer{newPageBuffer(DefaultPageWidth, DefaultPageHeight)},
		stats: stats.NewCollector(),
	}
	if cfg.Workers > 1 {
		v.pool = newWorkerPool(cfg.Workers)
		v.strategy = newDispatchStrategy(cfg.Strategy, cfg.Workers)
	}
	return v
}

func (v *VRAMModule) Name() string { return "VRAM" }

func (v *VRAMModule) Start(ctx context.Context, b bus.Bus) error {
	v.bus = b
	ch, err := b.Subscribe("vram")
	if err != nil {
		return err
	}
	sysCh, err := b.Subscribe("system")
	if err != nil {
		return err
	}
	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		v.run(ctx, ch, sysCh)
	}()
	return nil
}

func (v *VRAMModule) Stop() error {
	log.Println("[VRAM] Stop: waiting for run goroutine...")
	v.wg.Wait()
	if v.pool != nil {
		v.pool.stop()
	}
	log.Println("[VRAM] Stop: run goroutine finished.")
	return nil
}

func (v *VRAMModule) run(ctx context.Context, ch <-chan *bus.BusMessage, sysCh <-chan *bus.BusMessage) {
	for {
		select {
		case <-ctx.Done():
			log.Println("[VRAM] run: ctx.Done received, exiting")
			return
		case msg := <-sysCh:
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("[VRAM] run: shutdown command received, exiting")
				return
			}
		case msg := <-ch:
			v.handleMessage(msg)
		}
	}
}

// --- Stats ---

func (v *VRAMModule) GetStats() map[string]stats.CommandStat {
	return v.stats.Snapshot()
}

func (v *VRAMModule) publishStats() {
	snap := v.stats.Snapshot()
	payload := map[string]any{"commands": snap}
	data, _ := json.Marshal(payload)
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "stats_data", Operation: bus.OpCommand, Data: data, Source: v.Name(),
	})
}

// --- VRAMAccessor methods ---

func (v *VRAMModule) VRAMBuffer() []uint8       { return v.pages[v.displayPage].index }
func (v *VRAMModule) VRAMColorBuffer() []uint8   { return v.pages[v.displayPage].color }
func (v *VRAMModule) VRAMWidth() int             { return v.pages[v.displayPage].width }
func (v *VRAMModule) VRAMHeight() int            { return v.pages[v.displayPage].height }
func (v *VRAMModule) VRAMPalette() [256][4]uint8 { return v.palette }
func (v *VRAMModule) DisplayPage() int           { return v.displayPage }
func (v *VRAMModule) ViewportOffset() (int16, int16) {
	return v.viewportX, v.viewportY
}

// --- Message dispatch ---

func (v *VRAMModule) handleMessage(msg *bus.BusMessage) {
	if msg.Operation != bus.OpCommand {
		return
	}
	if msg.Target == "get_stats" {
		v.publishStats()
		return
	}

	start := time.Now()
	defer func() { v.stats.Record(msg.Target, time.Since(start)) }()

	v.mu.Lock()
	defer v.mu.Unlock()

	switch msg.Target {
	case "mode":
		pg := &v.pages[0]
		pg.index = make([]uint8, pg.width*pg.height)
		pg.color = make([]uint8, pg.width*pg.height*4)
		v.bus.Publish("vram_update", &bus.BusMessage{
			Target: "mode_changed", Operation: bus.OpCommand, Source: v.Name(),
		})
	case "draw_pixel":
		v.handleDrawPixel(msg)
	case "set_palette":
		v.handleSetPalette(msg)
	case "clear_vram":
		v.handleClearVRAM(msg)
	case "blit_rect":
		v.handleBlitRect(msg)
	case "blit_rect_transform":
		v.handleBlitRectTransform(msg)
	case "read_rect":
		v.handleReadRect(msg)
	case "copy_rect":
		v.handleCopyRect(msg)
	case "set_palette_block":
		v.handleSetPaletteBlock(msg)
	case "read_palette_block":
		v.handleReadPaletteBlock(msg)
	case "set_page_count":
		v.handleSetPageCount(msg)
	case "set_display_page":
		v.handleSetDisplayPage(msg)
	case "swap_pages":
		v.handleSwapPages(msg)
	case "copy_page":
		v.handleCopyPage(msg)
	case "set_page_size":
		v.handleSetPageSize(msg)
	case "set_viewport":
		v.handleSetViewport(msg)
	}
}

// parallelRows splits totalRows across workers and executes fn for each chunk.
// The strategy decides how many workers to use based on command and data size.
// If pool is nil (single-core mode), fn is called directly with (0, totalRows).
func (v *VRAMModule) parallelRows(command string, totalRows int, width int, fn func(startRow, endRow int)) {
	if v.pool == nil || totalRows <= 1 {
		fn(0, totalRows)
		return
	}
	dataSize := totalRows * width
	if dataSize < minThreshold {
		fn(0, totalRows)
		return
	}
	workers := v.strategy.Decide(command, dataSize)
	start := time.Now()
	if workers == 1 {
		fn(0, totalRows)
	} else {
		chunks := splitRows(totalRows, workers)
		var wg sync.WaitGroup
		wg.Add(len(chunks))
		for _, c := range chunks {
			v.pool.tasks <- rowTask{fn: fn, startRow: c[0], endRow: c[1], wg: &wg}
		}
		wg.Wait()
	}
	v.strategy.Feedback(command, dataSize, workers, time.Since(start))
}

// parallelRowsFn returns a function suitable for TransformBlitParallel,
// or nil if pool is not available.
func (v *VRAMModule) parallelRowsFn(command string, width int) func(int, func(int, int)) {
	if v.pool == nil {
		return nil
	}
	return func(totalRows int, fn func(int, int)) {
		v.parallelRows(command, totalRows, width, fn)
	}
}

// --- Drawing commands ---

// Format: [page:u8][x:u16][y:u16][p:u8]
func (v *VRAMModule) handleDrawPixel(msg *bus.BusMessage) {
	if len(msg.Data) < 6 {
		return
	}
	page := int(msg.Data[0])
	if !v.isValidPage(page) {
		v.publishPageError(0x01)
		return
	}
	pg := &v.pages[page]
	x := int(binary.BigEndian.Uint16(msg.Data[1:]))
	y := int(binary.BigEndian.Uint16(msg.Data[3:]))
	p := msg.Data[5]
	if x < pg.width && y < pg.height {
		idx := y*pg.width + x
		pg.index[idx] = p
		pal := v.palette[p]
		pg.color[idx*4] = pal[0]
		pg.color[idx*4+1] = pal[1]
		pg.color[idx*4+2] = pal[2]
		pg.color[idx*4+3] = pal[3]
		v.bus.Publish("vram_update", &bus.BusMessage{
			Target: "vram_updated", Operation: bus.OpCommand, Data: msg.Data, Source: v.Name(),
		})
	}
}

// Format (4 bytes): [index:u8][R:u8][G:u8][B:u8] — alpha defaults to 255
// Format (5 bytes): [index:u8][R:u8][G:u8][B:u8][A:u8]
func (v *VRAMModule) handleSetPalette(msg *bus.BusMessage) {
	if len(msg.Data) < 4 {
		return
	}
	index := msg.Data[0]
	r, g, b := msg.Data[1], msg.Data[2], msg.Data[3]
	a := uint8(255)
	if len(msg.Data) >= 5 {
		a = msg.Data[4]
	}
	v.palette[index] = [4]uint8{r, g, b, a}
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "palette_updated", Operation: bus.OpCommand, Data: msg.Data, Source: v.Name(),
	})
}

// Format: [page:u8][palette_idx:u8] (palette_idx optional, defaults to 0)
func (v *VRAMModule) handleClearVRAM(msg *bus.BusMessage) {
	page := 0
	paletteIdx := uint8(0)
	if len(msg.Data) >= 1 {
		page = int(msg.Data[0])
	}
	if len(msg.Data) >= 2 {
		paletteIdx = msg.Data[1]
	}
	if !v.isValidPage(page) {
		v.publishPageError(0x01)
		return
	}
	pg := &v.pages[page]
	pal := v.palette[paletteIdx]
	v.parallelRows("clear_vram", pg.height, pg.width, func(startRow, endRow int) {
		for y := startRow; y < endRow; y++ {
			for x := 0; x < pg.width; x++ {
				i := y*pg.width + x
				pg.index[i] = paletteIdx
				pg.color[i*4] = pal[0]
				pg.color[i*4+1] = pal[1]
				pg.color[i*4+2] = pal[2]
				pg.color[i*4+3] = pal[3]
			}
		}
	})
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "vram_cleared", Operation: bus.OpCommand,
		Data: []byte{uint8(page), paletteIdx}, Source: v.Name(),
	})
}

// Format: [dst_page:u8][dst_x:u16][dst_y:u16][w:u16][h:u16][blend_mode:u8][pixel_data...]
func (v *VRAMModule) handleBlitRect(msg *bus.BusMessage) {
	if len(msg.Data) < 10 {
		return
	}
	page := int(msg.Data[0])
	if !v.isValidPage(page) {
		v.publishPageError(0x01)
		return
	}
	pg := &v.pages[page]
	dstX := int(binary.BigEndian.Uint16(msg.Data[1:]))
	dstY := int(binary.BigEndian.Uint16(msg.Data[3:]))
	w := int(binary.BigEndian.Uint16(msg.Data[5:]))
	h := int(binary.BigEndian.Uint16(msg.Data[7:]))
	blendMode := BlendMode(msg.Data[9])
	pixelData := msg.Data[10:]

	if len(pixelData) < w*h {
		return
	}

	cx, cy, cw, ch, srcOffX, srcOffY := clipRect(dstX, dstY, w, h, pg.width, pg.height)
	if cw <= 0 || ch <= 0 {
		return
	}

	v.parallelRows("blit_rect", ch, cw, func(startRow, endRow int) {
		for row := startRow; row < endRow; row++ {
			for col := range cw {
				srcIdx := pixelData[(srcOffY+row)*w+(srcOffX+col)]
				dstIdx := (cy+row)*pg.width + (cx + col)

				if blendMode == BlendReplace {
					pg.index[dstIdx] = srcIdx
					pal := v.palette[srcIdx]
					pg.color[dstIdx*4] = pal[0]
					pg.color[dstIdx*4+1] = pal[1]
					pg.color[dstIdx*4+2] = pal[2]
					pg.color[dstIdx*4+3] = pal[3]
				} else {
					srcRGBA := v.palette[srcIdx]
					dstRGBA := [4]uint8{
						pg.color[dstIdx*4], pg.color[dstIdx*4+1],
						pg.color[dstIdx*4+2], pg.color[dstIdx*4+3],
					}
					result := BlendPixel(blendMode, srcRGBA, dstRGBA)
					pg.index[dstIdx] = DirectColorMarker
					pg.color[dstIdx*4] = result[0]
					pg.color[dstIdx*4+1] = result[1]
					pg.color[dstIdx*4+2] = result[2]
					pg.color[dstIdx*4+3] = result[3]
				}
			}
		}
	})
	v.publishRectEvent(cx, cy, cw, ch)
}

// Format: [dst_page:u8][dst_x:u16][dst_y:u16][src_w:u16][src_h:u16]
//
//	[pivot_x:u16][pivot_y:u16][rotation:u8][scale_x:u16][scale_y:u16]
//	[blend_mode:u8][pixel_data...]
func (v *VRAMModule) handleBlitRectTransform(msg *bus.BusMessage) {
	if len(msg.Data) < 19 {
		return
	}
	page := int(msg.Data[0])
	if !v.isValidPage(page) {
		v.publishPageError(0x01)
		return
	}
	pg := &v.pages[page]
	dstX := int(binary.BigEndian.Uint16(msg.Data[1:]))
	dstY := int(binary.BigEndian.Uint16(msg.Data[3:]))
	srcW := int(binary.BigEndian.Uint16(msg.Data[5:]))
	srcH := int(binary.BigEndian.Uint16(msg.Data[7:]))
	pivotX := int(binary.BigEndian.Uint16(msg.Data[9:]))
	pivotY := int(binary.BigEndian.Uint16(msg.Data[11:]))
	rotation := msg.Data[13]
	scaleX := binary.BigEndian.Uint16(msg.Data[14:])
	scaleY := binary.BigEndian.Uint16(msg.Data[16:])
	blendMode := BlendMode(msg.Data[18])
	pixelData := msg.Data[19:]

	if len(pixelData) < srcW*srcH {
		return
	}

	transformed, outW, outH, offX, offY := TransformBlitParallel(
		pixelData, srcW, srcH, pivotX, pivotY, rotation, scaleX, scaleY,
		v.parallelRowsFn("blit_rect_transform", srcW),
	)
	if transformed == nil {
		return
	}

	v.parallelRows("blit_rect_transform", outH, outW, func(startRow, endRow int) {
		for oy := startRow; oy < endRow; oy++ {
			for ox := range outW {
				val := transformed[oy*outW+ox]
				if val == 0 {
					continue
				}
				vx := dstX + offX + ox
				vy := dstY + offY + oy
				if vx < 0 || vx >= pg.width || vy < 0 || vy >= pg.height {
					continue
				}
				dstIdx := vy*pg.width + vx
				if blendMode == BlendReplace {
					pg.index[dstIdx] = val
					pal := v.palette[val]
					pg.color[dstIdx*4] = pal[0]
					pg.color[dstIdx*4+1] = pal[1]
					pg.color[dstIdx*4+2] = pal[2]
					pg.color[dstIdx*4+3] = pal[3]
				} else {
					srcRGBA := v.palette[val]
					dstRGBA := [4]uint8{
						pg.color[dstIdx*4], pg.color[dstIdx*4+1],
						pg.color[dstIdx*4+2], pg.color[dstIdx*4+3],
					}
					result := BlendPixel(blendMode, srcRGBA, dstRGBA)
					pg.index[dstIdx] = DirectColorMarker
					pg.color[dstIdx*4] = result[0]
					pg.color[dstIdx*4+1] = result[1]
					pg.color[dstIdx*4+2] = result[2]
					pg.color[dstIdx*4+3] = result[3]
				}
			}
		}
	})
	v.publishRectEvent(dstX+offX, dstY+offY, outW, outH)
}

// Format: [page:u8][x:u16][y:u16][w:u16][h:u16]
func (v *VRAMModule) handleReadRect(msg *bus.BusMessage) {
	if len(msg.Data) < 9 {
		return
	}
	page := int(msg.Data[0])
	if !v.isValidPage(page) {
		v.publishPageError(0x01)
		return
	}
	pg := &v.pages[page]
	x := int(binary.BigEndian.Uint16(msg.Data[1:]))
	y := int(binary.BigEndian.Uint16(msg.Data[3:]))
	w := int(binary.BigEndian.Uint16(msg.Data[5:]))
	h := int(binary.BigEndian.Uint16(msg.Data[7:]))

	resp := make([]byte, 8+w*h)
	binary.BigEndian.PutUint16(resp[0:], uint16(x))
	binary.BigEndian.PutUint16(resp[2:], uint16(y))
	binary.BigEndian.PutUint16(resp[4:], uint16(w))
	binary.BigEndian.PutUint16(resp[6:], uint16(h))

	for row := range h {
		for col := range w {
			rx, ry := x+col, y+row
			val := uint8(0)
			if rx >= 0 && rx < pg.width && ry >= 0 && ry < pg.height {
				val = pg.index[ry*pg.width+rx]
			}
			resp[8+row*w+col] = val
		}
	}
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "rect_data", Operation: bus.OpCommand, Data: resp, Source: v.Name(),
	})
}

// Format: [src_page:u8][dst_page:u8][src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]
func (v *VRAMModule) handleCopyRect(msg *bus.BusMessage) {
	if len(msg.Data) < 14 {
		return
	}
	srcPage := int(msg.Data[0])
	dstPage := int(msg.Data[1])
	if !v.isValidPage(srcPage) || !v.isValidPage(dstPage) {
		v.publishPageError(0x01)
		return
	}
	sp := &v.pages[srcPage]
	dp := &v.pages[dstPage]
	srcX := int(binary.BigEndian.Uint16(msg.Data[2:]))
	srcY := int(binary.BigEndian.Uint16(msg.Data[4:]))
	dstX := int(binary.BigEndian.Uint16(msg.Data[6:]))
	dstY := int(binary.BigEndian.Uint16(msg.Data[8:]))
	w := int(binary.BigEndian.Uint16(msg.Data[10:]))
	h := int(binary.BigEndian.Uint16(msg.Data[12:]))

	csx, csy, csw, csh, _, _ := clipRect(srcX, srcY, w, h, sp.width, sp.height)
	if csw <= 0 || csh <= 0 {
		return
	}

	tmpIndex := make([]uint8, csw*csh)
	tmpColor := make([]uint8, csw*csh*4)
	v.parallelRows("copy_rect", csh, csw, func(startRow, endRow int) {
		for row := startRow; row < endRow; row++ {
			for col := range csw {
				si := (csy+row)*sp.width + (csx + col)
				ti := row*csw + col
				tmpIndex[ti] = sp.index[si]
				copy(tmpColor[ti*4:ti*4+4], sp.color[si*4:si*4+4])
			}
		}
	})

	adjDstX := dstX + (csx - srcX)
	adjDstY := dstY + (csy - srcY)
	cdx, cdy, cdw, cdh, dOffX, dOffY := clipRect(adjDstX, adjDstY, csw, csh, dp.width, dp.height)
	if cdw <= 0 || cdh <= 0 {
		return
	}

	v.parallelRows("copy_rect", cdh, cdw, func(startRow, endRow int) {
		for row := startRow; row < endRow; row++ {
			for col := range cdw {
				si := (dOffY+row)*csw + (dOffX + col)
				di := (cdy+row)*dp.width + (cdx + col)
				dp.index[di] = tmpIndex[si]
				copy(dp.color[di*4:di*4+4], tmpColor[si*4:si*4+4])
			}
		}
	})
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "rect_copied", Operation: bus.OpCommand, Data: msg.Data, Source: v.Name(),
	})
}

// Format: [start:u8][count:u8][R:u8][G:u8][B:u8][A:u8]...
func (v *VRAMModule) handleSetPaletteBlock(msg *bus.BusMessage) {
	if len(msg.Data) < 2 {
		return
	}
	start := int(msg.Data[0])
	count := int(msg.Data[1])
	if len(msg.Data) < 2+count*4 {
		return
	}
	for i := range count {
		idx := start + i
		if idx > 255 {
			break
		}
		off := 2 + i*4
		v.palette[idx] = [4]uint8{msg.Data[off], msg.Data[off+1], msg.Data[off+2], msg.Data[off+3]}
	}
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "palette_block_updated", Operation: bus.OpCommand, Data: msg.Data[:2], Source: v.Name(),
	})
}

// Format: [start:u8][count:u8]
func (v *VRAMModule) handleReadPaletteBlock(msg *bus.BusMessage) {
	if len(msg.Data) < 2 {
		return
	}
	start := int(msg.Data[0])
	count := int(msg.Data[1])
	resp := make([]byte, 2+count*4)
	resp[0] = msg.Data[0]
	resp[1] = msg.Data[1]
	for i := range count {
		idx := start + i
		if idx > 255 {
			break
		}
		off := 2 + i*4
		pal := v.palette[idx]
		resp[off] = pal[0]
		resp[off+1] = pal[1]
		resp[off+2] = pal[2]
		resp[off+3] = pal[3]
	}
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "palette_data", Operation: bus.OpCommand, Data: resp, Source: v.Name(),
	})
}

// --- Page management commands ---

// Format: [count:u8] — 0 means 256
func (v *VRAMModule) handleSetPageCount(msg *bus.BusMessage) {
	if len(msg.Data) < 1 {
		return
	}
	count := int(msg.Data[0])
	if count == 0 {
		count = 256
	}
	cur := len(v.pages)
	if count > cur {
		for range count - cur {
			v.pages = append(v.pages, newPageBuffer(DefaultPageWidth, DefaultPageHeight))
		}
	} else if count < cur {
		v.pages = v.pages[:count]
	}
	if v.displayPage >= count {
		v.displayPage = 0
	}
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "page_count_changed", Operation: bus.OpCommand,
		Data: []byte{msg.Data[0]}, Source: v.Name(),
	})
}

// Format: [page:u8]
func (v *VRAMModule) handleSetDisplayPage(msg *bus.BusMessage) {
	if len(msg.Data) < 1 {
		return
	}
	page := int(msg.Data[0])
	if !v.isValidPage(page) {
		v.publishPageError(0x03)
		return
	}
	v.displayPage = page
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "display_page_changed", Operation: bus.OpCommand,
		Data: []byte{msg.Data[0]}, Source: v.Name(),
	})
}

// Format: [page1:u8][page2:u8]
func (v *VRAMModule) handleSwapPages(msg *bus.BusMessage) {
	if len(msg.Data) < 2 {
		return
	}
	p1, p2 := int(msg.Data[0]), int(msg.Data[1])
	if !v.isValidPage(p1) || !v.isValidPage(p2) {
		v.publishPageError(0x01)
		return
	}
	v.pages[p1], v.pages[p2] = v.pages[p2], v.pages[p1]
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "pages_swapped", Operation: bus.OpCommand,
		Data: msg.Data[:2], Source: v.Name(),
	})
}

// Format: [src:u8][dst:u8]
func (v *VRAMModule) handleCopyPage(msg *bus.BusMessage) {
	if len(msg.Data) < 2 {
		return
	}
	src, dst := int(msg.Data[0]), int(msg.Data[1])
	if !v.isValidPage(src) || !v.isValidPage(dst) {
		v.publishPageError(0x01)
		return
	}
	if src == dst {
		return
	}
	sp := &v.pages[src]
	dp := &v.pages[dst]
	dp.width = sp.width
	dp.height = sp.height
	dp.index = make([]uint8, len(sp.index))
	dp.color = make([]uint8, len(sp.color))
	copy(dp.index, sp.index)
	copy(dp.color, sp.color)
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "page_copied", Operation: bus.OpCommand,
		Data: msg.Data[:2], Source: v.Name(),
	})
}

// Format: [page:u8][w:u16][h:u16]
func (v *VRAMModule) handleSetPageSize(msg *bus.BusMessage) {
	if len(msg.Data) < 5 {
		return
	}
	page := int(msg.Data[0])
	if !v.isValidPage(page) {
		v.publishPageError(0x01)
		return
	}
	w := int(binary.BigEndian.Uint16(msg.Data[1:]))
	h := int(binary.BigEndian.Uint16(msg.Data[3:]))
	pg := &v.pages[page]
	pg.width = w
	pg.height = h
	pg.index = make([]uint8, w*h)
	pg.color = make([]uint8, w*h*4)
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "page_size_changed", Operation: bus.OpCommand,
		Data: msg.Data[:5], Source: v.Name(),
	})
}

// Format: [offX:i16][offY:i16] (big-endian signed)
func (v *VRAMModule) handleSetViewport(msg *bus.BusMessage) {
	if len(msg.Data) < 4 {
		return
	}
	v.viewportX = int16(binary.BigEndian.Uint16(msg.Data[0:]))
	v.viewportY = int16(binary.BigEndian.Uint16(msg.Data[2:]))
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "viewport_changed", Operation: bus.OpCommand,
		Data: msg.Data[:4], Source: v.Name(),
	})
}

// --- Helpers ---

func (v *VRAMModule) publishRectEvent(x, y, w, h int) {
	data := make([]byte, 8)
	binary.BigEndian.PutUint16(data[0:], uint16(x))
	binary.BigEndian.PutUint16(data[2:], uint16(y))
	binary.BigEndian.PutUint16(data[4:], uint16(w))
	binary.BigEndian.PutUint16(data[6:], uint16(h))
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "rect_updated", Operation: bus.OpCommand, Data: data, Source: v.Name(),
	})
}

func (v *VRAMModule) publishPageError(code uint8) {
	v.bus.Publish("vram_update", &bus.BusMessage{
		Target: "page_error", Operation: bus.OpCommand, Data: []byte{code}, Source: v.Name(),
	})
}

func (v *VRAMModule) isValidPage(p int) bool {
	return p >= 0 && p < len(v.pages)
}

func clipRect(x, y, w, h, bw, bh int) (cx, cy, cw, ch, srcOffX, srcOffY int) {
	cx, cy = x, y
	cw, ch = w, h
	if cx < 0 {
		srcOffX = -cx
		cw += cx
		cx = 0
	}
	if cy < 0 {
		srcOffY = -cy
		ch += cy
		cy = 0
	}
	if cx+cw > bw {
		cw = bw - cx
	}
	if cy+ch > bh {
		ch = bh - cy
	}
	if cw <= 0 || ch <= 0 {
		return 0, 0, 0, 0, 0, 0
	}
	return
}
