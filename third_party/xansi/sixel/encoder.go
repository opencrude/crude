package sixel

import (
	"context"
	"fmt"
	"image"
	"io"
	"strconv"
)

// Sixels encode a paletted image one six-pixel-high band at a time, one color
// at a time. A carriage return brings the graphics cursor back to the start
// of the band for the next color; a line break starts the next band.

// Sixel control functions.
const (
	LineBreak        byte = '-'
	CarriageReturn   byte = '$'
	RepeatIntroducer byte = '!'
	ColorIntroducer  byte = '#'
	RasterAttribute  byte = '"'
)

// Encoder is a Sixel encoder. It encodes an image to Sixel data format.
type Encoder struct {
	// Dither reduces palette bands in opaque images with more than 256 colors.
	// It uses Floyd-Steinberg diffusion with the adaptive palette. Images with
	// transparency retain the ordinary alpha-aware encoding.
	Dither bool
}

// Encode writes the contents of a Sixel DCS, including the raster dimensions
// and palette, but not the DCS introducer or terminator.
func (e *Encoder) Encode(w io.Writer, img image.Image) error {
	return e.EncodeContext(context.Background(), w, img)
}

// EncodeContext is Encode with cooperative cancellation during palette
// construction and between raster rows. Cancellation or a writer error can
// leave an incomplete payload; callers framing a DCS must discard it.
func (e *Encoder) EncodeContext(ctx context.Context, w io.Writer, img image.Image) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if img == nil {
		return nil
	}

	q, err := e.Quantize(ctx, img)
	if err != nil {
		return err
	}
	data, err := q.AppendEncode(ctx, nil, q.Bounds())
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("error encoding sixel: %w", err)
	}
	return nil
}

// sixelBands turns up to six rows of palette entries into one Sixel band.
// Its scratch is reused across bands; one value serves one goroutine.
type sixelBands struct {
	width  int
	colors []sixelColor
	// A mask packs the band's six pixels for one column/color. The occupied
	// range is found while filling it so a sparse color neither scans its
	// leading holes nor encodes its trailing holes.
	mask        []byte
	first, end  [MaxColors]int
	transparent [MaxColors]bool
}

func newSixelBands(width int, colors []sixelColor) *sixelBands {
	b := &sixelBands{width: width, colors: colors, mask: make([]byte, width*len(colors))}
	for i, c := range colors {
		b.transparent[i] = c.Alpha == 0
	}
	return b
}

func (b *sixelBands) append(out []byte, rows [][]uint8) []byte {
	width := b.width
	for bit, row := range rows {
		m := byte(1) << bit
		for col, index := range row[:width] {
			if b.transparent[index] {
				continue
			}
			b.mask[int(index)*width+col] |= m
			if b.end[index] == 0 || col < b.first[index] {
				b.first[index] = col
			}
			if col >= b.end[index] {
				b.end[index] = col + 1
			}
		}
	}

	start := len(out)
	for index, last := range b.end[:len(b.colors)] {
		if last == 0 {
			continue
		}
		if len(out) != start {
			out = append(out, CarriageReturn)
		}
		out = append(out, ColorIntroducer)
		out = strconv.AppendInt(out, int64(index), 10)
		out = appendSixelRun(out, b.first[index], '?')
		columns := b.mask[index*width+b.first[index] : index*width+last]
		for from := 0; from < len(columns); {
			to := from + 1
			for to < len(columns) && columns[to] == columns[from] {
				to++
			}
			out = appendSixelRun(out, to-from, columns[from]+'?')
			from = to
		}
		clear(columns)
		b.end[index] = 0
	}
	return append(out, LineBreak)
}

func appendSixelRun(out []byte, n int, c byte) []byte {
	if n > 3 {
		out = append(out, RepeatIntroducer)
		out = strconv.AppendInt(out, int64(n), 10)
		return append(out, c)
	}
	for range n {
		out = append(out, c)
	}
	return out
}
