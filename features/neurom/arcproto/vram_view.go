package arcproto

import "encoding/binary"

// --- set_viewport ---

// SetViewport shifts the visible window over the display page, which is how
// hardware scrolling works: the page keeps its contents and only the origin
// moves. The offsets are signed so the window can move up and to the left.
//
// Layout: [off_x:i16][off_y:i16]
type SetViewport struct {
	OffX, OffY int16
}

const setViewportLen = 4

func (c SetViewport) Target() string { return TargetSetViewport }

func (c SetViewport) Encode() []byte {
	d := make([]byte, setViewportLen)
	binary.BigEndian.PutUint16(d[0:], uint16(c.OffX))
	binary.BigEndian.PutUint16(d[2:], uint16(c.OffY))
	return d
}

func DecodeSetViewport(d []byte) (SetViewport, error) {
	if len(d) < setViewportLen {
		return SetViewport{}, ErrShortPayload
	}
	return SetViewport{
		OffX: int16(binary.BigEndian.Uint16(d[0:])),
		OffY: int16(binary.BigEndian.Uint16(d[2:])),
	}, nil
}
