package arcproto

import "encoding/binary"

// --- rect_data ---

// RectData answers a ReadRect request.
//
// Layout: [x:u16][y:u16][w:u16][h:u16][pixels...]
//
// The page field of the request is not echoed, so a requester issuing reads
// against several pages must serialise them or correlate the replies itself.
type RectData struct {
	X, Y   uint16
	W, H   uint16
	Pixels []uint8
}

const rectDataHeaderLen = 8

func (e RectData) Target() string { return EventRectData }

func (e RectData) Encode() []byte {
	d := make([]byte, rectDataHeaderLen+len(e.Pixels))
	binary.BigEndian.PutUint16(d[0:], e.X)
	binary.BigEndian.PutUint16(d[2:], e.Y)
	binary.BigEndian.PutUint16(d[4:], e.W)
	binary.BigEndian.PutUint16(d[6:], e.H)
	copy(d[rectDataHeaderLen:], e.Pixels)
	return d
}

// DecodeRectData returns Pixels as a subslice of d.
func DecodeRectData(d []byte) (RectData, error) {
	if len(d) < rectDataHeaderLen {
		return RectData{}, ErrShortPayload
	}
	return RectData{
		X:      binary.BigEndian.Uint16(d[0:]),
		Y:      binary.BigEndian.Uint16(d[2:]),
		W:      binary.BigEndian.Uint16(d[4:]),
		H:      binary.BigEndian.Uint16(d[6:]),
		Pixels: d[rectDataHeaderLen:],
	}, nil
}

// --- rect_updated ---

// RectUpdated reports the region a drawing command touched so the monitor can
// repaint just that area. For a transformed blit the region is the rendered
// bounding box, which is larger than the source rectangle.
//
// Layout: [x:u16][y:u16][w:u16][h:u16]
type RectUpdated struct {
	X, Y uint16
	W, H uint16
}

const rectUpdatedLen = 8

func (e RectUpdated) Target() string { return EventRectUpdated }

func (e RectUpdated) Encode() []byte {
	d := make([]byte, rectUpdatedLen)
	binary.BigEndian.PutUint16(d[0:], e.X)
	binary.BigEndian.PutUint16(d[2:], e.Y)
	binary.BigEndian.PutUint16(d[4:], e.W)
	binary.BigEndian.PutUint16(d[6:], e.H)
	return d
}

func DecodeRectUpdated(d []byte) (RectUpdated, error) {
	if len(d) < rectUpdatedLen {
		return RectUpdated{}, ErrShortPayload
	}
	return RectUpdated{
		X: binary.BigEndian.Uint16(d[0:]),
		Y: binary.BigEndian.Uint16(d[2:]),
		W: binary.BigEndian.Uint16(d[4:]),
		H: binary.BigEndian.Uint16(d[6:]),
	}, nil
}

// --- page_error ---

// PageError reports a command rejected for addressing a page that does not
// exist. The command is dropped; nothing identifies which one it was.
//
// Layout: [code:u8]
type PageError struct {
	Code uint8
}

func (e PageError) Target() string { return EventPageError }

func (e PageError) Encode() []byte {
	return []byte{e.Code}
}

func DecodePageError(d []byte) (PageError, error) {
	if len(d) < 1 {
		return PageError{}, ErrShortPayload
	}
	return PageError{Code: d[0]}, nil
}

// --- palette_data ---

// PaletteData answers a ReadPaletteBlock request. It shares the
// set_palette_block layout.
//
// Layout: [start:u8][count:u8][R:u8][G:u8][B:u8][A:u8] x count
type PaletteData struct {
	Start  uint8
	Colors []Color
}

func (e PaletteData) Target() string { return EventPaletteData }

func (e PaletteData) Encode() []byte {
	return encodePaletteBlock(e.Start, e.Colors)
}

func DecodePaletteData(d []byte) (PaletteData, error) {
	start, colors, err := decodePaletteBlock(d)
	if err != nil {
		return PaletteData{}, err
	}
	return PaletteData{Start: start, Colors: colors}, nil
}
