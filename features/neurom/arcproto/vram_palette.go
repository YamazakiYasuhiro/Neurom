package arcproto

// maxPaletteBlockColors is the largest number of colours a block command can
// address, because the count occupies a single byte.
const maxPaletteBlockColors = 255

// --- set_palette ---

// SetPalette assigns one palette entry.
//
// Layout: [index:u8][R:u8][G:u8][B:u8][A:u8]
//
// The alpha byte may be omitted, in which case the receiver assumes 255. Set
// OmitAlpha to reproduce that shorter form; it exists so a decoded message
// re-encodes to the exact bytes it arrived as.
type SetPalette struct {
	Index     uint8
	Color     Color
	OmitAlpha bool
}

func (c SetPalette) Target() string { return TargetSetPalette }

func (c SetPalette) Encode() []byte {
	if c.OmitAlpha {
		return []byte{c.Index, c.Color.R, c.Color.G, c.Color.B}
	}
	return []byte{c.Index, c.Color.R, c.Color.G, c.Color.B, c.Color.A}
}

func DecodeSetPalette(d []byte) (SetPalette, error) {
	if len(d) < 4 {
		return SetPalette{}, ErrShortPayload
	}
	c := SetPalette{
		Index: d[0],
		Color: Color{R: d[1], G: d[2], B: d[3], A: 255},
	}
	if len(d) >= 5 {
		c.Color.A = d[4]
	} else {
		c.OmitAlpha = true
	}
	return c, nil
}

// --- set_palette_block ---

// SetPaletteBlock assigns a run of palette entries starting at Start. Entries
// that would run past index 255 are discarded by the receiver.
//
// Layout: [start:u8][count:u8][R:u8][G:u8][B:u8][A:u8] x count
type SetPaletteBlock struct {
	Start  uint8
	Colors []Color
}

const paletteBlockHeaderLen = 2

func (c SetPaletteBlock) Target() string { return TargetSetPaletteBlock }

func (c SetPaletteBlock) Encode() []byte {
	return encodePaletteBlock(c.Start, c.Colors)
}

func DecodeSetPaletteBlock(d []byte) (SetPaletteBlock, error) {
	start, colors, err := decodePaletteBlock(d)
	if err != nil {
		return SetPaletteBlock{}, err
	}
	return SetPaletteBlock{Start: start, Colors: colors}, nil
}

// --- read_palette_block ---

// ReadPaletteBlock requests a run of palette entries. The receiver answers with
// a PaletteData event.
//
// Layout: [start:u8][count:u8]
type ReadPaletteBlock struct {
	Start uint8
	Count uint8
}

func (c ReadPaletteBlock) Target() string { return TargetReadPaletteBlock }

func (c ReadPaletteBlock) Encode() []byte {
	return []byte{c.Start, c.Count}
}

func DecodeReadPaletteBlock(d []byte) (ReadPaletteBlock, error) {
	if len(d) < 2 {
		return ReadPaletteBlock{}, ErrShortPayload
	}
	return ReadPaletteBlock{Start: d[0], Count: d[1]}, nil
}

// encodePaletteBlock writes the layout shared by set_palette_block and the
// palette_data event. Colours beyond maxPaletteBlockColors are dropped, since
// the count byte cannot describe them.
func encodePaletteBlock(start uint8, colors []Color) []byte {
	count := len(colors)
	if count > maxPaletteBlockColors {
		count = maxPaletteBlockColors
	}
	d := make([]byte, paletteBlockHeaderLen+count*4)
	d[0] = start
	d[1] = uint8(count)
	for i := range count {
		off := paletteBlockHeaderLen + i*4
		d[off] = colors[i].R
		d[off+1] = colors[i].G
		d[off+2] = colors[i].B
		d[off+3] = colors[i].A
	}
	return d
}

func decodePaletteBlock(d []byte) (uint8, []Color, error) {
	if len(d) < paletteBlockHeaderLen {
		return 0, nil, ErrShortPayload
	}
	count := int(d[1])
	if len(d) < paletteBlockHeaderLen+count*4 {
		return 0, nil, ErrShortPayload
	}
	colors := make([]Color, count)
	for i := range count {
		off := paletteBlockHeaderLen + i*4
		colors[i] = Color{R: d[off], G: d[off+1], B: d[off+2], A: d[off+3]}
	}
	return d[0], colors, nil
}
