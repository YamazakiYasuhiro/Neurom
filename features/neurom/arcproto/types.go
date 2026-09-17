package arcproto

import (
	"errors"
	"math"
)

var (
	// ErrShortPayload is returned when a payload is shorter than the fixed part
	// of its layout. Receivers treat this as "drop the message".
	ErrShortPayload = errors.New("arcproto: payload too short")
	// ErrBadPayload is returned when a payload is long enough but internally
	// inconsistent, such as a declared element count that overruns the buffer.
	ErrBadPayload = errors.New("arcproto: malformed payload")
)

// Command is implemented by every command and event in this package.
type Command interface {
	// Target returns the wire name the receiver dispatches on.
	Target() string
	// Encode returns the payload bytes. A command without a payload returns nil.
	Encode() []byte
}

// Rotation is a clockwise rotation where a full turn is 256 steps, chosen so the
// angle fits in a single byte. The renderer reads it as
// float64(rotation) / 256.0 * 2π.
type Rotation uint8

// rotationSteps is the number of Rotation steps in a full turn.
const rotationSteps = 256

// Degrees converts an angle in degrees to a Rotation. Angles outside one turn
// wrap around, so 450 and -270 both yield a quarter turn.
func Degrees(deg float64) Rotation {
	return Turns(deg / 360.0)
}

// Turns converts a number of turns to a Rotation, where 1.0 is a full turn.
// Values outside one turn wrap around.
func Turns(t float64) Rotation {
	// Masking a negative value relies on two's complement, which Go guarantees,
	// so -0.25 turns becomes 192 rather than an out-of-range conversion.
	return Rotation(int(math.Round(t*rotationSteps)) & 0xFF)
}

// Scale is an 8.8 fixed-point magnification factor. The renderer reads it as
// float64(scale) / 256.0.
type Scale uint16

// ScaleOne is the identity scale factor.
const ScaleOne Scale = 0x0100

// ScaleOf converts a magnification factor to a Scale. Values of zero or less
// yield ScaleOne, matching the renderer, which clamps a non-positive factor to
// 1.0 rather than collapsing the sprite.
func ScaleOf(f float64) Scale {
	if f <= 0 {
		return ScaleOne
	}
	steps := math.Round(f * 256.0)
	if steps > math.MaxUint16 {
		return math.MaxUint16
	}
	return Scale(steps)
}

// BlendMode selects how source pixels combine with the destination.
type BlendMode uint8

const (
	BlendReplace  BlendMode = 0x00
	BlendAlpha    BlendMode = 0x01
	BlendAdditive BlendMode = 0x02
	BlendMultiply BlendMode = 0x03
	BlendScreen   BlendMode = 0x04
)

// Color is an RGBA palette entry.
type Color struct{ R, G, B, A uint8 }

// Error codes carried by the page_error event. 0x02 is unassigned: the VRAM
// module has never emitted it.
const (
	PageErrInvalidPage    uint8 = 0x01 // page index outside the allocated pages
	PageErrInvalidDisplay uint8 = 0x03 // display page index outside the allocated pages
)
