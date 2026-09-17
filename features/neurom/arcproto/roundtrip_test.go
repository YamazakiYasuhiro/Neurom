package arcproto

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTripDrawPixel(t *testing.T) {
	tests := []struct {
		name string
		in   DrawPixel
	}{
		{"zero", DrawPixel{}},
		{"typical", DrawPixel{Page: 1, X: 10, Y: 20, P: 7}},
		{"max", DrawPixel{Page: 255, X: 65535, Y: 65535, P: 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeDrawPixel(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeDrawPixel() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripClearVRAM(t *testing.T) {
	tests := []struct {
		name string
		in   ClearVRAM
	}{
		{"zero", ClearVRAM{}},
		{"typical", ClearVRAM{Page: 0, PaletteIdx: 3}},
		{"max", ClearVRAM{Page: 255, PaletteIdx: 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeClearVRAM(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeClearVRAM() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

// TestDecodeClearVRAMLenient locks the lenient parsing of the current VRAM
// implementation: missing fields default to zero instead of erroring.
func TestDecodeClearVRAMLenient(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want ClearVRAM
	}{
		{"empty", []byte{}, ClearVRAM{}},
		{"nil", nil, ClearVRAM{}},
		{"page_only", []byte{5}, ClearVRAM{Page: 5}},
		{"full", []byte{5, 9}, ClearVRAM{Page: 5, PaletteIdx: 9}},
		{"extra_ignored", []byte{5, 9, 77}, ClearVRAM{Page: 5, PaletteIdx: 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeClearVRAM(tt.data)
			if err != nil {
				t.Fatalf("DecodeClearVRAM() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRoundTripBlitRect(t *testing.T) {
	tests := []struct {
		name string
		in   BlitRect
	}{
		{"minimal", BlitRect{W: 1, H: 1, Pixels: []uint8{7}}},
		{"typical", BlitRect{Page: 1, X: 10, Y: 20, W: 4, H: 4, Blend: BlendAlpha, Pixels: make([]uint8, 16)}},
		{"max_coords", BlitRect{Page: 255, X: 65535, Y: 65535, W: 2, H: 2, Blend: BlendScreen, Pixels: []uint8{1, 2, 3, 4}}},
		{"large_512", BlitRect{W: 512, H: 512, Pixels: make([]uint8, 512*512)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeBlitRect(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeBlitRect() error = %v", err)
			}
			if got.Page != tt.in.Page || got.X != tt.in.X || got.Y != tt.in.Y ||
				got.W != tt.in.W || got.H != tt.in.H || got.Blend != tt.in.Blend {
				t.Errorf("header mismatch: got %+v, want %+v", got, tt.in)
			}
			if !bytes.Equal(got.Pixels, tt.in.Pixels) {
				t.Errorf("pixels mismatch: got %d bytes, want %d bytes", len(got.Pixels), len(tt.in.Pixels))
			}
		})
	}
}

func TestRoundTripBlitRectTransform(t *testing.T) {
	tests := []struct {
		name string
		in   BlitRectTransform
	}{
		{"minimal", BlitRectTransform{SrcW: 1, SrcH: 1, ScaleX: ScaleOne, ScaleY: ScaleOne, Pixels: []uint8{1}}},
		{
			"typical",
			BlitRectTransform{
				Page: 0, X: 128, Y: 106, SrcW: 8, SrcH: 8,
				PivotX: 4, PivotY: 4, Rotation: 64,
				ScaleX: ScaleOne, ScaleY: ScaleOne, Blend: BlendReplace,
				Pixels: make([]uint8, 64),
			},
		},
		{
			"scaled_rotated",
			BlitRectTransform{
				Page: 2, X: 1, Y: 2, SrcW: 16, SrcH: 16,
				PivotX: 8, PivotY: 15, Rotation: 255,
				ScaleX: ScaleOf(2.0), ScaleY: ScaleOf(0.5), Blend: BlendAdditive,
				Pixels: make([]uint8, 256),
			},
		},
		{
			"max_fields",
			BlitRectTransform{
				Page: 255, X: 65535, Y: 65535, SrcW: 2, SrcH: 2,
				PivotX: 65535, PivotY: 65535, Rotation: 255,
				ScaleX: 65535, ScaleY: 65535, Blend: BlendScreen,
				Pixels: []uint8{1, 2, 3, 4},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeBlitRectTransform(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeBlitRectTransform() error = %v", err)
			}
			if got.Page != tt.in.Page || got.X != tt.in.X || got.Y != tt.in.Y ||
				got.SrcW != tt.in.SrcW || got.SrcH != tt.in.SrcH ||
				got.PivotX != tt.in.PivotX || got.PivotY != tt.in.PivotY ||
				got.Rotation != tt.in.Rotation ||
				got.ScaleX != tt.in.ScaleX || got.ScaleY != tt.in.ScaleY ||
				got.Blend != tt.in.Blend {
				t.Errorf("header mismatch:\ngot  %+v\nwant %+v", got, tt.in)
			}
			if !bytes.Equal(got.Pixels, tt.in.Pixels) {
				t.Errorf("pixels mismatch: got %d bytes, want %d bytes", len(got.Pixels), len(tt.in.Pixels))
			}
		})
	}
}

func TestRoundTripCopyRect(t *testing.T) {
	tests := []struct {
		name string
		in   CopyRect
	}{
		{"zero", CopyRect{}},
		{"scroll_up", CopyRect{SrcPage: 0, DstPage: 0, SrcX: 0, SrcY: 1, DstX: 0, DstY: 0, W: 256, H: 211}},
		{"cross_page", CopyRect{SrcPage: 1, DstPage: 2, SrcX: 10, SrcY: 20, DstX: 30, DstY: 40, W: 50, H: 60}},
		{"max", CopyRect{SrcPage: 255, DstPage: 255, SrcX: 65535, SrcY: 65535, DstX: 65535, DstY: 65535, W: 65535, H: 65535}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeCopyRect(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeCopyRect() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripSetPalette(t *testing.T) {
	tests := []struct {
		name string
		in   SetPalette
	}{
		{"with_alpha", SetPalette{Index: 1, Color: Color{R: 10, G: 20, B: 30, A: 40}}},
		{"opaque", SetPalette{Index: 255, Color: Color{R: 255, G: 255, B: 255, A: 255}}},
		{"omit_alpha", SetPalette{Index: 2, Color: Color{R: 1, G: 2, B: 3, A: 255}, OmitAlpha: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSetPalette(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeSetPalette() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

// TestDecodeSetPaletteAlphaDefault locks that a 4-byte payload yields alpha 255.
func TestDecodeSetPaletteAlphaDefault(t *testing.T) {
	got, err := DecodeSetPalette([]byte{7, 1, 2, 3})
	if err != nil {
		t.Fatalf("DecodeSetPalette() error = %v", err)
	}
	want := SetPalette{Index: 7, Color: Color{R: 1, G: 2, B: 3, A: 255}, OmitAlpha: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestRoundTripSetPaletteBlock(t *testing.T) {
	tests := []struct {
		name string
		in   SetPaletteBlock
	}{
		{"empty", SetPaletteBlock{Start: 0, Colors: []Color{}}},
		{"three", SetPaletteBlock{Start: 0, Colors: []Color{
			{R: 20, G: 20, B: 40, A: 255},
			{R: 255, G: 100, B: 50, A: 255},
			{R: 50, G: 200, B: 255, A: 255},
		}}},
		{"full_128", SetPaletteBlock{Start: 128, Colors: make([]Color, 128)}},
		{"max_start", SetPaletteBlock{Start: 255, Colors: []Color{{R: 1, G: 2, B: 3, A: 4}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSetPaletteBlock(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeSetPaletteBlock() error = %v", err)
			}
			if got.Start != tt.in.Start {
				t.Errorf("Start = %d, want %d", got.Start, tt.in.Start)
			}
			if len(got.Colors) != len(tt.in.Colors) {
				t.Fatalf("len(Colors) = %d, want %d", len(got.Colors), len(tt.in.Colors))
			}
			for i := range got.Colors {
				if got.Colors[i] != tt.in.Colors[i] {
					t.Errorf("Colors[%d] = %+v, want %+v", i, got.Colors[i], tt.in.Colors[i])
				}
			}
		})
	}
}

func TestRoundTripReadPaletteBlock(t *testing.T) {
	tests := []struct {
		name string
		in   ReadPaletteBlock
	}{
		{"zero", ReadPaletteBlock{}},
		{"typical", ReadPaletteBlock{Start: 0, Count: 8}},
		{"max", ReadPaletteBlock{Start: 255, Count: 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeReadPaletteBlock(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeReadPaletteBlock() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripReadRect(t *testing.T) {
	tests := []struct {
		name string
		in   ReadRect
	}{
		{"zero", ReadRect{}},
		{"typical", ReadRect{Page: 0, X: 0, Y: 0, W: 2, H: 2}},
		{"max", ReadRect{Page: 255, X: 65535, Y: 65535, W: 65535, H: 65535}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeReadRect(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeReadRect() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripSetPageCount(t *testing.T) {
	tests := []struct {
		name         string
		in           SetPageCount
		wantResolved int
	}{
		{"zero_means_256", SetPageCount{Count: 0}, 256},
		{"one", SetPageCount{Count: 1}, 1},
		{"four", SetPageCount{Count: 4}, 4},
		{"max", SetPageCount{Count: 255}, 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSetPageCount(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeSetPageCount() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
			if n := got.ResolvedCount(); n != tt.wantResolved {
				t.Errorf("ResolvedCount() = %d, want %d", n, tt.wantResolved)
			}
		})
	}
}

func TestRoundTripSetDisplayPage(t *testing.T) {
	for _, page := range []uint8{0, 1, 128, 255} {
		in := SetDisplayPage{Page: page}
		got, err := DecodeSetDisplayPage(in.Encode())
		if err != nil {
			t.Fatalf("DecodeSetDisplayPage() error = %v", err)
		}
		if got != in {
			t.Errorf("got %+v, want %+v", got, in)
		}
	}
}

func TestRoundTripSwapPages(t *testing.T) {
	tests := []struct {
		name string
		in   SwapPages
	}{
		{"zero", SwapPages{}},
		{"typical", SwapPages{Page1: 0, Page2: 1}},
		{"max", SwapPages{Page1: 255, Page2: 254}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSwapPages(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeSwapPages() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripCopyPage(t *testing.T) {
	tests := []struct {
		name string
		in   CopyPage
	}{
		{"zero", CopyPage{}},
		{"typical", CopyPage{Src: 0, Dst: 1}},
		{"max", CopyPage{Src: 255, Dst: 254}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeCopyPage(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeCopyPage() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripSetPageSize(t *testing.T) {
	tests := []struct {
		name string
		in   SetPageSize
	}{
		{"zero", SetPageSize{}},
		{"default", SetPageSize{Page: 0, W: 256, H: 212}},
		{"large", SetPageSize{Page: 1, W: 512, H: 512}},
		{"max", SetPageSize{Page: 255, W: 65535, H: 65535}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSetPageSize(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeSetPageSize() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

// TestRoundTripSetViewport covers the only command carrying signed integers.
func TestRoundTripSetViewport(t *testing.T) {
	tests := []struct {
		name string
		in   SetViewport
	}{
		{"zero", SetViewport{}},
		{"positive", SetViewport{OffX: 10, OffY: 20}},
		{"negative_one", SetViewport{OffX: -1, OffY: -1}},
		{"min", SetViewport{OffX: -32768, OffY: -32768}},
		{"max", SetViewport{OffX: 32767, OffY: 32767}},
		{"mixed", SetViewport{OffX: -100, OffY: 200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSetViewport(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeSetViewport() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripRectData(t *testing.T) {
	tests := []struct {
		name string
		in   RectData
	}{
		{"minimal", RectData{W: 1, H: 1, Pixels: []uint8{9}}},
		{"typical", RectData{X: 0, Y: 0, W: 2, H: 2, Pixels: []uint8{1, 2, 3, 4}}},
		{"max_coords", RectData{X: 65535, Y: 65535, W: 1, H: 1, Pixels: []uint8{0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeRectData(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeRectData() error = %v", err)
			}
			if got.X != tt.in.X || got.Y != tt.in.Y || got.W != tt.in.W || got.H != tt.in.H {
				t.Errorf("header mismatch: got %+v, want %+v", got, tt.in)
			}
			if !bytes.Equal(got.Pixels, tt.in.Pixels) {
				t.Errorf("pixels = % X, want % X", got.Pixels, tt.in.Pixels)
			}
		})
	}
}

func TestRoundTripRectUpdated(t *testing.T) {
	tests := []struct {
		name string
		in   RectUpdated
	}{
		{"zero", RectUpdated{}},
		{"typical", RectUpdated{X: 10, Y: 20, W: 30, H: 40}},
		{"max", RectUpdated{X: 65535, Y: 65535, W: 65535, H: 65535}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeRectUpdated(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeRectUpdated() error = %v", err)
			}
			if got != tt.in {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestRoundTripPageError(t *testing.T) {
	for _, code := range []uint8{PageErrInvalidPage, PageErrInvalidDisplay, 0xFF} {
		in := PageError{Code: code}
		got, err := DecodePageError(in.Encode())
		if err != nil {
			t.Fatalf("DecodePageError() error = %v", err)
		}
		if got != in {
			t.Errorf("got %+v, want %+v", got, in)
		}
	}
}

// TestDecodeShortPayload verifies every decoder rejects truncated input with
// ErrShortPayload instead of panicking. The VRAM module relies on this to
// silently drop malformed commands.
func TestDecodeShortPayload(t *testing.T) {
	tests := []struct {
		name string
		fn   func([]byte) error
		size int
	}{
		{"draw_pixel", func(d []byte) error { _, err := DecodeDrawPixel(d); return err }, drawPixelLen},
		{"blit_rect", func(d []byte) error { _, err := DecodeBlitRect(d); return err }, blitRectHeaderLen},
		{"blit_rect_transform", func(d []byte) error { _, err := DecodeBlitRectTransform(d); return err }, blitRectTransformHeaderLen},
		{"copy_rect", func(d []byte) error { _, err := DecodeCopyRect(d); return err }, copyRectLen},
		{"set_palette", func(d []byte) error { _, err := DecodeSetPalette(d); return err }, 4},
		{"set_palette_block", func(d []byte) error { _, err := DecodeSetPaletteBlock(d); return err }, paletteBlockHeaderLen},
		{"read_palette_block", func(d []byte) error { _, err := DecodeReadPaletteBlock(d); return err }, 2},
		{"read_rect", func(d []byte) error { _, err := DecodeReadRect(d); return err }, readRectLen},
		{"set_page_count", func(d []byte) error { _, err := DecodeSetPageCount(d); return err }, 1},
		{"set_display_page", func(d []byte) error { _, err := DecodeSetDisplayPage(d); return err }, 1},
		{"swap_pages", func(d []byte) error { _, err := DecodeSwapPages(d); return err }, 2},
		{"copy_page", func(d []byte) error { _, err := DecodeCopyPage(d); return err }, 2},
		{"set_page_size", func(d []byte) error { _, err := DecodeSetPageSize(d); return err }, setPageSizeLen},
		{"set_viewport", func(d []byte) error { _, err := DecodeSetViewport(d); return err }, setViewportLen},
		{"rect_data", func(d []byte) error { _, err := DecodeRectData(d); return err }, rectDataHeaderLen},
		{"rect_updated", func(d []byte) error { _, err := DecodeRectUpdated(d); return err }, rectUpdatedLen},
		{"page_error", func(d []byte) error { _, err := DecodePageError(d); return err }, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Every length shorter than the minimum must be rejected.
			for n := 0; n < tt.size; n++ {
				if err := tt.fn(make([]byte, n)); !errors.Is(err, ErrShortPayload) {
					t.Errorf("len=%d: error = %v, want ErrShortPayload", n, err)
				}
			}
			// The minimum length must be accepted.
			if err := tt.fn(make([]byte, tt.size)); err != nil {
				t.Errorf("len=%d: error = %v, want nil", tt.size, err)
			}
		})
	}
}

// TestDecodeSetPaletteBlockTruncatedColors covers the second length check:
// the header declares a colour count larger than the payload provides.
func TestDecodeSetPaletteBlockTruncatedColors(t *testing.T) {
	// Declares 3 colours (12 bytes) but only supplies 4 bytes of colour data.
	data := []byte{0, 3, 1, 2, 3, 4}
	if _, err := DecodeSetPaletteBlock(data); !errors.Is(err, ErrShortPayload) {
		t.Errorf("error = %v, want ErrShortPayload", err)
	}
}

// TestPixelsAreSubslice verifies decoders return a subslice of the input
// rather than a copy. The VRAM module depends on this to avoid allocations.
func TestPixelsAreSubslice(t *testing.T) {
	in := BlitRect{W: 2, H: 2, Pixels: []uint8{1, 2, 3, 4}}
	data := in.Encode()
	got, err := DecodeBlitRect(data)
	if err != nil {
		t.Fatalf("DecodeBlitRect() error = %v", err)
	}
	// Mutating the source buffer must be visible through the decoded pixels.
	data[blitRectHeaderLen] = 99
	if got.Pixels[0] != 99 {
		t.Errorf("Pixels is a copy, want a subslice of the input buffer")
	}
}

// TestTargets verifies every command and event type reports the Target string
// that the VRAM module dispatches on.
func TestTargets(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{Mode{}.Target(), TargetMode},
		{DrawPixel{}.Target(), TargetDrawPixel},
		{SetPalette{}.Target(), TargetSetPalette},
		{SetPaletteBlock{}.Target(), TargetSetPaletteBlock},
		{ReadPaletteBlock{}.Target(), TargetReadPaletteBlock},
		{ClearVRAM{}.Target(), TargetClearVRAM},
		{BlitRect{}.Target(), TargetBlitRect},
		{BlitRectTransform{}.Target(), TargetBlitRectTransform},
		{ReadRect{}.Target(), TargetReadRect},
		{CopyRect{}.Target(), TargetCopyRect},
		{SetPageCount{}.Target(), TargetSetPageCount},
		{SetDisplayPage{}.Target(), TargetSetDisplayPage},
		{SwapPages{}.Target(), TargetSwapPages},
		{CopyPage{}.Target(), TargetCopyPage},
		{SetPageSize{}.Target(), TargetSetPageSize},
		{SetViewport{}.Target(), TargetSetViewport},
		{GetStats{}.Target(), TargetGetStats},
		{RectData{}.Target(), EventRectData},
		{RectUpdated{}.Target(), EventRectUpdated},
		{PageError{}.Target(), EventPageError},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Target() = %q, want %q", tt.got, tt.want)
		}
	}
}
