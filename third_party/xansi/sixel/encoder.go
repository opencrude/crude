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

	bounds := img.Bounds()
	palette, dither, err := newSixelPalette(ctx, img, MaxColors, e.Dither)
	if err != nil {
		return err
	}
	if _, err := WriteRaster(w, 1, 1, bounds.Dx(), bounds.Dy()); err != nil {
		return fmt.Errorf("error encoding raster: %w", err)
	}
	for i, c := range palette.PaletteColors {
		if _, err := WriteColor(w, i, 2, int(c.Red), int(c.Green), int(c.Blue)); err != nil {
			return fmt.Errorf("error encoding palette: %w", err)
		}
	}

	return encodeBands(ctx, w, img, palette, dither)
}

func encodeBands(ctx context.Context, w io.Writer, img image.Image, palette sixelPalette, dither bool) error {
	bounds := img.Bounds()
	width := bounds.Dx()
	if bounds.Empty() {
		_, err := io.WriteString(w, string(LineBreak))
		return err
	}

	var diffusion *sixelDiffusion
	if dither {
		diffusion = newSixelDiffusion(width, palette.PaletteColors)
	}
	rgba := readSixelRGBA(img)

	// Retain only one band. A mask packs the band's six pixels for one
	// column/color. Find the occupied range while filling it so a sparse color
	// neither scans its leading holes nor encodes its trailing holes.
	mask := make([]byte, width*len(palette.PaletteColors))
	var first, end [MaxColors]int
	var band []byte
	for y := bounds.Min.Y; y < bounds.Max.Y; {
		for row := 0; row < 6 && y < bounds.Max.Y; row, y = row+1, y+1 {
			if err := ctx.Err(); err != nil {
				return err
			}
			for col := range width {
				r, g, b, a := rgba(bounds.Min.X+col, y)
				var index uint8
				if diffusion != nil {
					index = diffusion.pixel(col, r, g, b)
				} else {
					index = palette.paletteIndexes[sixelConvertColor(r, g, b, a)]
					if palette.PaletteColors[index].Alpha == 0 {
						continue
					}
				}
				mask[int(index)*width+col] |= 1 << row
				if end[index] == 0 || col < first[index] {
					first[index] = col
				}
				end[index] = max(end[index], col+1)
			}
			if diffusion != nil {
				diffusion.nextRow()
			}
		}

		band = band[:0]
		for index, last := range end[:len(palette.PaletteColors)] {
			if last == 0 {
				continue
			}
			if len(band) != 0 {
				band = append(band, CarriageReturn)
			}
			band = append(band, ColorIntroducer)
			band = strconv.AppendInt(band, int64(index), 10)
			band = appendSixelRun(band, first[index], '?')
			columns := mask[index*width+first[index] : index*width+last]
			for from := 0; from < len(columns); {
				to := from + 1
				for to < len(columns) && columns[to] == columns[from] {
					to++
				}
				band = appendSixelRun(band, to-from, columns[from]+'?')
				from = to
			}
			clear(columns)
			end[index] = 0
		}
		band = append(band, LineBreak)
		if _, err := w.Write(band); err != nil {
			return fmt.Errorf("error encoding band: %w", err)
		}
	}
	return ctx.Err()
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
