package arcproto

import "encoding/binary"

// --- read_rect ---

// ReadRect requests a rectangle of palette indices. The receiver answers with a
// RectData event. Pixels outside the page read back as index 0.
//
// Layout: [page:u8][x:u16][y:u16][w:u16][h:u16]
type ReadRect struct {
	Page uint8
	X, Y uint16
	W, H uint16
}

const readRectLen = 9

func (c ReadRect) Target() string { return TargetReadRect }

func (c ReadRect) Encode() []byte {
	d := make([]byte, readRectLen)
	d[0] = c.Page
	binary.BigEndian.PutUint16(d[1:], c.X)
	binary.BigEndian.PutUint16(d[3:], c.Y)
	binary.BigEndian.PutUint16(d[5:], c.W)
	binary.BigEndian.PutUint16(d[7:], c.H)
	return d
}

func DecodeReadRect(d []byte) (ReadRect, error) {
	if len(d) < readRectLen {
		return ReadRect{}, ErrShortPayload
	}
	return ReadRect{
		Page: d[0],
		X:    binary.BigEndian.Uint16(d[1:]),
		Y:    binary.BigEndian.Uint16(d[3:]),
		W:    binary.BigEndian.Uint16(d[5:]),
		H:    binary.BigEndian.Uint16(d[7:]),
	}, nil
}

// --- get_stats ---

// GetStats requests a performance snapshot. The receiver answers with a
// stats_data event carrying a JSON body, which is why this package does not
// define a binary layout for the reply.
type GetStats struct{}

func (c GetStats) Target() string { return TargetGetStats }

// Encode returns nil: the command carries no payload.
func (c GetStats) Encode() []byte { return nil }
