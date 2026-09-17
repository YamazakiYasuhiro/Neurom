package vram

import "github.com/axsh/neurom/arcproto"

// BlendMode re-exports the protocol definition so that callers of BlendPixel do
// not need to import arcproto. It is an alias rather than a defined type, so the
// two spellings are interchangeable.
type BlendMode = arcproto.BlendMode

// The wire values live in arcproto; the comments here describe what BlendPixel
// computes for each mode.
const (
	// BlendReplace overwrites the destination with the source (no blending).
	BlendReplace = arcproto.BlendReplace
	// BlendAlpha performs standard alpha compositing: dst = src*α + dst*(1-α).
	BlendAlpha = arcproto.BlendAlpha
	// BlendAdditive performs additive blending: dst = min(src*α + dst, 255).
	BlendAdditive = arcproto.BlendAdditive
	// BlendMultiply performs multiplicative blending: dst = src * dst / 255.
	BlendMultiply = arcproto.BlendMultiply
	// BlendScreen performs screen blending: dst = 255 - (255-src)*(255-dst)/255.
	BlendScreen = arcproto.BlendScreen
)

// BlendPixel computes the blended RGBA output for a single pixel.
// src and dst are [R, G, B, A] arrays.
// For Alpha and Additive modes, alpha is taken from src[3].
// For Multiply and Screen modes, alpha is ignored (direct color math).
func BlendPixel(mode BlendMode, src, dst [4]uint8) [4]uint8 {
	switch mode {
	case BlendReplace:
		return src

	case BlendAlpha:
		alpha := uint16(src[3])
		invAlpha := 255 - alpha
		return [4]uint8{
			uint8((uint16(src[0])*alpha + uint16(dst[0])*invAlpha) / 255),
			uint8((uint16(src[1])*alpha + uint16(dst[1])*invAlpha) / 255),
			uint8((uint16(src[2])*alpha + uint16(dst[2])*invAlpha) / 255),
			255,
		}

	case BlendAdditive:
		alpha := uint16(src[3])
		return [4]uint8{
			clamp8(uint16(src[0])*alpha/255 + uint16(dst[0])),
			clamp8(uint16(src[1])*alpha/255 + uint16(dst[1])),
			clamp8(uint16(src[2])*alpha/255 + uint16(dst[2])),
			255,
		}

	case BlendMultiply:
		return [4]uint8{
			uint8(uint16(src[0]) * uint16(dst[0]) / 255),
			uint8(uint16(src[1]) * uint16(dst[1]) / 255),
			uint8(uint16(src[2]) * uint16(dst[2]) / 255),
			255,
		}

	case BlendScreen:
		return [4]uint8{
			uint8(255 - uint16(255-src[0])*uint16(255-dst[0])/255),
			uint8(255 - uint16(255-src[1])*uint16(255-dst[1])/255),
			uint8(255 - uint16(255-src[2])*uint16(255-dst[2])/255),
			255,
		}

	default:
		return src
	}
}

// clamp8 clamps a uint16 value to the uint8 range [0, 255].
func clamp8(v uint16) uint8 {
	if v > 255 {
		return 255
	}
	return uint8(v)
}
