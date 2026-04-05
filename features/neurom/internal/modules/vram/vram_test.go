package vram

import (
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

type testBus struct {
	ch chan *bus.BusMessage
}

func (b *testBus) Publish(topic string, msg *bus.BusMessage) error {
	b.ch <- msg
	return nil
}
func (b *testBus) Subscribe(topic string) (<-chan *bus.BusMessage, error) {
	ch := make(chan *bus.BusMessage, 1)
	return ch, nil
}
func (b *testBus) Close() error { return nil }

func newTestVRAM() (*VRAMModule, *testBus) {
	b := &testBus{ch: make(chan *bus.BusMessage, 100)}
	v := New()
	v.bus = b
	return v, b
}

func drainBus(b *testBus) {
	for len(b.ch) > 0 {
		<-b.ch
	}
}

func expectEvent(t *testing.T, b *testBus, target string) *bus.BusMessage {
	t.Helper()
	select {
	case out := <-b.ch:
		if out.Target != target {
			t.Errorf("Expected event %s, got %s", target, out.Target)
		}
		return out
	case <-time.After(time.Second):
		t.Fatalf("No %s event received", target)
		return nil
	}
}

// --- Existing tests (updated for page-aware wire formats) ---

func TestVRAMModule(t *testing.T) {
	v, b := newTestVRAM()

	// Format: [page:u8][x:u16][y:u16][p:u8]
	msg := &bus.BusMessage{
		Target:    "draw_pixel",
		Operation: bus.OpCommand,
		Data:      []byte{0x00, 0x00, 0x01, 0x00, 0x02, 0x0F},
	}
	v.handleMessage(msg)

	pg := &v.pages[0]
	if pg.index[2*pg.width+1] != 15 {
		t.Errorf("Expected pixel to be 15, got %d", pg.index[2*pg.width+1])
	}

	expectEvent(t, b, "vram_updated")
}

func TestVRAMStatsRecording(t *testing.T) {
	v, _ := newTestVRAM()

	now := time.Now()
	v.stats.SetNowFunc(func() time.Time { return now })

	for range 5 {
		v.handleMessage(&bus.BusMessage{
			Target:    "draw_pixel",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		})
	}
	for range 2 {
		v.handleMessage(&bus.BusMessage{
			Target:    "set_palette",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0xFF, 0x00, 0x00},
		})
	}

	now = now.Add(1 * time.Second)

	snap := v.GetStats()
	if snap["draw_pixel"].Last1s.Count != 5 {
		t.Errorf("draw_pixel Last1s.Count = %d, want 5", snap["draw_pixel"].Last1s.Count)
	}
	if snap["set_palette"].Last1s.Count != 2 {
		t.Errorf("set_palette Last1s.Count = %d, want 2", snap["set_palette"].Last1s.Count)
	}
}

