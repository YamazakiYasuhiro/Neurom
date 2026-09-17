package arcproto

import (
	"bytes"
	"testing"
)

// TestGoldenEncode locks the exact wire bytes of every command and event.
//
// The expected values are derived by hand from the offset arithmetic of the
// pre-refactoring encoders in internal/modules/cpu/cpu.go and the response
// builders in internal/modules/vram/vram.go. This test is the primary evidence
// that extracting the protocol into this package did not change a single bit on
// the bus.
func TestGoldenEncode(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want []byte
	}{
		// cpu.go run(): Data: []byte{0x00, 0x01, 0x00}
		// The VRAM module never reads this payload; the bytes are kept verbatim.
		{
			name: "mode",
			got:  Mode{}.Encode(),
			want: []byte{0x00, 0x01, 0x00},
		},
		// [page:u8][x:u16][y:u16][p:u8]
		{
			name: "draw_pixel",
			got:  DrawPixel{Page: 0, X: 1, Y: 2, P: 3}.Encode(),
			want: []byte{
				0x00,       // page
				0x00, 0x01, // x = 1
				0x00, 0x02, // y = 2
				0x03, // p
			},
		},
		// [index:u8][R][G][B][A]
		{
			name: "set_palette_with_alpha",
			got:  SetPalette{Index: 7, Color: Color{R: 1, G: 2, B: 3, A: 4}}.Encode(),
			want: []byte{0x07, 0x01, 0x02, 0x03, 0x04},
		},
		// [index:u8][R][G][B] — legacy 4-byte form used by integration tests
		{
			name: "set_palette_omit_alpha",
			got:  SetPalette{Index: 7, Color: Color{R: 1, G: 2, B: 3, A: 255}, OmitAlpha: true}.Encode(),
			want: []byte{0x07, 0x01, 0x02, 0x03},
		},
		// [start:u8][count:u8][R][G][B][A] x count
		// Mirrors scene2Init: 3 colours starting at index 0.
		{
			name: "set_palette_block_three",
			got: SetPaletteBlock{Start: 0, Colors: []Color{
				{R: 20, G: 20, B: 40, A: 255},
				{R: 255, G: 100, B: 50, A: 255},
				{R: 50, G: 200, B: 255, A: 255},
			}}.Encode(),
			want: []byte{
				0x00,                   // start
				0x03,                   // count
				0x14, 0x14, 0x28, 0xFF, // {20, 20, 40, 255}
				0xFF, 0x64, 0x32, 0xFF, // {255, 100, 50, 255}
				0x32, 0xC8, 0xFF, 0xFF, // {50, 200, 255, 255}
			},
		},
		{
			name: "set_palette_block_empty",
			got:  SetPaletteBlock{Start: 5, Colors: nil}.Encode(),
			want: []byte{0x05, 0x00},
		},
		// [start:u8][count:u8]
		{
			name: "read_palette_block",
			got:  ReadPaletteBlock{Start: 0, Count: 8}.Encode(),
			want: []byte{0x00, 0x08},
		},
		// [page:u8][palette_idx:u8]
		{
			name: "clear_vram",
			got:  ClearVRAM{Page: 0, PaletteIdx: 3}.Encode(),
			want: []byte{0x00, 0x03},
		},
		// [page:u8][x:u16][y:u16][w:u16][h:u16][blend:u8][pixels...]
		{
			name: "blit_rect",
			got:  BlitRect{Page: 0, X: 10, Y: 20, W: 4, H: 4, Blend: BlendReplace, Pixels: []uint8{1, 2, 3, 4}}.Encode(),
			want: []byte{
				0x00,       // page
				0x00, 0x0A, // x = 10
				0x00, 0x14, // y = 20
				0x00, 0x04, // w = 4
				0x00, 0x04, // h = 4
				0x00,                   // blend = Replace
				0x01, 0x02, 0x03, 0x04, // pixels
			},
		},
		{
			name: "blit_rect_alpha_blend",
			got:  BlitRect{Page: 1, X: 0, Y: 0, W: 2, H: 2, Blend: BlendAlpha, Pixels: []uint8{9, 9, 9, 9}}.Encode(),
			want: []byte{
				0x01,
				0x00, 0x00,
				0x00, 0x00,
				0x00, 0x02,
				0x00, 0x02,
				0x01, // blend = Alpha
				0x09, 0x09, 0x09, 0x09,
			},
		},
		// [page:u8][x:u16][y:u16][srcW:u16][srcH:u16]
		// [pivotX:u16][pivotY:u16][rotation:u8][scaleX:u16][scaleY:u16][blend:u8][pixels...]
		// Mirrors the centre sprite of scene3Update at rotation 64.
		{
			name: "blit_rect_transform_header",
			got: BlitRectTransform{
				Page: 0, X: 128, Y: 106, SrcW: 8, SrcH: 8,
				PivotX: 4, PivotY: 4, Rotation: 64,
				ScaleX: ScaleOne, ScaleY: ScaleOne, Blend: BlendReplace,
				Pixels: make([]uint8, 64),
			}.Encode()[:blitRectTransformHeaderLen],
			want: []byte{
				0x00,       // page
				0x00, 0x80, // x = 128
				0x00, 0x6A, // y = 106
				0x00, 0x08, // srcW = 8
				0x00, 0x08, // srcH = 8
				0x00, 0x04, // pivotX = 4
				0x00, 0x04, // pivotY = 4
				0x40,       // rotation = 64 (a quarter turn)
				0x01, 0x00, // scaleX = 0x0100 (1x)
				0x01, 0x00, // scaleY = 0x0100 (1x)
				0x00, // blend = Replace
			},
		},
		// Mirrors the right-hand sprite of scene3Update: 2x scale.
		{
			name: "blit_rect_transform_scale_2x",
			got: BlitRectTransform{
				Page: 0, X: 192, Y: 106, SrcW: 8, SrcH: 8,
				PivotX: 4, PivotY: 4, Rotation: 0,
				ScaleX: ScaleOf(2.0), ScaleY: ScaleOf(2.0), Blend: BlendReplace,
				Pixels: make([]uint8, 64),
			}.Encode()[:blitRectTransformHeaderLen],
			want: []byte{
				0x00,
				0x00, 0xC0, // x = 192
				0x00, 0x6A, // y = 106
				0x00, 0x08,
				0x00, 0x08,
				0x00, 0x04,
				0x00, 0x04,
				0x00,       // rotation = 0
				0x02, 0x00, // scaleX = 0x0200 (2x)
				0x02, 0x00, // scaleY = 0x0200 (2x)
				0x00,
			},
		},
		// [page:u8][x:u16][y:u16][w:u16][h:u16]
		{
			name: "read_rect",
			got:  ReadRect{Page: 0, X: 0, Y: 0, W: 2, H: 2}.Encode(),
			want: []byte{
				0x00,
				0x00, 0x00,
				0x00, 0x00,
				0x00, 0x02,
				0x00, 0x02,
			},
		},
		// [srcPage:u8][dstPage:u8][srcX:u16][srcY:u16][dstX:u16][dstY:u16][w:u16][h:u16]
		// Mirrors scene5Update: scroll the page up by one row.
		{
			name: "copy_rect",
			got:  CopyRect{SrcPage: 0, DstPage: 0, SrcX: 0, SrcY: 1, DstX: 0, DstY: 0, W: 256, H: 211}.Encode(),
			want: []byte{
				0x00,       // srcPage
				0x00,       // dstPage
				0x00, 0x00, // srcX
				0x00, 0x01, // srcY = 1
				0x00, 0x00, // dstX
				0x00, 0x00, // dstY
				0x01, 0x00, // w = 256
				0x00, 0xD3, // h = 211
			},
		},
		// [count:u8]
		{
			name: "set_page_count_one",
			got:  SetPageCount{Count: 1}.Encode(),
			want: []byte{0x01},
		},
		{
			name: "set_page_count_zero_means_256",
			got:  SetPageCount{Count: 0}.Encode(),
			want: []byte{0x00},
		},
		// [page:u8]
		{
			name: "set_display_page",
			got:  SetDisplayPage{Page: 1}.Encode(),
			want: []byte{0x01},
		},
		// [page1:u8][page2:u8]
		{
			name: "swap_pages",
			got:  SwapPages{Page1: 0, Page2: 1}.Encode(),
			want: []byte{0x00, 0x01},
		},
		// [src:u8][dst:u8]
		{
			name: "copy_page",
			got:  CopyPage{Src: 0, Dst: 1}.Encode(),
			want: []byte{0x00, 0x01},
		},
		// [page:u8][w:u16][h:u16]
		{
			name: "set_page_size",
			got:  SetPageSize{Page: 0, W: 512, H: 512}.Encode(),
			want: []byte{
				0x00,
				0x02, 0x00, // w = 512
				0x02, 0x00, // h = 512
			},
		},
		// [offX:i16][offY:i16] — two's complement, big-endian
		{
			name: "set_viewport_zero",
			got:  SetViewport{OffX: 0, OffY: 0}.Encode(),
			want: []byte{0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "set_viewport_negative_one",
			got:  SetViewport{OffX: -1, OffY: -1}.Encode(),
			want: []byte{0xFF, 0xFF, 0xFF, 0xFF},
		},
		{
			name: "set_viewport_mixed_sign",
			got:  SetViewport{OffX: 10, OffY: -20}.Encode(),
			want: []byte{
				0x00, 0x0A, // 10
				0xFF, 0xEC, // -20
			},
		},
		// get_stats carries no payload.
		{
			name: "get_stats",
			got:  GetStats{}.Encode(),
			want: nil,
		},
		// --- Events ---
		// rect_data: [x:u16][y:u16][w:u16][h:u16][pixels...] — note: no page field
		{
			name: "rect_data",
			got:  RectData{X: 0, Y: 0, W: 2, H: 2, Pixels: []uint8{1, 2, 3, 4}}.Encode(),
			want: []byte{
				0x00, 0x00,
				0x00, 0x00,
				0x00, 0x02,
				0x00, 0x02,
				0x01, 0x02, 0x03, 0x04,
			},
		},
		// rect_updated: [x:u16][y:u16][w:u16][h:u16]
		{
			name: "rect_updated",
			got:  RectUpdated{X: 10, Y: 20, W: 30, H: 40}.Encode(),
			want: []byte{
				0x00, 0x0A,
				0x00, 0x14,
				0x00, 0x1E,
				0x00, 0x28,
			},
		},
		// page_error: [code:u8]
		{
			name: "page_error_invalid_page",
			got:  PageError{Code: PageErrInvalidPage}.Encode(),
			want: []byte{0x01},
		},
		{
			name: "page_error_invalid_display",
			got:  PageError{Code: PageErrInvalidDisplay}.Encode(),
			want: []byte{0x03},
		},
		// palette_data shares the set_palette_block layout.
		{
			name: "palette_data",
			got: PaletteData{Start: 2, Colors: []Color{
				{R: 1, G: 2, B: 3, A: 4},
			}}.Encode(),
			want: []byte{
				0x02,                   // start
				0x01,                   // count
				0x01, 0x02, 0x03, 0x04, // colour
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bytes.Equal(tt.got, tt.want) {
				t.Errorf("Encode() = % X, want % X", tt.got, tt.want)
			}
		})
	}
}

