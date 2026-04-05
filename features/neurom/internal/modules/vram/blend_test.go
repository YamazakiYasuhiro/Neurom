package vram

import (
	"math"
	"testing"
)

func TestBlendReplace(t *testing.T) {
	tests := []struct {
		name string
		src  [4]uint8
		dst  [4]uint8
		want [4]uint8
	}{
		{
			name: "opaque source replaces destination",
			src:  [4]uint8{100, 150, 200, 255},
			dst:  [4]uint8{50, 50, 50, 255},
			want: [4]uint8{100, 150, 200, 255},
		},
		{
			name: "semi-transparent source still replaces",
			src:  [4]uint8{100, 150, 200, 128},
			dst:  [4]uint8{50, 50, 50, 255},
			want: [4]uint8{100, 150, 200, 128},
		},
		{
			name: "zero alpha source replaces",
			src:  [4]uint8{100, 150, 200, 0},
			dst:  [4]uint8{50, 50, 50, 255},
			want: [4]uint8{100, 150, 200, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlendPixel(BlendReplace, tt.src, tt.dst)
			if got != tt.want {
				t.Errorf("BlendPixel(Replace, %v, %v) = %v, want %v", tt.src, tt.dst, got, tt.want)
			}
		})
	}
}

func TestBlendAlpha(t *testing.T) {
	tests := []struct {
		name      string
		src       [4]uint8
		dst       [4]uint8
		want      [4]uint8
		tolerance uint8
	}{
		{
			name:      "half-alpha red over blue produces purple",
			src:       [4]uint8{255, 0, 0, 128},
			dst:       [4]uint8{0, 0, 255, 255},
			want:      [4]uint8{128, 0, 127, 255},
			tolerance: 2,
		},
		{
			name:      "full-alpha source fully replaces",
			src:       [4]uint8{255, 0, 0, 255},
			dst:       [4]uint8{0, 0, 255, 255},
			want:      [4]uint8{255, 0, 0, 255},
			tolerance: 1,
		},
		{
			name:      "zero-alpha source keeps destination",
			src:       [4]uint8{255, 0, 0, 0},
			dst:       [4]uint8{0, 0, 255, 255},
			want:      [4]uint8{0, 0, 255, 255},
			tolerance: 1,
		},
		{
			name:      "quarter-alpha green over white",
			src:       [4]uint8{0, 255, 0, 64},
			dst:       [4]uint8{255, 255, 255, 255},
			want:      [4]uint8{191, 255, 191, 255},
			tolerance: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlendPixel(BlendAlpha, tt.src, tt.dst)
			for i := range 4 {
				diff := math.Abs(float64(got[i]) - float64(tt.want[i]))
				if diff > float64(tt.tolerance) {
					t.Errorf("BlendPixel(Alpha, %v, %v) = %v, want %v (channel %d diff=%.0f > tolerance=%d)",
						tt.src, tt.dst, got, tt.want, i, diff, tt.tolerance)
					break
				}
			}
		})
	}
}

func TestBlendAdditive(t *testing.T) {
	tests := []struct {
		name      string
		src       [4]uint8
		dst       [4]uint8
		want      [4]uint8
		tolerance uint8
	}{
		{
			name:      "additive with half alpha",
			src:       [4]uint8{200, 200, 200, 128},
			dst:       [4]uint8{100, 100, 100, 255},
			want:      [4]uint8{200, 200, 200, 255},
			tolerance: 2,
		},
		{
			name:      "additive clamps to 255",
			src:       [4]uint8{255, 255, 255, 255},
			dst:       [4]uint8{200, 200, 200, 255},
			want:      [4]uint8{255, 255, 255, 255},
			tolerance: 1,
		},
		{
			name:      "additive with zero alpha adds nothing",
			src:       [4]uint8{255, 255, 255, 0},
			dst:       [4]uint8{100, 100, 100, 255},
			want:      [4]uint8{100, 100, 100, 255},
			tolerance: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlendPixel(BlendAdditive, tt.src, tt.dst)
			for i := range 4 {
				diff := math.Abs(float64(got[i]) - float64(tt.want[i]))
				if diff > float64(tt.tolerance) {
					t.Errorf("BlendPixel(Additive, %v, %v) = %v, want %v (channel %d diff=%.0f > tolerance=%d)",
						tt.src, tt.dst, got, tt.want, i, diff, tt.tolerance)
					break
				}
			}
		})
	}
}

func TestBlendMultiply(t *testing.T) {
	tests := []struct {
		name      string
		src       [4]uint8
		dst       [4]uint8
		want      [4]uint8
		tolerance uint8
	}{
		{
			name:      "multiply mid-gray with colors",
			src:       [4]uint8{128, 128, 128, 255},
			dst:       [4]uint8{200, 100, 50, 255},
			want:      [4]uint8{100, 50, 25, 255},
			tolerance: 2,
		},
		{
			name:      "multiply white preserves destination",
			src:       [4]uint8{255, 255, 255, 255},
			dst:       [4]uint8{100, 200, 50, 255},
			want:      [4]uint8{100, 200, 50, 255},
			tolerance: 1,
		},
		{
			name:      "multiply black produces black",
			src:       [4]uint8{0, 0, 0, 255},
			dst:       [4]uint8{200, 200, 200, 255},
			want:      [4]uint8{0, 0, 0, 255},
			tolerance: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlendPixel(BlendMultiply, tt.src, tt.dst)
			for i := range 4 {
				diff := math.Abs(float64(got[i]) - float64(tt.want[i]))
				if diff > float64(tt.tolerance) {
					t.Errorf("BlendPixel(Multiply, %v, %v) = %v, want %v (channel %d diff=%.0f > tolerance=%d)",
						tt.src, tt.dst, got, tt.want, i, diff, tt.tolerance)
					break
				}
			}
		})
	}
}

func TestBlendScreen(t *testing.T) {
	tests := []struct {
		name      string
		src       [4]uint8
		dst       [4]uint8
		want      [4]uint8
		tolerance uint8
	}{
		{
			name:      "screen mid-gray with mid-gray",
			src:       [4]uint8{128, 128, 128, 255},
			dst:       [4]uint8{100, 100, 100, 255},
			want:      [4]uint8{178, 178, 178, 255},
			tolerance: 2,
		},
		{
			name:      "screen black preserves destination",
			src:       [4]uint8{0, 0, 0, 255},
			dst:       [4]uint8{100, 200, 50, 255},
			want:      [4]uint8{100, 200, 50, 255},
			tolerance: 1,
		},
		{
			name:      "screen white produces white",
			src:       [4]uint8{255, 255, 255, 255},
			dst:       [4]uint8{100, 200, 50, 255},
			want:      [4]uint8{255, 255, 255, 255},
			tolerance: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlendPixel(BlendScreen, tt.src, tt.dst)
			for i := range 4 {
				diff := math.Abs(float64(got[i]) - float64(tt.want[i]))
				if diff > float64(tt.tolerance) {
					t.Errorf("BlendPixel(Screen, %v, %v) = %v, want %v (channel %d diff=%.0f > tolerance=%d)",
						tt.src, tt.dst, got, tt.want, i, diff, tt.tolerance)
					break
				}
			}
		})
	}
}
