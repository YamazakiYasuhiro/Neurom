package vram

// Frame is a consistent snapshot of the VRAM display state. Every field is
// read under a single lock acquisition, so the dimensions always describe the
// buffers they arrive with. The buffers belong to the caller, which is what
// makes them safe to read while the VRAM module keeps drawing.
type Frame struct {
	Index   []uint8
	Color   []uint8
	Width   int
	Height  int
	Palette [256][4]uint8
	Page    int
	ViewX   int16
	ViewY   int16
}

// Snapshot copies the display state into dst, reusing dst's buffers when they
// are large enough. Callers are expected to keep one Frame and pass it every
// time so that a steady state allocates nothing.
func (v *VRAMModule) Snapshot(dst *Frame) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	pg := &v.pages[v.displayPage]
	dst.Index = resizeTo(dst.Index, len(pg.index))
	copy(dst.Index, pg.index)
	dst.Color = resizeTo(dst.Color, len(pg.color))
	copy(dst.Color, pg.color)
	dst.Width = pg.width
	dst.Height = pg.height
	dst.Palette = v.palette
	dst.Page = v.displayPage
	dst.ViewX = v.viewportX
	dst.ViewY = v.viewportY
}

// resizeTo returns a slice of length n, keeping b's array when it has room.
func resizeTo(b []uint8, n int) []uint8 {
	if cap(b) >= n {
		return b[:n]
	}
	return make([]uint8, n)
}
