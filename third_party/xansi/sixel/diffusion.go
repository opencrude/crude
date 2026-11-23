package sixel

import (
	"context"
	"sync"
	"sync/atomic"
)

// The opacity check in newSixelPalette permits three-channel diffusion.
// Colors and error use 16-bit samples, like image/draw.FloydSteinberg.
type diffusionRGB [3]int32

const diffusionShift = 12 // sixteen buckets per channel

// diffusionPalette finds the exact nearest palette entry, as image/draw does.
// Candidate lists per bucket are computed on first use and published
// atomically, so concurrent rows share them without locking; a duplicate
// computation produces the same list.
type diffusionPalette struct {
	colors     []diffusionRGB
	candidates [16 * 16 * 16]atomic.Pointer[[]uint8]
}

func newDiffusionPalette(palette []sixelColor) *diffusionPalette {
	d := &diffusionPalette{colors: make([]diffusionRGB, len(palette))}
	for i, c := range palette {
		r, g, b, _ := sixelRGB(int(c.Red), int(c.Green), int(c.Blue)).RGBA()
		d.colors[i] = diffusionRGB{int32(r), int32(g), int32(b)}
	}
	return d
}

func (d *diffusionPalette) nearest(src diffusionRGB) uint8 {
	r, g, b := src[0]>>diffusionShift, src[1]>>diffusionShift, src[2]>>diffusionShift
	slot := &d.candidates[r<<8|g<<4|b]
	list := slot.Load()
	if list == nil {
		indices := d.bucketColors(diffusionRGB{r << diffusionShift, g << diffusionShift, b << diffusionShift})
		list = &indices
		slot.Store(list)
	}
	indices := *list
	best, distance := indices[0], ^uint32(0)
	for _, i := range indices {
		if score := diffusionDistance(src, d.colors[i]); score < distance {
			best, distance = i, score
			if score == 0 {
				break
			}
		}
	}
	return best
}

func diffusionDistance(a, b diffusionRGB) uint32 {
	// Match image/draw's distance and tie-breaking; shift each square
	// before adding to fit the sum of the three squares in 32 bits.
	dr, dg, db := uint32(a[0]-b[0]), uint32(a[1]-b[1]), uint32(a[2]-b[2])
	return dr*dr>>2 + dg*dg>>2 + db*db>>2
}

func (d *diffusionPalette) bucketColors(lo diffusionRGB) []uint8 {
	const size = 1 << diffusionShift
	center := diffusionRGB{lo[0] + size/2, lo[1] + size/2, lo[2] + size/2}
	anchor, minDistance := d.colors[0], ^uint32(0)
	for _, p := range d.colors {
		if score := diffusionDistance(center, p); score < minDistance {
			anchor, minDistance = p, score
		}
	}

	// The difference of squared distances from p and the anchor is linear
	// in each input component. Find its minimum at the nearest box corner
	// and discard p only if it cannot win or tie anywhere in this box.
	// Allow for the low bits discarded by diffusionDistance as well.
	indices := make([]uint8, 0, 4)
	for i, p := range d.colors {
		var minDifference int64
		for c, v := range p {
			q := lo[c]
			if v > anchor[c] {
				q += size - 1
			}
			minDifference += int64(v-anchor[c]) * int64(v+anchor[c]-2*q)
		}
		if minDifference <= 9 {
			indices = append(indices, uint8(i))
		}
	}
	return indices
}

// diffusionChunk is the column granularity at which a row reports progress to
// the row below it.
const diffusionChunk = 64

// diffuse assigns Floyd–Steinberg palette entries to every pixel of an opaque
// image, bit for bit as a single sequential pass would.
//
// Error diffusion looks sequential, but a pixel depends only on its left
// neighbor and on the three pixels above it (up-left, up, up-right). Row y can
// therefore process column x as soon as row y-1 has finished column x+1. Rows
// run concurrently as a skewed wavefront: each starts a chunk once the row
// above has finished the next chunk. Every pixel sees exactly the same inputs
// as in the sequential order, so the result is identical.
func diffuse(ctx context.Context, img sixelPixels, palette *diffusionPalette, pix []uint8) error {
	width, height := img.bounds.Dx(), img.bounds.Dy()
	if width == 0 || height == 0 {
		return ctx.Err()
	}
	chunks := (width + diffusionChunk - 1) / diffusionChunk
	workers := max(1, min(parallelWorkers(), height, width*height/minParallelPixels))
	// Worker w owns rows w, w+workers, ... in order. With one more slot than
	// workers, the slot a row reuses belonged to rows that its own worker
	// has already finished, so reuse never races.
	slots := workers + 1
	errs := make([][]diffusionRGB, slots)
	done := make([]chan struct{}, slots)
	for i := range slots {
		errs[i] = make([]diffusionRGB, width+2)
		done[i] = make(chan struct{}, chunks)
	}
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var row []uint8
			for y := w; y < height; y += workers {
				// Error from the row above, final up to the chunks it reported.
				above, below := errs[y%slots], errs[(y+1)%slots]
				clear(below)
				var wait chan struct{}
				if y > 0 {
					wait = done[(y-1)%slots]
				}
				report := done[y%slots]
				skip := ctx.Err() != nil // the caller discards a cancelled result
				row = pix[y*width : (y+1)*width]
				ready := 0
				var right diffusionRGB // 7/16 carried to the next pixel
				for k := range chunks {
					for need := min(k+2, chunks); wait != nil && ready < need; ready++ {
						<-wait
					}
					if !skip {
						from, to := k*diffusionChunk, min(width, (k+1)*diffusionChunk)
						right = diffuseSpan(img, palette, y, from, to, above, below, right, row)
					}
					// Report even after cancellation so no row waits forever.
					report <- struct{}{}
				}
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// diffuseSpan processes columns [from, to) of row y. It reads only the final
// error of the row above and writes only the error for the row below, which
// the row below does not read until this span is reported.
func diffuseSpan(img sixelPixels, palette *diffusionPalette, y, from, to int,
	above, below []diffusionRGB, right diffusionRGB, row []uint8,
) diffusionRGB {
	rgba := img.rgba
	offset := 0
	if rgba != nil {
		offset = rgba.PixOffset(img.bounds.Min.X+from, img.bounds.Min.Y+y)
	}
	for x := from; x < to; x++ {
		var src diffusionRGB
		if rgba != nil {
			s := rgba.Pix[offset : offset+3 : offset+3]
			src = diffusionRGB{int32(s[0]) * 0x101, int32(s[1]) * 0x101, int32(s[2]) * 0x101}
			offset += 4
		} else {
			r, g, b, _ := img.read(img.bounds.Min.X+x, img.bounds.Min.Y+y)
			src = diffusionRGB{int32(r), int32(g), int32(b)}
		}
		for i := range src {
			src[i] = min(65535, max(0, src[i]+(above[x+1][i]+right[i])/16))
		}
		index := palette.nearest(src)
		row[x] = index
		selected := palette.colors[index]
		// Floyd–Steinberg sends 7/16 right; 3/16, 5/16, 1/16 to the
		// left, center and right pixels on the next row.
		for i, v := range src {
			err := v - selected[i]
			right[i] = 7 * err
			below[x][i] += 3 * err
			below[x+1][i] += 5 * err
			below[x+2][i] += err
		}
	}
	return right
}
