package vram

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/axsh/neurom/arcproto"
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
	ch, err := b.Subscribe(arcproto.TopicVRAM)
	if err != nil {
		return err
	}
	sysCh, err := b.Subscribe(arcproto.TopicSystem)
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
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventStatsData, Operation: bus.OpCommand, Data: data, Source: v.Name(),
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
	if msg.Target == arcproto.TargetGetStats {
		v.publishStats()
		return
	}

	start := hrNow()
	defer func() { v.stats.Record(msg.Target, hrSince(start)) }()

	v.mu.Lock()
	defer v.mu.Unlock()

	switch msg.Target {
	case arcproto.TargetMode:
		// The payload is not inspected: the command only reinitialises page 0.
		pg := &v.pages[0]
		pg.index = make([]uint8, pg.width*pg.height)
		pg.color = make([]uint8, pg.width*pg.height*4)
		v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
			Target: arcproto.EventModeChanged, Operation: bus.OpCommand, Source: v.Name(),
		})
	case arcproto.TargetDrawPixel:
		v.handleDrawPixel(msg)
	case arcproto.TargetSetPalette:
		v.handleSetPalette(msg)
	case arcproto.TargetClearVRAM:
		v.handleClearVRAM(msg)
	case arcproto.TargetBlitRect:
		v.handleBlitRect(msg)
	case arcproto.TargetBlitRectTransform:
		v.handleBlitRectTransform(msg)
	case arcproto.TargetReadRect:
		v.handleReadRect(msg)
	case arcproto.TargetCopyRect:
		v.handleCopyRect(msg)
	case arcproto.TargetSetPaletteBlock:
		v.handleSetPaletteBlock(msg)
	case arcproto.TargetReadPaletteBlock:
		v.handleReadPaletteBlock(msg)
	case arcproto.TargetSetPageCount:
		v.handleSetPageCount(msg)
	case arcproto.TargetSetDisplayPage:
		v.handleSetDisplayPage(msg)
	case arcproto.TargetSwapPages:
		v.handleSwapPages(msg)
	case arcproto.TargetCopyPage:
		v.handleCopyPage(msg)
	case arcproto.TargetSetPageSize:
		v.handleSetPageSize(msg)
	case arcproto.TargetSetViewport:
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
	start := hrNow()
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
	v.strategy.Feedback(command, dataSize, workers, hrSince(start))
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

func (v *VRAMModule) handleDrawPixel(msg *bus.BusMessage) {
	c, err := arcproto.DecodeDrawPixel(msg.Data)
	if err != nil {
		return
	}
	page := int(c.Page)
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	pg := &v.pages[page]
	x, y := int(c.X), int(c.Y)
	p := c.P
	if x < pg.width && y < pg.height {
		idx := y*pg.width + x
		pg.index[idx] = p
		pal := v.palette[p]
		pg.color[idx*4] = pal[0]
		pg.color[idx*4+1] = pal[1]
		pg.color[idx*4+2] = pal[2]
		pg.color[idx*4+3] = pal[3]
		v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
			Target: arcproto.EventVRAMUpdated, Operation: bus.OpCommand, Data: msg.Data, Source: v.Name(),
		})
	}
}

func (v *VRAMModule) handleSetPalette(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetPalette(msg.Data)
	if err != nil {
		return
	}
	v.palette[c.Index] = [4]uint8{c.Color.R, c.Color.G, c.Color.B, c.Color.A}
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventPaletteUpdated, Operation: bus.OpCommand, Data: msg.Data, Source: v.Name(),
	})
}

