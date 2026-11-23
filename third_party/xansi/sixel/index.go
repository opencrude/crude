package sixel

import (
	"image"
	"sync"
)

// Sixel color channels are percentages, 0 through 100 inclusive.
const sixelLevels = 101

// sixelChannel8 converts an 8-bit sample to a Sixel percentage exactly as
// ConvertChannel converts the same sample widened to 16 bits.
var sixelChannel8 = func() (table [256]uint8) {
	for v := range table {
		table[v] = uint8(ConvertChannel(uint32(v) * 0x101)) //nolint:gosec // 0-100
	}
	return table
}()

type sixelColorTable [sixelLevels * sixelLevels * sixelLevels]int32

// A dense table costs 4 MB, so it is pooled rather than allocated per image.
// Only the entries an image touched are reset before it is returned.
var sixelColorTables = sync.Pool{New: func() any { return new(sixelColorTable) }}

// sixelColorIndex maps a Sixel color to a small integer. Opaque colors, the
// overwhelmingly common case, use an array indexed by the color itself
// instead of hashing every pixel; other alpha values use a map.
type sixelColorIndex struct {
	dense  *sixelColorTable
	sparse map[sixelColor]int32
}

func newSixelColorIndex() *sixelColorIndex {
	return &sixelColorIndex{dense: sixelColorTables.Get().(*sixelColorTable)}
}

func denseSixelKey(c sixelColor) int {
	return (int(c.Red)*sixelLevels+int(c.Green))*sixelLevels + int(c.Blue)
}

func (x *sixelColorIndex) get(c sixelColor) (int32, bool) {
	if c.Alpha == 100 {
		v := x.dense[denseSixelKey(c)]
		return v - 1, v != 0
	}
	v, ok := x.sparse[c]
	return v, ok
}

func (x *sixelColorIndex) set(c sixelColor, v int32) {
	if c.Alpha == 100 {
		x.dense[denseSixelKey(c)] = v + 1
		return
	}
	if x.sparse == nil {
		x.sparse = make(map[sixelColor]int32)
	}
	x.sparse[c] = v
}

// release clears every key set from colors and returns the dense table.
func (x *sixelColorIndex) release(colors []weightedSixelColor) {
	if x == nil || x.dense == nil {
		return
	}
	for _, c := range colors {
		if c.Alpha == 100 {
			x.dense[denseSixelKey(c.sixelColor)] = 0
		}
	}
	sixelColorTables.Put(x.dense)
	x.dense, x.sparse = nil, nil
}

// sixelPixels reads rows of premultiplied 16-bit samples. Decoded and
// resampled images are usually *image.RGBA; reading its bytes directly avoids
// an interface call per pixel without changing any sample value.
type sixelPixels struct {
	rgba   *image.RGBA
	bounds image.Rectangle
	read   func(x, y int) (uint32, uint32, uint32, uint32)
}

func newSixelPixels(img image.Image) sixelPixels {
	p := sixelPixels{bounds: img.Bounds(), read: readSixelRGBA(img)}
	p.rgba, _ = img.(*image.RGBA)
	return p
}

// colors fills one row of Sixel colors.
func (p sixelPixels) colors(y int, dst []sixelColor) {
	if p.rgba != nil {
		i := p.rgba.PixOffset(p.bounds.Min.X, y)
		for x := range dst {
			s := p.rgba.Pix[i : i+4 : i+4]
			dst[x] = sixelColor{sixelChannel8[s[0]], sixelChannel8[s[1]], sixelChannel8[s[2]], sixelChannel8[s[3]]}
			i += 4
		}
		return
	}
	for x := range dst {
		dst[x] = sixelConvertColor(p.read(p.bounds.Min.X+x, y))
	}
}

// sixelIndexer assigns exact palette entries row by row. Rows are
// independent; each goroutine uses its own indexer.
type sixelIndexer struct {
	pixels  sixelPixels
	palette sixelPalette
	colors  []sixelColor
}

func newSixelIndexer(img image.Image, palette sixelPalette) *sixelIndexer {
	pixels := newSixelPixels(img)
	return &sixelIndexer{pixels: pixels, palette: palette, colors: make([]sixelColor, pixels.bounds.Dx())}
}

func (ix *sixelIndexer) fill(y int, dst []uint8) {
	ix.pixels.colors(y, ix.colors)
	var last sixelColor
	var index uint8
	for x, c := range ix.colors {
		if x == 0 || c != last {
			last = c
			index, _ = ix.palette.lookup(c)
		}
		dst[x] = index
	}
}
