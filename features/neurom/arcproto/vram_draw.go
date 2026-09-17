package arcproto

import "encoding/binary"

// --- mode ---

// Mode reinitialises page 0. The receiver ignores the payload; the three bytes
// are retained verbatim because they are what the platform has always sent.
type Mode struct{}

func (c Mode) Target() string { return TargetMode }

func (c Mode) Encode() []byte { return []byte{0x00, 0x01, 0x00} }

// --- draw_pixel ---

// DrawPixel writes a single palette index.
//
// Layout: [page:u8][x:u16][y:u16][p:u8]
type DrawPixel struct {
	Page uint8
	X, Y uint16
	P    uint8
}

const drawPixelLen = 6

func (c DrawPixel) Target() string { return TargetDrawPixel }

func (c DrawPixel) Encode() []byte {
	d := make([]byte, drawPixelLen)
	d[0] = c.Page
	binary.BigEndian.PutUint16(d[1:], c.X)
	binary.BigEndian.PutUint16(d[3:], c.Y)
	d[5] = c.P
	return d
}

func DecodeDrawPixel(d []byte) (DrawPixel, error) {
	if len(d) < drawPixelLen {
		return DrawPixel{}, ErrShortPayload
	}
	return DrawPixel{
		Page: d[0],
		X:    binary.BigEndian.Uint16(d[1:]),
		Y:    binary.BigEndian.Uint16(d[3:]),
		P:    d[5],
	}, nil
}

// --- clear_vram ---

// ClearVRAM fills a page with one palette index.
//
// Layout: [page:u8][palette_idx:u8]
//
// Both fields are optional on the wire and default to zero, so this is the one
// command whose decoder never fails.
type ClearVRAM struct {
	Page       uint8
	PaletteIdx uint8
}

func (c ClearVRAM) Target() string { return TargetClearVRAM }

func (c ClearVRAM) Encode() []byte {
	return []byte{c.Page, c.PaletteIdx}
}

// DecodeClearVRAM never returns an error. It reports the error for signature
// symmetry with the other decoders.
func DecodeClearVRAM(d []byte) (ClearVRAM, error) {
	var c ClearVRAM
	if len(d) >= 1 {
		c.Page = d[0]
	}
	if len(d) >= 2 {
		c.PaletteIdx = d[1]
	}
	return c, nil
}

// --- blit_rect ---

// BlitRect copies a rectangle of palette indices onto a page.
//
// Layout: [page:u8][x:u16][y:u16][w:u16][h:u16][blend:u8][pixels...]
type BlitRect struct {
	Page   uint8
	X, Y   uint16
	W, H   uint16
	Blend  BlendMode
	Pixels []uint8
}

const blitRectHeaderLen = 10

func (c BlitRect) Target() string { return TargetBlitRect }

func (c BlitRect) Encode() []byte {
	d := make([]byte, blitRectHeaderLen+len(c.Pixels))
	d[0] = c.Page
	binary.BigEndian.PutUint16(d[1:], c.X)
	binary.BigEndian.PutUint16(d[3:], c.Y)
	binary.BigEndian.PutUint16(d[5:], c.W)
	binary.BigEndian.PutUint16(d[7:], c.H)
	d[9] = uint8(c.Blend)
	copy(d[blitRectHeaderLen:], c.Pixels)
	return d
}

// DecodeBlitRect returns Pixels as a subslice of d.
func DecodeBlitRect(d []byte) (BlitRect, error) {
	if len(d) < blitRectHeaderLen {
		return BlitRect{}, ErrShortPayload
	}
	return BlitRect{
		Page:   d[0],
		X:      binary.BigEndian.Uint16(d[1:]),
		Y:      binary.BigEndian.Uint16(d[3:]),
		W:      binary.BigEndian.Uint16(d[5:]),
		H:      binary.BigEndian.Uint16(d[7:]),
		Blend:  BlendMode(d[9]),
		Pixels: d[blitRectHeaderLen:],
	}, nil
}

// --- blit_rect_transform ---

