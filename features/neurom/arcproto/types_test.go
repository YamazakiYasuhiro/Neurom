package arcproto

import "testing"

func TestDegrees(t *testing.T) {
	tests := []struct {
		name string
		deg  float64
		want Rotation
	}{
		{"zero", 0, 0},
		{"quarter", 90, 64},
		{"half", 180, 128},
		{"three_quarter", 270, 192},
		{"full_wraps_to_zero", 360, 0},
		{"over_full", 450, 64},
		{"negative_quarter", -90, 192},
		{"negative_full", -360, 0},
		{"one_step", 360.0 / 256.0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Degrees(tt.deg); got != tt.want {
				t.Errorf("Degrees(%v) = %d, want %d", tt.deg, got, tt.want)
			}
		})
	}
}

func TestTurns(t *testing.T) {
	tests := []struct {
		name string
		turn float64
		want Rotation
	}{
		{"zero", 0, 0},
		{"quarter", 0.25, 64},
		{"half", 0.5, 128},
		{"full_wraps_to_zero", 1.0, 0},
		{"one_and_quarter", 1.25, 64},
		{"negative_quarter", -0.25, 192},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Turns(tt.turn); got != tt.want {
				t.Errorf("Turns(%v) = %d, want %d", tt.turn, got, tt.want)
			}
		})
	}
}

func TestScaleOf(t *testing.T) {
	tests := []struct {
		name string
		f    float64
		want Scale
	}{
		{"one", 1.0, ScaleOne},
		{"one_is_0x0100", 1.0, 0x0100},
		{"two", 2.0, 0x0200},
		{"half", 0.5, 0x0080},
		{"quarter", 0.25, 0x0040},
		{"one_and_half", 1.5, 0x0180},
		// transform.go clamps scale <= 0 to 1.0, so ScaleOf mirrors that behaviour.
		{"zero_clamps_to_one", 0, ScaleOne},
		{"negative_clamps_to_one", -1.0, ScaleOne},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScaleOf(tt.f); got != tt.want {
				t.Errorf("ScaleOf(%v) = %#04x, want %#04x", tt.f, uint16(got), uint16(tt.want))
			}
		})
	}
}

// TestBlendModeValues locks the wire values of the blend modes.
// They must match internal/modules/vram/blend.go, otherwise rendering changes.
func TestBlendModeValues(t *testing.T) {
	tests := []struct {
		name string
		got  BlendMode
		want uint8
	}{
		{"replace", BlendReplace, 0x00},
		{"alpha", BlendAlpha, 0x01},
		{"additive", BlendAdditive, 0x02},
		{"multiply", BlendMultiply, 0x03},
		{"screen", BlendScreen, 0x04},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if uint8(tt.got) != tt.want {
				t.Errorf("%s = %#02x, want %#02x", tt.name, uint8(tt.got), tt.want)
			}
		})
	}
}

// TestPageErrorCodes locks the error codes emitted by the VRAM module.
func TestPageErrorCodes(t *testing.T) {
	if PageErrInvalidPage != 0x01 {
		t.Errorf("PageErrInvalidPage = %#02x, want 0x01", PageErrInvalidPage)
	}
	if PageErrInvalidDisplay != 0x03 {
		t.Errorf("PageErrInvalidDisplay = %#02x, want 0x03", PageErrInvalidDisplay)
	}
}
