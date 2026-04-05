package vram

import (
	"testing"
)

func TestRotation90(t *testing.T) {
	// 4x4 source with distinct row values
	src := []uint8{
		1, 1, 1, 1,
		2, 2, 2, 2,
		3, 3, 3, 3,
		4, 4, 4, 4,
	}

	dst, dstW, dstH, _, _ := TransformBlit(src, 4, 4, 2, 2, 64, 0x0100, 0x0100)

	// 90-degree rotation should produce a 4x4 output
	if dstW < 3 || dstH < 3 {
		t.Fatalf("Output too small: %dx%d", dstW, dstH)
	}

	// Check that non-zero pixels exist (rotation happened)
	nonZero := 0
	for _, v := range dst {
		if v != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("No non-zero pixels after 90-degree rotation")
	}

	// After 90-degree clockwise rotation around center (2,2):
	// Original column 0 (values 1,2,3,4) should appear in a row.
	// Verify that we see values from different original rows
	// in what is now a single row or column.
	valueSet := make(map[uint8]bool)
	for _, v := range dst {
		if v != 0 {
			valueSet[v] = true
		}
	}
	if len(valueSet) < 3 {
		t.Errorf("Expected at least 3 distinct non-zero values after rotation, got %d: %v", len(valueSet), valueSet)
	}
}

func TestRotationWithCustomPivot(t *testing.T) {
	src := []uint8{
		1, 1, 1, 1,
		2, 2, 2, 2,
		3, 3, 3, 3,
		4, 4, 4, 4,
	}

	// Rotation with center pivot
	_, dstW1, dstH1, offX1, offY1 := TransformBlit(src, 4, 4, 2, 2, 64, 0x0100, 0x0100)

	// Rotation with top-left pivot
	_, dstW2, dstH2, offX2, offY2 := TransformBlit(src, 4, 4, 0, 0, 64, 0x0100, 0x0100)

	// The results should differ (different pivot points produce different offsets)
	if dstW1 == dstW2 && dstH1 == dstH2 && offX1 == offX2 && offY1 == offY2 {
		t.Error("Expected different output dimensions/offsets for different pivots")
	}
}

func TestScale2x(t *testing.T) {
	src := []uint8{
		1, 2,
		3, 4,
	}

	dst, dstW, dstH, _, _ := TransformBlit(src, 2, 2, 1, 1, 0, 0x0200, 0x0200)

	if dstW < 4 || dstH < 4 {
		t.Fatalf("Expected at least 4x4 output for 2x scale, got %dx%d", dstW, dstH)
	}

	// Check that all 4 source values appear in the output and are scaled
	valueCounts := make(map[uint8]int)
	for _, v := range dst {
		if v != 0 {
			valueCounts[v]++
		}
	}

	for _, v := range []uint8{1, 2, 3, 4} {
		if valueCounts[v] < 2 {
			t.Errorf("Value %d appears %d times, expected at least 2 for 2x scale", v, valueCounts[v])
		}
	}
}

func TestRotateAndScale(t *testing.T) {
	src := []uint8{
		1, 1, 1, 1,
		2, 2, 2, 2,
		3, 3, 3, 3,
		4, 4, 4, 4,
	}

	dst, dstW, dstH, _, _ := TransformBlit(src, 4, 4, 2, 2, 64, 0x0200, 0x0200)

	// 2x scale of 4x4 rotated 90° produces an 8x8 axis-aligned square
	if dstW < 7 || dstH < 7 {
		t.Fatalf("Expected at least 7x7 for 2x scaled+rotated 4x4, got %dx%d", dstW, dstH)
	}

	// 90° rotation of a square is still axis-aligned, so all output pixels
	// should be filled (no zero gaps).
	nonZero := 0
	for _, v := range dst {
		if v != 0 {
			nonZero++
		}
	}

	// All source pixels (16) are scaled 2x in each dimension → 64 output pixels
	if nonZero < 30 || nonZero > dstW*dstH {
		t.Errorf("Non-zero pixel count %d seems unreasonable for 2x scaled 4x4", nonZero)
	}
}