// BlitRectTransform blits a rectangle with rotation and scaling applied around a
// pivot. X and Y address the pivot, not the top-left corner, and the rendered
// size may exceed SrcW by SrcH.
//
// Layout: [page:u8][x:u16][y:u16][src_w:u16][src_h:u16]
//
//	[pivot_x:u16][pivot_y:u16][rotation:u8][scale_x:u16][scale_y:u16]
//	[blend:u8][pixels...]
type BlitRectTransform struct {
	Page           uint8
	X, Y           uint16
	SrcW, SrcH     uint16
	PivotX, PivotY uint16
	Rotation       Rotation
	ScaleX, ScaleY Scale
	Blend          BlendMode
	Pixels         []uint8
}

const blitRectTransformHeaderLen = 19

func (c BlitRectTransform) Target() string { return TargetBlitRectTransform }

func (c BlitRectTransform) Encode() []byte {
	d := make([]byte, blitRectTransformHeaderLen+len(c.Pixels))
	d[0] = c.Page
	binary.BigEndian.PutUint16(d[1:], c.X)
	binary.BigEndian.PutUint16(d[3:], c.Y)
	binary.BigEndian.PutUint16(d[5:], c.SrcW)
	binary.BigEndian.PutUint16(d[7:], c.SrcH)
	binary.BigEndian.PutUint16(d[9:], c.PivotX)
	binary.BigEndian.PutUint16(d[11:], c.PivotY)
	d[13] = uint8(c.Rotation)
	binary.BigEndian.PutUint16(d[14:], uint16(c.ScaleX))
	binary.BigEndian.PutUint16(d[16:], uint16(c.ScaleY))
	d[18] = uint8(c.Blend)
	copy(d[blitRectTransformHeaderLen:], c.Pixels)
	return d
}

// DecodeBlitRectTransform returns Pixels as a subslice of d.
func DecodeBlitRectTransform(d []byte) (BlitRectTransform, error) {
	if len(d) < blitRectTransformHeaderLen {
		return BlitRectTransform{}, ErrShortPayload
	}
	return BlitRectTransform{
		Page:     d[0],
		X:        binary.BigEndian.Uint16(d[1:]),
		Y:        binary.BigEndian.Uint16(d[3:]),
		SrcW:     binary.BigEndian.Uint16(d[5:]),
		SrcH:     binary.BigEndian.Uint16(d[7:]),
		PivotX:   binary.BigEndian.Uint16(d[9:]),
		PivotY:   binary.BigEndian.Uint16(d[11:]),
		Rotation: Rotation(d[13]),
		ScaleX:   Scale(binary.BigEndian.Uint16(d[14:])),
		ScaleY:   Scale(binary.BigEndian.Uint16(d[16:])),
		Blend:    BlendMode(d[18]),
		Pixels:   d[blitRectTransformHeaderLen:],
	}, nil
}

// --- copy_rect ---

// CopyRect copies a rectangle between pages, or within one page. Overlapping
// source and destination regions are safe because the receiver stages the pixels
// before writing them back.
//
// Layout: [src_page:u8][dst_page:u8][src_x:u16][src_y:u16]
//
//	[dst_x:u16][dst_y:u16][w:u16][h:u16]
type CopyRect struct {
	SrcPage, DstPage uint8
	SrcX, SrcY       uint16
	DstX, DstY       uint16
	W, H             uint16
}

const copyRectLen = 14

func (c CopyRect) Target() string { return TargetCopyRect }

func (c CopyRect) Encode() []byte {
	d := make([]byte, copyRectLen)
	d[0] = c.SrcPage
	d[1] = c.DstPage
	binary.BigEndian.PutUint16(d[2:], c.SrcX)
	binary.BigEndian.PutUint16(d[4:], c.SrcY)
	binary.BigEndian.PutUint16(d[6:], c.DstX)
	binary.BigEndian.PutUint16(d[8:], c.DstY)
	binary.BigEndian.PutUint16(d[10:], c.W)
	binary.BigEndian.PutUint16(d[12:], c.H)
	return d
}

func DecodeCopyRect(d []byte) (CopyRect, error) {
	if len(d) < copyRectLen {
		return CopyRect{}, ErrShortPayload
	}
	return CopyRect{
		SrcPage: d[0],
		DstPage: d[1],
		SrcX:    binary.BigEndian.Uint16(d[2:]),
		SrcY:    binary.BigEndian.Uint16(d[4:]),
		DstX:    binary.BigEndian.Uint16(d[6:]),
		DstY:    binary.BigEndian.Uint16(d[8:]),
		W:       binary.BigEndian.Uint16(d[10:]),
		H:       binary.BigEndian.Uint16(d[12:]),
	}, nil
}