func TestVRAMGetStatsCommand(t *testing.T) {
	v, b := newTestVRAM()

	now := time.Now()
	v.stats.SetNowFunc(func() time.Time { return now })

	v.handleMessage(&bus.BusMessage{
		Target: "draw_pixel", Operation: bus.OpCommand,
		Data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	})

	now = now.Add(1 * time.Second)

	drainBus(b)

	v.handleMessage(&bus.BusMessage{
		Target: "get_stats", Operation: bus.OpCommand,
	})

	out := expectEvent(t, b, "stats_data")
	var resp struct {
		Commands map[string]struct {
			Last1s struct {
				Count int64 `json:"count"`
			} `json:"last_1s"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Data, &resp); err != nil {
		t.Fatalf("failed to unmarshal stats JSON: %v", err)
	}
	dp, ok := resp.Commands["draw_pixel"]
	if !ok {
		t.Fatal("draw_pixel not found in stats response")
	}
	if dp.Last1s.Count != 1 {
		t.Errorf("draw_pixel Last1s.Count = %d, want 1", dp.Last1s.Count)
	}
}

func TestVRAMGetStatsNoLock(t *testing.T) {
	v, _ := newTestVRAM()

	v.mu.RLock()
	done := make(chan struct{})
	go func() {
		v.handleMessage(&bus.BusMessage{Target: "get_stats", Operation: bus.OpCommand})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("get_stats appears to be deadlocked on mu.Lock()")
	}
	v.mu.RUnlock()
}

// --- Part 1 tests (updated for page-aware formats) ---

func TestSetPaletteRGBA(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{10, 0xFF, 0x00, 0x80},
	})
	if v.palette[10] != [4]uint8{0xFF, 0x00, 0x80, 0xFF} {
		t.Errorf("4-byte: got %v, want [255 0 128 255]", v.palette[10])
	}

	v.handleMessage(&bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{20, 0x00, 0xFF, 0x00, 0x80},
	})
	if v.palette[20] != [4]uint8{0x00, 0xFF, 0x00, 0x80} {
		t.Errorf("5-byte: got %v, want [0 255 0 128]", v.palette[20])
	}
}

func TestClearVRAM(t *testing.T) {
	v, b := newTestVRAM()
	v.palette[5] = [4]uint8{100, 150, 200, 255}

	// Format: [page:u8][palette_idx:u8]
	v.handleMessage(&bus.BusMessage{
		Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{0, 5},
	})

	pg := &v.pages[0]
	for i := range pg.width * pg.height {
		if pg.index[i] != 5 {
			t.Fatalf("index[%d] = %d, want 5", i, pg.index[i])
		}
		if pg.color[i*4] != 100 || pg.color[i*4+1] != 150 ||
			pg.color[i*4+2] != 200 || pg.color[i*4+3] != 255 {
			t.Fatalf("color mismatch at %d", i)
		}
	}
	expectEvent(t, b, "vram_cleared")
}

func TestClearVRAMDefault(t *testing.T) {
	v, _ := newTestVRAM()
	v.pages[0].index[0] = 99
	v.handleMessage(&bus.BusMessage{
		Target: "clear_vram", Operation: bus.OpCommand, Data: nil,
	})
	if v.pages[0].index[0] != 0 {
		t.Errorf("index[0] = %d, want 0", v.pages[0].index[0])
	}
}

func blitMsg(page uint8, x, y, w, h uint16, blend BlendMode, pixels []byte) *bus.BusMessage {
	data := make([]byte, 10+len(pixels))
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], x)
	binary.BigEndian.PutUint16(data[3:], y)
	binary.BigEndian.PutUint16(data[5:], w)
	binary.BigEndian.PutUint16(data[7:], h)
	data[9] = byte(blend)
	copy(data[10:], pixels)
	return &bus.BusMessage{Target: "blit_rect", Operation: bus.OpCommand, Data: data}
}

func TestBlitRect(t *testing.T) {
	v, b := newTestVRAM()
	v.palette[5] = [4]uint8{50, 100, 150, 255}

	pixels := make([]byte, 16)
	for i := range pixels {
		pixels[i] = 5
	}
	v.handleMessage(blitMsg(0, 10, 20, 4, 4, BlendReplace, pixels))

	pg := &v.pages[0]
	for row := range 4 {
		for col := range 4 {
			idx := (20+row)*pg.width + (10 + col)
			if pg.index[idx] != 5 {
				t.Errorf("index[%d,%d] = %d, want 5", 10+col, 20+row, pg.index[idx])
			}
		}
	}
	if pg.index[20*pg.width+9] != 0 {
		t.Error("Pixel at (9,20) should be unchanged")
	}
	expectEvent(t, b, "rect_updated")
}

func TestBlitRectAlphaBlend(t *testing.T) {
	v, _ := newTestVRAM()
	v.palette[1] = [4]uint8{255, 0, 0, 255}
	v.palette[2] = [4]uint8{0, 0, 255, 128}

	v.handleMessage(blitMsg(0, 0, 0, 2, 2, BlendReplace, []byte{1, 1, 1, 1}))
	v.handleMessage(blitMsg(0, 0, 0, 2, 2, BlendAlpha, []byte{2, 2, 2, 2}))

	pg := &v.pages[0]
	if pg.index[0] != DirectColorMarker {
		t.Errorf("index[0] = %d, want DirectColorMarker", pg.index[0])
	}
	r, b := pg.color[0], pg.color[2]
	if r < 120 || r > 135 {
		t.Errorf("R = %d, want ~127", r)
	}
	if b < 120 || b > 135 {
		t.Errorf("B = %d, want ~128", b)
	}
}

func TestBlitRectTransform(t *testing.T) {
	v, b := newTestVRAM()
	v.palette[3] = [4]uint8{30, 60, 90, 255}

	sprite := make([]byte, 16)
	sprite[1*4+1] = 3
	sprite[1*4+2] = 3
	sprite[2*4+1] = 3
	sprite[2*4+2] = 3

	data := make([]byte, 19+len(sprite))
	data[0] = 0 // page
	binary.BigEndian.PutUint16(data[1:], 128)
	binary.BigEndian.PutUint16(data[3:], 106)
	binary.BigEndian.PutUint16(data[5:], 4)
	binary.BigEndian.PutUint16(data[7:], 4)
	binary.BigEndian.PutUint16(data[9:], 2)
	binary.BigEndian.PutUint16(data[11:], 2)
	data[13] = 0
	binary.BigEndian.PutUint16(data[14:], 256)
	binary.BigEndian.PutUint16(data[16:], 256)
	data[18] = byte(BlendReplace)
	copy(data[19:], sprite)

	v.handleMessage(&bus.BusMessage{
		Target: "blit_rect_transform", Operation: bus.OpCommand, Data: data,
	})

	pg := &v.pages[0]
	found := false
	for y := 104; y < 110; y++ {
		for x := 126; x < 132; x++ {
			if x < pg.width && y < pg.height && pg.index[y*pg.width+x] == 3 {
				found = true
			}
		}
	}
	if !found {
		t.Error("No transformed pixels found near (128, 106)")
	}
	expectEvent(t, b, "rect_updated")
}

func TestReadRect(t *testing.T) {
	v, b := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{0, 5},
	})
	drainBus(b)

	v.handleMessage(blitMsg(0, 10, 10, 2, 2, BlendReplace, []byte{3, 3, 3, 3}))
	drainBus(b)

	// read_rect: [page:u8][x:u16][y:u16][w:u16][h:u16]
	readData := make([]byte, 9)
	readData[0] = 0
	binary.BigEndian.PutUint16(readData[1:], 9)
	binary.BigEndian.PutUint16(readData[3:], 10)
	binary.BigEndian.PutUint16(readData[5:], 4)
	binary.BigEndian.PutUint16(readData[7:], 2)
	v.handleMessage(&bus.BusMessage{
		Target: "read_rect", Operation: bus.OpCommand, Data: readData,
	})

	out := expectEvent(t, b, "rect_data")
	respPixels := out.Data[8:]
	expected := []uint8{5, 3, 3, 5, 5, 3, 3, 5}
	for i, exp := range expected {
		if respPixels[i] != exp {
			t.Errorf("pixel[%d] = %d, want %d", i, respPixels[i], exp)
		}
	}
}

func TestCopyRect(t *testing.T) {
	v, b := newTestVRAM()
	v.palette[7] = [4]uint8{70, 80, 90, 255}

	pixels := make([]byte, 64)
	for i := range pixels {
		pixels[i] = 7
	}
	v.handleMessage(blitMsg(0, 0, 0, 8, 8, BlendReplace, pixels))
	drainBus(b)

	// copy_rect: [src_page:u8][dst_page:u8][src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]
	copyData := make([]byte, 14)
	copyData[0] = 0
	copyData[1] = 0
	binary.BigEndian.PutUint16(copyData[2:], 0)
	binary.BigEndian.PutUint16(copyData[4:], 0)
	binary.BigEndian.PutUint16(copyData[6:], 20)
	binary.BigEndian.PutUint16(copyData[8:], 20)
	binary.BigEndian.PutUint16(copyData[10:], 8)
	binary.BigEndian.PutUint16(copyData[12:], 8)
	v.handleMessage(&bus.BusMessage{
		Target: "copy_rect", Operation: bus.OpCommand, Data: copyData,
	})

	pg := &v.pages[0]
	for row := range 8 {
		for col := range 8 {
			idx := (20+row)*pg.width + (20 + col)
			if pg.index[idx] != 7 {
				t.Fatalf("copy dest (%d,%d) = %d, want 7", 20+col, 20+row, pg.index[idx])
			}
		}
	}
	expectEvent(t, b, "rect_copied")
}

func TestCopyRectOverlap(t *testing.T) {
	v, _ := newTestVRAM()

	pg := &v.pages[0]
	for row := range 8 {
		for col := range 8 {
			pg.index[row*pg.width+col] = uint8(row)
		}
	}

	copyData := make([]byte, 14)
	copyData[0] = 0
	copyData[1] = 0
	binary.BigEndian.PutUint16(copyData[2:], 0)
	binary.BigEndian.PutUint16(copyData[4:], 0)
	binary.BigEndian.PutUint16(copyData[6:], 0)
	binary.BigEndian.PutUint16(copyData[8:], 2)
	binary.BigEndian.PutUint16(copyData[10:], 8)
	binary.BigEndian.PutUint16(copyData[12:], 8)
	v.handleMessage(&bus.BusMessage{
		Target: "copy_rect", Operation: bus.OpCommand, Data: copyData,
	})

	for row := range 8 {
		val := pg.index[(2+row)*pg.width]
		if val != uint8(row) {
			t.Errorf("after overlap copy, row %d = %d, want %d", 2+row, val, row)
		}
	}
}

func TestSetPaletteBlock(t *testing.T) {
	v, b := newTestVRAM()

	colors := make([]byte, 2+16*4)
	colors[0] = 10
	colors[1] = 16
	for i := range 16 {
		off := 2 + i*4
		colors[off] = uint8(i * 10)
		colors[off+1] = uint8(i * 5)
		colors[off+2] = uint8(i * 3)
		colors[off+3] = 255
	}
	v.handleMessage(&bus.BusMessage{
		Target: "set_palette_block", Operation: bus.OpCommand, Data: colors,
	})

	for i := range 16 {
		expected := [4]uint8{uint8(i * 10), uint8(i * 5), uint8(i * 3), 255}
		if v.palette[10+i] != expected {
			t.Errorf("palette[%d] = %v, want %v", 10+i, v.palette[10+i], expected)
		}
	}
	expectEvent(t, b, "palette_block_updated")
}

func TestReadPaletteBlock(t *testing.T) {
	v, b := newTestVRAM()

	v.palette[5] = [4]uint8{10, 20, 30, 255}
	v.palette[6] = [4]uint8{40, 50, 60, 128}
	v.palette[7] = [4]uint8{70, 80, 90, 64}

	v.handleMessage(&bus.BusMessage{
		Target: "read_palette_block", Operation: bus.OpCommand, Data: []byte{5, 3},
	})

	out := expectEvent(t, b, "palette_data")
	expectations := [][4]uint8{{10, 20, 30, 255}, {40, 50, 60, 128}, {70, 80, 90, 64}}
	for i, exp := range expectations {
		off := 2 + i*4
		got := [4]uint8{out.Data[off], out.Data[off+1], out.Data[off+2], out.Data[off+3]}
		if got != exp {
			t.Errorf("palette_data[%d] = %v, want %v", i, got, exp)
		}
	}
}

func TestBlitRectClipping(t *testing.T) {
	v, _ := newTestVRAM()
	v.palette[9] = [4]uint8{90, 90, 90, 255}

	pixels := make([]byte, 256)
	for i := range pixels {
		pixels[i] = 9
	}
	v.handleMessage(blitMsg(0, 250, 200, 16, 16, BlendReplace, pixels))

	pg := &v.pages[0]
	for y := 200; y < 212; y++ {
		for x := 250; x < 256; x++ {
			if pg.index[y*pg.width+x] != 9 {
				t.Errorf("(%d,%d) = %d, want 9", x, y, pg.index[y*pg.width+x])
			}
		}
	}

	v.handleMessage(blitMsg(0, 300, 300, 4, 4, BlendReplace, make([]byte, 16)))
}

func TestCopyRectClipping(t *testing.T) {
	v, _ := newTestVRAM()
	v.palette[4] = [4]uint8{40, 50, 60, 255}

	pg := &v.pages[0]
	for y := range 8 {
		for x := range 8 {
			pg.index[y*pg.width+x] = 4
		}
	}

	copyData := make([]byte, 14)
	copyData[0] = 0
	copyData[1] = 0
	binary.BigEndian.PutUint16(copyData[2:], 0)
	binary.BigEndian.PutUint16(copyData[4:], 0)
	binary.BigEndian.PutUint16(copyData[6:], 252)
	binary.BigEndian.PutUint16(copyData[8:], 208)
	binary.BigEndian.PutUint16(copyData[10:], 8)
	binary.BigEndian.PutUint16(copyData[12:], 8)
	v.handleMessage(&bus.BusMessage{
		Target: "copy_rect", Operation: bus.OpCommand, Data: copyData,
	})

	for y := 208; y < 212; y++ {
		for x := 252; x < 256; x++ {
			if pg.index[y*pg.width+x] != 4 {
				t.Errorf("copy clip (%d,%d) = %d, want 4", x, y, pg.index[y*pg.width+x])
			}
		}
	}
}

// --- Part 2: Page management tests ---

func TestPageManagement(t *testing.T) {
	v, b := newTestVRAM()

	if len(v.pages) != 1 {
		t.Fatalf("initial pages = %d, want 1", len(v.pages))
	}

	// set_page_count(2)
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})
	if len(v.pages) != 2 {
		t.Fatalf("after set_page_count(2): pages = %d, want 2", len(v.pages))
	}
	expectEvent(t, b, "page_count_changed")

	// set_page_count(1): tail page discarded
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{1},
	})
	if len(v.pages) != 1 {
		t.Fatalf("after set_page_count(1): pages = %d, want 1", len(v.pages))
	}
	drainBus(b)

	// displayPage reset when >= count
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{3},
	})
	v.displayPage = 2
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})
	if v.displayPage != 0 {
		t.Errorf("displayPage = %d, want 0 after shrink", v.displayPage)
	}
}

func TestSetDisplayPage(t *testing.T) {
	v, b := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{3},
	})
	drainBus(b)

	v.handleMessage(&bus.BusMessage{
		Target: "set_display_page", Operation: bus.OpCommand, Data: []byte{1},
	})
	if v.displayPage != 1 {
		t.Errorf("displayPage = %d, want 1", v.displayPage)
	}
	expectEvent(t, b, "display_page_changed")

	// Invalid page → error
	v.handleMessage(&bus.BusMessage{
		Target: "set_display_page", Operation: bus.OpCommand, Data: []byte{5},
	})
	expectEvent(t, b, "page_error")
	if v.displayPage != 1 {
		t.Errorf("displayPage = %d, want 1 (unchanged)", v.displayPage)
	}
}

func TestSwapPages(t *testing.T) {
	v, b := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})
	drainBus(b)

	// Write pattern A to page 0, pattern B to page 1
	v.pages[0].index[0] = 10
	v.pages[1].index[0] = 20

	v.handleMessage(&bus.BusMessage{
		Target: "swap_pages", Operation: bus.OpCommand, Data: []byte{0, 1},
	})

	if v.pages[0].index[0] != 20 {
		t.Errorf("page 0 after swap = %d, want 20", v.pages[0].index[0])
	}
	if v.pages[1].index[0] != 10 {
		t.Errorf("page 1 after swap = %d, want 10", v.pages[1].index[0])
	}
	expectEvent(t, b, "pages_swapped")
}

func TestCopyPage(t *testing.T) {
	v, b := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})
	drainBus(b)

	// Write pattern to page 0
	for i := range 100 {
		v.pages[0].index[i] = uint8(i % 256)
	}

	v.handleMessage(&bus.BusMessage{
		Target: "copy_page", Operation: bus.OpCommand, Data: []byte{0, 1},
	})

	for i := range 100 {
		if v.pages[1].index[i] != uint8(i%256) {
			t.Errorf("page 1 index[%d] = %d, want %d", i, v.pages[1].index[i], i%256)
		}
	}
	expectEvent(t, b, "page_copied")

	// Same page: no-op
	drainBus(b)
	v.handleMessage(&bus.BusMessage{
		Target: "copy_page", Operation: bus.OpCommand, Data: []byte{0, 0},
	})
	if len(b.ch) != 0 {
		t.Error("copy_page(0,0) should not emit events")
	}
}

func TestDrawPixelWithPage(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})

	// Draw on page 1: [page:u8][x:u16][y:u16][p:u8]
	v.handleMessage(&bus.BusMessage{
		Target: "draw_pixel", Operation: bus.OpCommand,
		Data: []byte{1, 0x00, 0x05, 0x00, 0x03, 42},
	})

	if v.pages[1].index[3*DefaultPageWidth+5] != 42 {
		t.Errorf("page 1 pixel = %d, want 42", v.pages[1].index[3*DefaultPageWidth+5])
	}
	if v.pages[0].index[3*DefaultPageWidth+5] != 0 {
		t.Error("page 0 should be unchanged")
	}
}

func TestClearVRAMWithPage(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})

	v.handleMessage(&bus.BusMessage{
		Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{1, 7},
	})

	for i := range v.pages[1].width * v.pages[1].height {
		if v.pages[1].index[i] != 7 {
			t.Fatalf("page 1 index[%d] = %d, want 7", i, v.pages[1].index[i])
		}
	}
	if v.pages[0].index[0] != 0 {
		t.Error("page 0 should be unchanged")
	}
}

func TestBlitRectWithPage(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})

	pixels := []byte{5, 5, 5, 5}
	v.handleMessage(blitMsg(1, 0, 0, 2, 2, BlendReplace, pixels))

	if v.pages[1].index[0] != 5 {
		t.Errorf("page 1 = %d, want 5", v.pages[1].index[0])
	}
	if v.pages[0].index[0] != 0 {
		t.Error("page 0 should be unchanged")
	}
}

func TestCopyRectCrossPage(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})

	// Fill page 0 with pattern
	for i := range 16 {
		v.pages[0].index[i] = uint8(i + 1)
	}

	// copy_rect from page 0 to page 1: [src:u8][dst:u8][sx:u16][sy:u16][dx:u16][dy:u16][w:u16][h:u16]
	copyData := make([]byte, 14)
	copyData[0] = 0
	copyData[1] = 1
	binary.BigEndian.PutUint16(copyData[2:], 0)
	binary.BigEndian.PutUint16(copyData[4:], 0)
	binary.BigEndian.PutUint16(copyData[6:], 0)
	binary.BigEndian.PutUint16(copyData[8:], 0)
	binary.BigEndian.PutUint16(copyData[10:], 4)
	binary.BigEndian.PutUint16(copyData[12:], 4)

	v.handleMessage(&bus.BusMessage{
		Target: "copy_rect", Operation: bus.OpCommand, Data: copyData,
	})

	pg1 := &v.pages[1]
	for row := range 4 {
		for col := range 4 {
			srcVal := uint8(row*DefaultPageWidth + col + 1)
			dstVal := pg1.index[row*pg1.width+col]
			expected := v.pages[0].index[row*DefaultPageWidth+col]
			if dstVal != expected {
				t.Errorf("cross page copy (%d,%d) = %d, want %d (src=%d)", col, row, dstVal, expected, srcVal)
			}
		}
	}
}

func TestPageSizeManagement(t *testing.T) {
	v, b := newTestVRAM()

	// set_page_size: [page:u8][w:u16][h:u16]
	data := make([]byte, 5)
	data[0] = 0
	binary.BigEndian.PutUint16(data[1:], 512)
	binary.BigEndian.PutUint16(data[3:], 512)
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_size", Operation: bus.OpCommand, Data: data,
	})

	pg := &v.pages[0]
	if pg.width != 512 || pg.height != 512 {
		t.Errorf("page 0 size = %dx%d, want 512x512", pg.width, pg.height)
	}
	if len(pg.index) != 512*512 {
		t.Errorf("index len = %d, want %d", len(pg.index), 512*512)
	}
	expectEvent(t, b, "page_size_changed")
}

func TestCopyPageResizes(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})

	// Resize page 0 to 512x512
	data := make([]byte, 5)
	data[0] = 0
	binary.BigEndian.PutUint16(data[1:], 512)
	binary.BigEndian.PutUint16(data[3:], 512)
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_size", Operation: bus.OpCommand, Data: data,
	})

	// Copy page 0 → page 1: page 1 should resize
	v.handleMessage(&bus.BusMessage{
		Target: "copy_page", Operation: bus.OpCommand, Data: []byte{0, 1},
	})

	if v.pages[1].width != 512 || v.pages[1].height != 512 {
		t.Errorf("page 1 size = %dx%d, want 512x512", v.pages[1].width, v.pages[1].height)
	}
}

func TestCopyRectCrossPageClipping(t *testing.T) {
	v, _ := newTestVRAM()

	v.handleMessage(&bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand, Data: []byte{2},
	})

	// Resize page 0 to 512x512
	data := make([]byte, 5)
	data[0] = 0
	binary.BigEndian.PutUint16(data[1:], 512)
	binary.BigEndian.PutUint16(data[3:], 512)
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_size", Operation: bus.OpCommand, Data: data,
	})

	// Fill (400,400,100,100) on page 0
	for y := 400; y < 500; y++ {
		for x := 400; x < 500; x++ {
			v.pages[0].index[y*512+x] = 8
		}
	}

	// Copy from page 0 (400,400) 100x100 → page 1 (200,200)
	// Page 1 is 256x212, so clipping: 200+100=300 > 256 → 56 wide, 200+100=300 > 212 → 12 high
	copyData := make([]byte, 14)
	copyData[0] = 0
	copyData[1] = 1
	binary.BigEndian.PutUint16(copyData[2:], 400)
	binary.BigEndian.PutUint16(copyData[4:], 400)
	binary.BigEndian.PutUint16(copyData[6:], 200)
	binary.BigEndian.PutUint16(copyData[8:], 200)
	binary.BigEndian.PutUint16(copyData[10:], 100)
	binary.BigEndian.PutUint16(copyData[12:], 100)
	v.handleMessage(&bus.BusMessage{
		Target: "copy_rect", Operation: bus.OpCommand, Data: copyData,
	})

	pg1 := &v.pages[1]
	for y := 200; y < 212; y++ {
		for x := 200; x < 256; x++ {
			if pg1.index[y*pg1.width+x] != 8 {
				t.Errorf("cross page clip (%d,%d) = %d, want 8", x, y, pg1.index[y*pg1.width+x])
			}
		}
	}
}

func TestViewportOffset(t *testing.T) {
	v, b := newTestVRAM()

	// set_viewport: [offX:i16][offY:i16]
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:], uint16(int16(100)))
	binary.BigEndian.PutUint16(data[2:], uint16(int16(50)))
	v.handleMessage(&bus.BusMessage{
		Target: "set_viewport", Operation: bus.OpCommand, Data: data,
	})

	vpX, vpY := v.ViewportOffset()
	if vpX != 100 || vpY != 50 {
		t.Errorf("ViewportOffset = (%d,%d), want (100,50)", vpX, vpY)
	}
	expectEvent(t, b, "viewport_changed")
}

func TestInvalidPageError(t *testing.T) {
	v, b := newTestVRAM()

	// draw_pixel on invalid page
	v.handleMessage(&bus.BusMessage{
		Target: "draw_pixel", Operation: bus.OpCommand,
		Data: []byte{5, 0x00, 0x00, 0x00, 0x00, 1},
	})
	expectEvent(t, b, "page_error")

	// clear_vram on invalid page
	v.handleMessage(&bus.BusMessage{
		Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{5, 0},
	})
	expectEvent(t, b, "page_error")

	// blit_rect on invalid page
	v.handleMessage(blitMsg(5, 0, 0, 1, 1, BlendReplace, []byte{0}))
	expectEvent(t, b, "page_error")
}

// --- Multi-core tests ---

func newTestVRAMWithWorkers(workers int) (*VRAMModule, *testBus) {
	b := &testBus{ch: make(chan *bus.BusMessage, 100)}
	v := New(Config{Workers: workers})
	v.bus = b
	return v, b
}

func TestCPUConfigValidation(t *testing.T) {
	t.Run("Workers=0 fallback", func(t *testing.T) {
		v := New(Config{Workers: 0})
		if v.pool != nil {
			t.Error("pool should be nil for workers=0 (fallback to 1)")
		}
	})
	t.Run("Workers=257 fallback", func(t *testing.T) {
		v := New(Config{Workers: 257})
		if v.pool != nil {
			t.Error("pool should be nil for workers=257 (fallback to 1)")
		}
	})
	t.Run("Workers=256 valid", func(t *testing.T) {
		v := New(Config{Workers: 256})
		if v.pool == nil {
			t.Fatal("pool should not be nil for workers=256")
		}
		if v.pool.size != 256 {
			t.Errorf("pool size = %d, want 256", v.pool.size)
		}
		v.pool.stop()
	})
	t.Run("Workers=1 no pool", func(t *testing.T) {
		v := New(Config{Workers: 1})
		if v.pool != nil {
			t.Error("pool should be nil for workers=1")
		}
	})
	t.Run("No config", func(t *testing.T) {
		v := New()
		if v.pool != nil {
			t.Error("pool should be nil with no config")
		}
	})
	t.Run("Strategy empty defaults to dynamic", func(t *testing.T) {
		v := New(Config{Workers: 4, Strategy: ""})
		defer v.pool.stop()
		if _, ok := v.strategy.(*dynamicDispatcher); !ok {
			t.Errorf("expected *dynamicDispatcher, got %T", v.strategy)
		}
	})
	t.Run("Strategy static", func(t *testing.T) {
		v := New(Config{Workers: 4, Strategy: "static"})
		defer v.pool.stop()
		if _, ok := v.strategy.(*staticDispatcher); !ok {
			t.Errorf("expected *staticDispatcher, got %T", v.strategy)
		}
	})
	t.Run("Strategy dynamic", func(t *testing.T) {
		v := New(Config{Workers: 4, Strategy: "dynamic"})
		defer v.pool.stop()
		if _, ok := v.strategy.(*dynamicDispatcher); !ok {
			t.Errorf("expected *dynamicDispatcher, got %T", v.strategy)
		}
	})
}

func TestClearVRAMMultiCore(t *testing.T) {
	v1, b1 := newTestVRAM()
	v4, b4 := newTestVRAMWithWorkers(4)
	defer v4.pool.stop()

	v1.palette[3] = [4]uint8{30, 60, 90, 200}
	v4.palette[3] = [4]uint8{30, 60, 90, 200}

	v1.handleMessage(&bus.BusMessage{Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{0, 3}})
	v4.handleMessage(&bus.BusMessage{Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{0, 3}})
	drainBus(b1)
	drainBus(b4)

	pg1 := &v1.pages[0]
	pg4 := &v4.pages[0]
	for i := range pg1.index {
		if pg1.index[i] != pg4.index[i] {
			t.Fatalf("index mismatch at %d: %d vs %d", i, pg1.index[i], pg4.index[i])
		}
	}
	for i := range pg1.color {
		if pg1.color[i] != pg4.color[i] {
			t.Fatalf("color mismatch at %d: %d vs %d", i, pg1.color[i], pg4.color[i])
		}
	}
}

func TestBlitRectMultiCore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		blend BlendMode
	}{
		{"Replace", BlendReplace},
		{"Alpha", BlendAlpha},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v1, b1 := newTestVRAM()
			v4, b4 := newTestVRAMWithWorkers(4)
			defer v4.pool.stop()

			for i := range 16 {
				v1.palette[uint8(i)] = [4]uint8{uint8(i * 16), uint8(i * 8), uint8(i * 4), 128}
				v4.palette[uint8(i)] = [4]uint8{uint8(i * 16), uint8(i * 8), uint8(i * 4), 128}
			}

			pixels := make([]byte, 32*32)
			for i := range pixels {
				pixels[i] = uint8(i % 16)
			}

			v1.handleMessage(blitMsg(0, 10, 10, 32, 32, tc.blend, pixels))
			v4.handleMessage(blitMsg(0, 10, 10, 32, 32, tc.blend, pixels))
			drainBus(b1)
			drainBus(b4)

			pg1 := &v1.pages[0]
			pg4 := &v4.pages[0]
			for i := range pg1.index {
				if pg1.index[i] != pg4.index[i] {
					t.Fatalf("index mismatch at %d: %d vs %d", i, pg1.index[i], pg4.index[i])
				}
			}
			for i := range pg1.color {
				if pg1.color[i] != pg4.color[i] {
					t.Fatalf("color mismatch at %d: %d vs %d", i, pg1.color[i], pg4.color[i])
				}
			}
		})
	}
}

func TestBlitRectTransformMultiCore(t *testing.T) {
	v1, b1 := newTestVRAM()
	v4, b4 := newTestVRAMWithWorkers(4)
	defer v4.pool.stop()

	v1.palette[1] = [4]uint8{255, 0, 0, 255}
	v4.palette[1] = [4]uint8{255, 0, 0, 255}

	pixels := make([]byte, 16*16)
	for i := range pixels {
		pixels[i] = 1
	}

	buildTransformMsg := func(page uint8, dstX, dstY, srcW, srcH, pivX, pivY uint16, rot uint8, scX, scY uint16, blend BlendMode, data []byte) *bus.BusMessage {
		d := make([]byte, 19+len(data))
		d[0] = page
		binary.BigEndian.PutUint16(d[1:], dstX)
		binary.BigEndian.PutUint16(d[3:], dstY)
		binary.BigEndian.PutUint16(d[5:], srcW)
		binary.BigEndian.PutUint16(d[7:], srcH)
		binary.BigEndian.PutUint16(d[9:], pivX)
		binary.BigEndian.PutUint16(d[11:], pivY)
		d[13] = rot
		binary.BigEndian.PutUint16(d[14:], scX)
		binary.BigEndian.PutUint16(d[16:], scY)
		d[18] = byte(blend)
		copy(d[19:], data)
		return &bus.BusMessage{Target: "blit_rect_transform", Operation: bus.OpCommand, Data: d}
	}

	msg1 := buildTransformMsg(0, 50, 50, 16, 16, 8, 8, 32, 0x0100, 0x0100, BlendReplace, pixels)
	msg4 := buildTransformMsg(0, 50, 50, 16, 16, 8, 8, 32, 0x0100, 0x0100, BlendReplace, pixels)
	v1.handleMessage(msg1)
	v4.handleMessage(msg4)
	drainBus(b1)
	drainBus(b4)

	pg1 := &v1.pages[0]
	pg4 := &v4.pages[0]
	for i := range pg1.index {
		if pg1.index[i] != pg4.index[i] {
			t.Fatalf("index mismatch at %d: %d vs %d", i, pg1.index[i], pg4.index[i])
		}
	}
	for i := range pg1.color {
		if pg1.color[i] != pg4.color[i] {
			t.Fatalf("color mismatch at %d: %d vs %d", i, pg1.color[i], pg4.color[i])
		}
	}
}

func TestCopyRectMultiCore(t *testing.T) {
	v1, b1 := newTestVRAM()
	v4, b4 := newTestVRAMWithWorkers(4)
	defer v4.pool.stop()

	v1.palette[2] = [4]uint8{0, 255, 0, 255}
	v4.palette[2] = [4]uint8{0, 255, 0, 255}

	pixels := make([]byte, 20*20)
	for i := range pixels {
		pixels[i] = 2
	}
	v1.handleMessage(blitMsg(0, 0, 0, 20, 20, BlendReplace, pixels))
	v4.handleMessage(blitMsg(0, 0, 0, 20, 20, BlendReplace, pixels))
	drainBus(b1)
	drainBus(b4)

	copyMsg := func() *bus.BusMessage {
		d := make([]byte, 14)
		d[0] = 0 // src_page
		d[1] = 0 // dst_page
		binary.BigEndian.PutUint16(d[2:], 0)  // src_x
		binary.BigEndian.PutUint16(d[4:], 0)  // src_y
		binary.BigEndian.PutUint16(d[6:], 50) // dst_x
		binary.BigEndian.PutUint16(d[8:], 50) // dst_y
		binary.BigEndian.PutUint16(d[10:], 20)
		binary.BigEndian.PutUint16(d[12:], 20)
		return &bus.BusMessage{Target: "copy_rect", Operation: bus.OpCommand, Data: d}
	}

	v1.handleMessage(copyMsg())
	v4.handleMessage(copyMsg())
	drainBus(b1)
	drainBus(b4)

	pg1 := &v1.pages[0]
	pg4 := &v4.pages[0]
	for i := range pg1.index {
		if pg1.index[i] != pg4.index[i] {
			t.Fatalf("index mismatch at %d: %d vs %d", i, pg1.index[i], pg4.index[i])
		}
	}
	for i := range pg1.color {
		if pg1.color[i] != pg4.color[i] {
			t.Fatalf("color mismatch at %d: %d vs %d", i, pg1.color[i], pg4.color[i])
		}
	}
}
