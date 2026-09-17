package arcproto

import "encoding/binary"

// --- set_page_count ---

// SetPageCount resizes the pool of pages. Growing it allocates pages at the
// default size; shrinking it discards the pages above the new count and resets
// the display page if it would fall outside the pool.
//
// Layout: [count:u8]
type SetPageCount struct {
	// Count is the number of pages, where 0 means 256. Use ResolvedCount to read
	// it as a plain number.
	Count uint8
}

func (c SetPageCount) Target() string { return TargetSetPageCount }

func (c SetPageCount) Encode() []byte {
	return []byte{c.Count}
}

// ResolvedCount returns the page count with the 0-means-256 encoding applied.
func (c SetPageCount) ResolvedCount() int {
	if c.Count == 0 {
		return 256
	}
	return int(c.Count)
}

func DecodeSetPageCount(d []byte) (SetPageCount, error) {
	if len(d) < 1 {
		return SetPageCount{}, ErrShortPayload
	}
	return SetPageCount{Count: d[0]}, nil
}

// --- set_display_page ---

// SetDisplayPage selects the page the monitor presents.
//
// Layout: [page:u8]
type SetDisplayPage struct {
	Page uint8
}

func (c SetDisplayPage) Target() string { return TargetSetDisplayPage }

func (c SetDisplayPage) Encode() []byte {
	return []byte{c.Page}
}

func DecodeSetDisplayPage(d []byte) (SetDisplayPage, error) {
	if len(d) < 1 {
		return SetDisplayPage{}, ErrShortPayload
	}
	return SetDisplayPage{Page: d[0]}, nil
}

// --- swap_pages ---

// SwapPages exchanges two pages without copying pixel data, which is how double
// buffering presents a finished frame.
//
// Layout: [page1:u8][page2:u8]
type SwapPages struct {
	Page1, Page2 uint8
}

func (c SwapPages) Target() string { return TargetSwapPages }

func (c SwapPages) Encode() []byte {
	return []byte{c.Page1, c.Page2}
}

func DecodeSwapPages(d []byte) (SwapPages, error) {
	if len(d) < 2 {
		return SwapPages{}, ErrShortPayload
	}
	return SwapPages{Page1: d[0], Page2: d[1]}, nil
}

// --- copy_page ---

// CopyPage duplicates a whole page, including its dimensions. A request where
// Src equals Dst is ignored by the receiver.
//
// Layout: [src:u8][dst:u8]
type CopyPage struct {
	Src, Dst uint8
}

func (c CopyPage) Target() string { return TargetCopyPage }

func (c CopyPage) Encode() []byte {
	return []byte{c.Src, c.Dst}
}

func DecodeCopyPage(d []byte) (CopyPage, error) {
	if len(d) < 2 {
		return CopyPage{}, ErrShortPayload
	}
	return CopyPage{Src: d[0], Dst: d[1]}, nil
}

// --- set_page_size ---

// SetPageSize reallocates a page at a new size, clearing its contents.
//
// Layout: [page:u8][w:u16][h:u16]
type SetPageSize struct {
	Page uint8
	W, H uint16
}

const setPageSizeLen = 5

func (c SetPageSize) Target() string { return TargetSetPageSize }

func (c SetPageSize) Encode() []byte {
	d := make([]byte, setPageSizeLen)
	d[0] = c.Page
	binary.BigEndian.PutUint16(d[1:], c.W)
	binary.BigEndian.PutUint16(d[3:], c.H)
	return d
}

func DecodeSetPageSize(d []byte) (SetPageSize, error) {
	if len(d) < setPageSizeLen {
		return SetPageSize{}, ErrShortPayload
	}
	return SetPageSize{
		Page: d[0],
		W:    binary.BigEndian.Uint16(d[1:]),
		H:    binary.BigEndian.Uint16(d[3:]),
	}, nil
}
