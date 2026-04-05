package vram

import (
	"math"
)

// TransformBlit applies rotation and scaling to source pixel data
// around the specified pivot point.
// Returns the transformed pixel buffer, its dimensions, and the offset
// from the pivot point to the top-left of the output buffer.
// Pixels outside the source range are set to 0 (transparent/skip marker).
func TransformBlit(
	src []uint8, srcW, srcH int,
	pivotX, pivotY int,
	rotation uint8,
	scaleX, scaleY uint16,
) (dst []uint8, dstW, dstH, offsetX, offsetY int) {
	return TransformBlitParallel(src, srcW, srcH, pivotX, pivotY, rotation, scaleX, scaleY, nil)
}

// TransformBlitParallel is the parallelized version of TransformBlit.
// parallelFn dispatches row ranges to a worker pool.
// If parallelFn is nil, the function falls back to sequential execution.
func TransformBlitParallel(
	src []uint8, srcW, srcH int,
	pivotX, pivotY int,
	rotation uint8,
	scaleX, scaleY uint16,
	parallelFn func(totalRows int, fn func(startRow, endRow int)),
) (dst []uint8, dstW, dstH, offsetX, offsetY int) {
	angleRad := float64(rotation) / 256.0 * 2.0 * math.Pi
	cosA := math.Cos(angleRad)
	sinA := math.Sin(angleRad)

	sx := float64(scaleX) / 256.0
	sy := float64(scaleY) / 256.0

	if sx <= 0 {
		sx = 1.0
	}
	if sy <= 0 {
		sy = 1.0
	}

	corners := [4][2]float64{
		{0, 0},
		{float64(srcW), 0},
		{0, float64(srcH)},
		{float64(srcW), float64(srcH)},
	}

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)

	for _, c := range corners {
		dx := c[0] - float64(pivotX)
		dy := c[1] - float64(pivotY)
		tx := dx*cosA*sx - dy*sinA*sy
		ty := dx*sinA*sx + dy*cosA*sy
		minX = math.Min(minX, tx)
		minY = math.Min(minY, ty)
		maxX = math.Max(maxX, tx)
		maxY = math.Max(maxY, ty)
	}

	dstW = int(math.Ceil(maxX - minX))
	dstH = int(math.Ceil(maxY - minY))

	if dstW <= 0 || dstH <= 0 {
		return nil, 0, 0, 0, 0
	}

	offsetX = int(math.Floor(minX))
	offsetY = int(math.Floor(minY))

	dst = make([]uint8, dstW*dstH)

	fill := func(startRow, endRow int) {
		for outY := startRow; outY < endRow; outY++ {
			for outX := range dstW {
				fx := float64(outX) + 0.5 + minX
				fy := float64(outY) + 0.5 + minY

				srcFX := (fx*cosA+fy*sinA)/sx + float64(pivotX)
				srcFY := (-fx*sinA+fy*cosA)/sy + float64(pivotY)

				srcPX := int(math.Floor(srcFX))
				srcPY := int(math.Floor(srcFY))

				if srcPX >= 0 && srcPX < srcW && srcPY >= 0 && srcPY < srcH {
					dst[outY*dstW+outX] = src[srcPY*srcW+srcPX]
				}
			}
		}
	}

	if parallelFn != nil {
		parallelFn(dstH, fill)
	} else {
		fill(0, dstH)
	}

	return dst, dstW, dstH, offsetX, offsetY
}