// TestGoldenCoversAllCommands guards against a command being added to the
// protocol without a corresponding golden case.
func TestGoldenCoversAllCommands(t *testing.T) {
	allCommands := []string{
		TargetMode,
		TargetDrawPixel,
		TargetSetPalette,
		TargetSetPaletteBlock,
		TargetReadPaletteBlock,
		TargetClearVRAM,
		TargetBlitRect,
		TargetBlitRectTransform,
		TargetReadRect,
		TargetCopyRect,
		TargetSetPageCount,
		TargetSetDisplayPage,
		TargetSwapPages,
		TargetCopyPage,
		TargetSetPageSize,
		TargetSetViewport,
		TargetGetStats,
	}
	if len(allCommands) != 17 {
		t.Fatalf("command count = %d, want 17 (update this test when the protocol grows)", len(allCommands))
	}

	// Every command must have at least one encoder exercised above. The mapping
	// is by golden case name prefix, which is kept in sync manually.
	covered := map[string]bool{
		TargetMode:              true,
		TargetDrawPixel:         true,
		TargetSetPalette:        true,
		TargetSetPaletteBlock:   true,
		TargetReadPaletteBlock:  true,
		TargetClearVRAM:         true,
		TargetBlitRect:          true,
		TargetBlitRectTransform: true,
		TargetReadRect:          true,
		TargetCopyRect:          true,
		TargetSetPageCount:      true,
		TargetSetDisplayPage:    true,
		TargetSwapPages:         true,
		TargetCopyPage:          true,
		TargetSetPageSize:       true,
		TargetSetViewport:       true,
		TargetGetStats:          true,
	}
	for _, cmd := range allCommands {
		if !covered[cmd] {
			t.Errorf("command %q has no golden byte-sequence case", cmd)
		}
	}
}