func (v *VRAMModule) handleClearVRAM(msg *bus.BusMessage) {
	// This decoder never fails: both fields default to zero when absent.
	c, _ := arcproto.DecodeClearVRAM(msg.Data)
	page := int(c.Page)
	paletteIdx := c.PaletteIdx
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidPage)
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
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventVRAMCleared, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleBlitRect(msg *bus.BusMessage) {
	c, err := arcproto.DecodeBlitRect(msg.Data)
	if err != nil {
		return
	}
	page := int(c.Page)
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	pg := &v.pages[page]
	dstX, dstY := int(c.X), int(c.Y)
	w, h := int(c.W), int(c.H)
	blendMode := c.Blend
	pixelData := c.Pixels

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

func (v *VRAMModule) handleBlitRectTransform(msg *bus.BusMessage) {
	c, err := arcproto.DecodeBlitRectTransform(msg.Data)
	if err != nil {
		return
	}
	page := int(c.Page)
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	pg := &v.pages[page]
	dstX, dstY := int(c.X), int(c.Y)
	srcW, srcH := int(c.SrcW), int(c.SrcH)
	pivotX, pivotY := int(c.PivotX), int(c.PivotY)
	blendMode := c.Blend
	pixelData := c.Pixels

	if len(pixelData) < srcW*srcH {
		return
	}

	transformed, outW, outH, offX, offY := TransformBlitParallel(
		pixelData, srcW, srcH, pivotX, pivotY,
		uint8(c.Rotation), uint16(c.ScaleX), uint16(c.ScaleY),
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

func (v *VRAMModule) handleReadRect(msg *bus.BusMessage) {
	c, err := arcproto.DecodeReadRect(msg.Data)
	if err != nil {
		return
	}
	page := int(c.Page)
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	pg := &v.pages[page]
	x, y := int(c.X), int(c.Y)
	w, h := int(c.W), int(c.H)

	pixels := make([]uint8, w*h)
	for row := range h {
		for col := range w {
			rx, ry := x+col, y+row
			val := uint8(0)
			if rx >= 0 && rx < pg.width && ry >= 0 && ry < pg.height {
				val = pg.index[ry*pg.width+rx]
			}
			pixels[row*w+col] = val
		}
	}
	e := arcproto.RectData{X: c.X, Y: c.Y, W: c.W, H: c.H, Pixels: pixels}
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: e.Target(), Operation: bus.OpCommand, Data: e.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleCopyRect(msg *bus.BusMessage) {
	c, err := arcproto.DecodeCopyRect(msg.Data)
	if err != nil {
		return
	}
	srcPage := int(c.SrcPage)
	dstPage := int(c.DstPage)
	if !v.isValidPage(srcPage) || !v.isValidPage(dstPage) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	sp := &v.pages[srcPage]
	dp := &v.pages[dstPage]
	srcX, srcY := int(c.SrcX), int(c.SrcY)
	dstX, dstY := int(c.DstX), int(c.DstY)
	w, h := int(c.W), int(c.H)

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
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventRectCopied, Operation: bus.OpCommand, Data: msg.Data, Source: v.Name(),
	})
}

func (v *VRAMModule) handleSetPaletteBlock(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetPaletteBlock(msg.Data)
	if err != nil {
		return
	}
	start := int(c.Start)
	for i, col := range c.Colors {
		idx := start + i
		if idx > 255 {
			break
		}
		v.palette[idx] = [4]uint8{col.R, col.G, col.B, col.A}
	}
	// Only the header is echoed, so subscribers learn which range changed
	// without paying for a copy of the colour data.
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventPaletteBlockUpdated, Operation: bus.OpCommand,
		Data: msg.Data[:2], Source: v.Name(),
	})
}

func (v *VRAMModule) handleReadPaletteBlock(msg *bus.BusMessage) {
	c, err := arcproto.DecodeReadPaletteBlock(msg.Data)
	if err != nil {
		return
	}
	start := int(c.Start)
	// Entries past index 255 are left zeroed, which is what the caller has
	// always received for an over-long request.
	colors := make([]arcproto.Color, int(c.Count))
	for i := range colors {
		idx := start + i
		if idx > 255 {
			break
		}
		pal := v.palette[idx]
		colors[i] = arcproto.Color{R: pal[0], G: pal[1], B: pal[2], A: pal[3]}
	}
	e := arcproto.PaletteData{Start: c.Start, Colors: colors}
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: e.Target(), Operation: bus.OpCommand, Data: e.Encode(), Source: v.Name(),
	})
}

// --- Page management commands ---

func (v *VRAMModule) handleSetPageCount(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetPageCount(msg.Data)
	if err != nil {
		return
	}
	count := c.ResolvedCount()
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
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventPageCountChanged, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleSetDisplayPage(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetDisplayPage(msg.Data)
	if err != nil {
		return
	}
	page := int(c.Page)
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidDisplay)
		return
	}
	v.displayPage = page
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventDisplayPageChanged, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleSwapPages(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSwapPages(msg.Data)
	if err != nil {
		return
	}
	p1, p2 := int(c.Page1), int(c.Page2)
	if !v.isValidPage(p1) || !v.isValidPage(p2) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	v.pages[p1], v.pages[p2] = v.pages[p2], v.pages[p1]
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventPagesSwapped, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleCopyPage(msg *bus.BusMessage) {
	c, err := arcproto.DecodeCopyPage(msg.Data)
	if err != nil {
		return
	}
	src, dst := int(c.Src), int(c.Dst)
	if !v.isValidPage(src) || !v.isValidPage(dst) {
		v.publishPageError(arcproto.PageErrInvalidPage)
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
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventPageCopied, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleSetPageSize(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetPageSize(msg.Data)
	if err != nil {
		return
	}
	page := int(c.Page)
	if !v.isValidPage(page) {
		v.publishPageError(arcproto.PageErrInvalidPage)
		return
	}
	w, h := int(c.W), int(c.H)
	pg := &v.pages[page]
	pg.width = w
	pg.height = h
	pg.index = make([]uint8, w*h)
	pg.color = make([]uint8, w*h*4)
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventPageSizeChanged, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) handleSetViewport(msg *bus.BusMessage) {
	c, err := arcproto.DecodeSetViewport(msg.Data)
	if err != nil {
		return
	}
	v.viewportX = c.OffX
	v.viewportY = c.OffY
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: arcproto.EventViewportChanged, Operation: bus.OpCommand,
		Data: c.Encode(), Source: v.Name(),
	})
}

// --- Helpers ---

func (v *VRAMModule) publishRectEvent(x, y, w, h int) {
	e := arcproto.RectUpdated{X: uint16(x), Y: uint16(y), W: uint16(w), H: uint16(h)}
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: e.Target(), Operation: bus.OpCommand, Data: e.Encode(), Source: v.Name(),
	})
}

func (v *VRAMModule) publishPageError(code uint8) {
	e := arcproto.PageError{Code: code}
	v.bus.Publish(arcproto.TopicVRAMUpdate, &bus.BusMessage{
		Target: e.Target(), Operation: bus.OpCommand, Data: e.Encode(), Source: v.Name(),
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
