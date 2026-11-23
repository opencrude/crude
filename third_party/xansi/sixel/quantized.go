package sixel

import (
	"context"
	"image"
	"runtime"
	"strconv"
	"sync"
)

// Quantized is an image reduced to Sixel palette entries. Quantization and
// dithering run once; any rectangle of it then encodes without repeating them,
// so adjacent or repeated crops share exact colors and join without seams.
// A Quantized value is immutable and safe for concurrent encoding.
type Quantized struct {
	rect    image.Rectangle
	pix     []uint8 // one palette entry per pixel, row-major over rect
	palette []sixelColor
}

// Quantize builds the adaptive palette for img and assigns every pixel an
// entry, with the same palette, diffusion and alpha rules as EncodeContext.
func (e *Encoder) Quantize(ctx context.Context, img image.Image) (*Quantized, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	palette, dither, err := newSixelPalette(ctx, img, MaxColors, e.Dither)
	if err != nil {
		return nil, err
	}
	defer palette.release()
	q := &Quantized{rect: bounds, pix: make([]uint8, bounds.Dx()*bounds.Dy()), palette: palette.PaletteColors}
	width := bounds.Dx()
	if dither {
		if err := diffuse(ctx, newSixelPixels(img), newDiffusionPalette(q.palette), q.pix); err != nil {
			return nil, err
		}
		return q, nil
	}
	// Exact lookups are independent per row.
	workers := min(parallelWorkers(), max(1, width*bounds.Dy()/minParallelPixels))
	err = parallelRows(ctx, bounds.Dy(), workers, func(_, from, to int) error {
		indexer := newSixelIndexer(img, palette)
		for y := from; y < to; y++ {
			if (y-from)%6 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			indexer.fill(bounds.Min.Y+y, q.pix[y*width:(y+1)*width])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return q, nil
}

// Bounds returns the bounds of the quantized image.
func (q *Quantized) Bounds() image.Rectangle { return q.rect }

// minParallelPixels keeps small crops on the calling goroutine, where the
// cost of starting workers would exceed the work.
const minParallelPixels = 1 << 16

// parallelWorkers leaves one processor free, so encoding in the background
// never takes every processor from an interactive goroutine.
func parallelWorkers() int { return max(1, runtime.GOMAXPROCS(0)-1) }

// AppendEncode appends the Sixel data for r ∩ Bounds() to dst: raster
// attributes, the palette entries that area uses, and its bands. Like
// EncodeContext it omits the DCS introducer and terminator. Independent bands
// are encoded in parallel. On error dst is returned unchanged.
func (q *Quantized) AppendEncode(ctx context.Context, dst []byte, r image.Rectangle) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	r = r.Intersect(q.rect)
	width, height := r.Dx(), r.Dy()
	out := append(dst, RasterAttribute, '1', ';', '1', ';')
	out = strconv.AppendInt(out, int64(width), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(height), 10)
	if r.Empty() {
		return append(out, LineBreak), nil
	}

	stride := q.rect.Dx()
	row := func(y int) []uint8 {
		i := (r.Min.Y+y-q.rect.Min.Y)*stride + r.Min.X - q.rect.Min.X
		return q.pix[i : i+width : i+width]
	}
	// Define only the entries this crop shows; a small crop of a photo uses
	// a fraction of the palette.
	var used [MaxColors]bool
	for y := range height {
		for _, index := range row(y) {
			used[index] = true
		}
	}
	for i, c := range q.palette {
		if used[i] {
			out = append(out, ColorIntroducer)
			out = strconv.AppendInt(out, int64(i), 10)
			out = append(out, ';', '2', ';')
			out = strconv.AppendInt(out, int64(c.Red), 10)
			out = append(out, ';')
			out = strconv.AppendInt(out, int64(c.Green), 10)
			out = append(out, ';')
			out = strconv.AppendInt(out, int64(c.Blue), 10)
		}
	}

	bands := (height + 5) / 6
	parts := make([][]byte, min(bands, parallelWorkers(), max(1, width*height/minParallelPixels)))
	err := parallelRows(ctx, bands, len(parts), func(chunk, from, to int) error {
		enc := newSixelBands(width, q.palette)
		var rows [6][]uint8
		var part []byte
		if len(parts) == 1 {
			part = out // encode in place when there is a single part
		}
		for band := from; band < to; band++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := 0
			for y := band * 6; n < 6 && y < height; n, y = n+1, y+1 {
				rows[n] = row(y)
			}
			part = enc.append(part, rows[:n])
		}
		parts[chunk] = part
		return nil
	})
	if err != nil {
		return dst, err
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	for _, part := range parts {
		out = append(out, part...)
	}
	return out, nil
}

// parallelRows splits n items into at most workers contiguous chunks and
// runs them concurrently. Chunk i covers [i*n/workers, (i+1)*n/workers).
func parallelRows(ctx context.Context, n, workers int, run func(chunk, from, to int) error) error {
	workers = max(1, min(workers, n))
	if workers == 1 {
		if n == 0 {
			return ctx.Err()
		}
		return run(0, 0, n)
	}
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = run(i, i*n/workers, (i+1)*n/workers)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}
