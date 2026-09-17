package vram

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

func TestResizeTo(t *testing.T) {
	cases := []struct {
		name      string
		input     []uint8
		n         int
		wantLen   int
		wantSame  bool // same backing array when both sides have len > 0
		wantCapGe int  // minimum cap after resize; 0 means do not check
	}{
		{name: "nil_to_4", input: nil, n: 4, wantLen: 4, wantSame: false, wantCapGe: 4},
		{name: "cap8_len0_to_4", input: make([]uint8, 0, 8), n: 4, wantLen: 4, wantSame: false, wantCapGe: 8},
		{name: "cap8_len8_to_4", input: make([]uint8, 8), n: 4, wantLen: 4, wantSame: true, wantCapGe: 8},
		{name: "cap4_len4_to_8", input: make([]uint8, 4), n: 8, wantLen: 8, wantSame: false, wantCapGe: 8},
		{name: "cap4_len4_to_0", input: make([]uint8, 4), n: 0, wantLen: 0, wantSame: false, wantCapGe: 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resizeTo(tc.input, tc.n)
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tc.wantLen)
			}
			if tc.wantCapGe > 0 && cap(got) < tc.wantCapGe {
				t.Fatalf("cap = %d, want >= %d", cap(got), tc.wantCapGe)
			}
			if tc.wantSame {
				if len(tc.input) == 0 || len(got) == 0 {
					t.Fatal("wantSame requires both slices to have len > 0")
				}
				if &tc.input[0] != &got[0] {
					t.Fatal("expected same backing array")
				}
			}
			if tc.name == "cap8_len0_to_4" {
				// Capacity was reused even though len was 0; verify via cap, not pointer.
				if cap(got) != 8 {
					t.Fatalf("cap = %d, want 8 (reuse)", cap(got))
				}
			}
			if tc.name == "cap4_len4_to_0" {
				if cap(got) != 4 {
					t.Fatalf("cap = %d, want 4 (truncate keeps array)", cap(got))
				}
			}
		})
	}
}

func TestSnapshotCopiesDisplayState(t *testing.T) {
	v := New()
	v.palette[7] = [4]uint8{10, 20, 30, 40}
	v.pages[0].index[0] = 7
	v.pages[0].color[0] = 1
	v.pages[0].color[1] = 2
	v.pages[0].color[2] = 3
	v.pages[0].color[3] = 4
	v.viewportX = 12
	v.viewportY = -8
	v.displayPage = 0

	var f Frame
	v.Snapshot(&f)

	if f.Width != DefaultPageWidth || f.Height != DefaultPageHeight {
		t.Fatalf("size = %dx%d, want %dx%d", f.Width, f.Height, DefaultPageWidth, DefaultPageHeight)
	}
	if f.Page != 0 {
		t.Fatalf("Page = %d, want 0", f.Page)
	}
	if f.ViewX != 12 || f.ViewY != -8 {
		t.Fatalf("viewport = (%d,%d), want (12,-8)", f.ViewX, f.ViewY)
	}
	if f.Palette[7] != [4]uint8{10, 20, 30, 40} {
		t.Fatalf("Palette[7] = %v, want [10 20 30 40]", f.Palette[7])
	}
	if f.Index[0] != 7 {
		t.Fatalf("Index[0] = %d, want 7", f.Index[0])
	}
	if f.Color[0] != 1 || f.Color[1] != 2 || f.Color[2] != 3 || f.Color[3] != 4 {
		t.Fatalf("Color[0:4] = %v, want [1 2 3 4]", f.Color[:4])
	}
	if len(f.Index) != DefaultPageWidth*DefaultPageHeight {
		t.Fatalf("len(Index) = %d, want %d", len(f.Index), DefaultPageWidth*DefaultPageHeight)
	}
	if len(f.Color) != DefaultPageWidth*DefaultPageHeight*4 {
		t.Fatalf("len(Color) = %d, want %d", len(f.Color), DefaultPageWidth*DefaultPageHeight*4)
	}

	// Palette is a value copy: mutating the module must not alter the snapshot.
	v.palette[7] = [4]uint8{99, 99, 99, 99}
	if f.Palette[7] != [4]uint8{10, 20, 30, 40} {
		t.Fatal("Palette was not an independent copy")
	}
}

func TestSnapshotReusesBuffers(t *testing.T) {
	v := New()
	var f Frame
	v.Snapshot(&f)
	if len(f.Index) == 0 || len(f.Color) == 0 {
		t.Fatal("snapshot buffers are empty")
	}
	idxPtr := &f.Index[0]
	colorPtr := &f.Color[0]

	v.Snapshot(&f)
	if &f.Index[0] != idxPtr {
		t.Fatal("Index backing array was reallocated")
	}
	if &f.Color[0] != colorPtr {
		t.Fatal("Color backing array was reallocated")
	}
}

func setPageSize(t *testing.T, v *VRAMModule, page uint8, w, h uint16) {
	t.Helper()
	applyPageSize(v, page, w, h)
}

func TestSnapshotGrowsOnPageEnlarge(t *testing.T) {
	v, _ := newTestVRAM()
	var f Frame
	v.Snapshot(&f)
	if f.Width != DefaultPageWidth {
		t.Fatalf("initial width = %d, want %d", f.Width, DefaultPageWidth)
	}

	setPageSize(t, v, 0, 512, 512)
	v.Snapshot(&f)

	if f.Width != 512 || f.Height != 512 {
		t.Fatalf("size = %dx%d, want 512x512", f.Width, f.Height)
	}
	if len(f.Index) != 512*512 {
		t.Fatalf("len(Index) = %d, want %d", len(f.Index), 512*512)
	}
	if len(f.Color) != 512*512*4 {
		t.Fatalf("len(Color) = %d, want %d", len(f.Color), 512*512*4)
	}
}

func TestSnapshotShrinksOnPageShrink(t *testing.T) {
	v, _ := newTestVRAM()
	var f Frame

	setPageSize(t, v, 0, 512, 512)
	v.Snapshot(&f)
	if len(f.Index) != 512*512 {
		t.Fatalf("after enlarge len(Index) = %d, want %d", len(f.Index), 512*512)
	}

	setPageSize(t, v, 0, 64, 64)
	v.Snapshot(&f)

	if f.Width != 64 || f.Height != 64 {
		t.Fatalf("size = %dx%d, want 64x64", f.Width, f.Height)
	}
	if len(f.Index) != 64*64 {
		t.Fatalf("len(Index) = %d, want %d (must truncate)", len(f.Index), 64*64)
	}
	if len(f.Color) != 64*64*4 {
		t.Fatalf("len(Color) = %d, want %d (must truncate)", len(f.Color), 64*64*4)
	}
}

func applyPageSize(v *VRAMModule, page uint8, w, h uint16) {
	data := make([]byte, 5)
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], w)
	binary.BigEndian.PutUint16(data[3:], h)
	v.handleMessage(&bus.BusMessage{
		Target: "set_page_size", Operation: bus.OpCommand, Data: data,
	})
}

func TestSnapshotConsistencyUnderPageResize(t *testing.T) {
	v, _ := newTestVRAM()
	const iterations = 200
	deadline := time.After(500 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				applyPageSize(v, 0, 256, 212)
			} else {
				applyPageSize(v, 0, 512, 512)
			}
		}
	}()

	var f Frame
	checked := 0
loop:
	for {
		select {
		case <-deadline:
			break loop
		default:
		}
		v.Snapshot(&f)
		if f.Width <= 0 || f.Height <= 0 {
			t.Fatalf("non-positive size %dx%d", f.Width, f.Height)
		}
		need := f.Width * f.Height
		if len(f.Index) < need {
			t.Fatalf("len(Index)=%d < Width*Height=%d", len(f.Index), need)
		}
		if len(f.Color) < need*4 {
			t.Fatalf("len(Color)=%d < Width*Height*4=%d", len(f.Color), need*4)
		}
		checked++
		if checked >= iterations {
			break
		}
	}
	wg.Wait()
	if checked == 0 {
		t.Fatal("no snapshots checked")
	}
}

func TestSnapshotDoesNotBlockWrites(t *testing.T) {
	v, _ := newTestVRAM()
	var f Frame
	v.Snapshot(&f)

	done := make(chan struct{})
	go func() {
		// draw_pixel: [page:u8][x:u16][y:u16][p:u8]
		v.handleMessage(&bus.BusMessage{
			Target:    "draw_pixel",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x2A},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("draw_pixel blocked; Snapshot appears to still hold the lock")
	}

	if v.pages[0].index[0] != 0x2A {
		t.Fatalf("pixel = %d, want 0x2A", v.pages[0].index[0])
	}
}

func BenchmarkSnapshot(b *testing.B) {
	v := New()
	var f Frame
	v.Snapshot(&f) // prime buffers so the loop measures steady-state reuse
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Snapshot(&f)
	}
}
