package sixel

// The opacity check in newSixelPalette permits three-channel diffusion.
// Colors and error use 16-bit samples, like image/draw.FloydSteinberg.
type diffusionRGB [3]int32

const diffusionShift = 12 // sixteen buckets per channel

type sixelDiffusion struct {
	palette       []diffusionRGB
	current, next []diffusionRGB
	candidates    [16 * 16 * 16][]uint8
}

func newSixelDiffusion(width int, palette []sixelColor) *sixelDiffusion {
	d := &sixelDiffusion{
		palette: make([]diffusionRGB, len(palette)),
		current: make([]diffusionRGB, width+2),
		next:    make([]diffusionRGB, width+2),
	}
	for i, c := range palette {
		r, g, b, _ := sixelRGB(int(c.Red), int(c.Green), int(c.Blue)).RGBA()
		d.palette[i] = diffusionRGB{int32(r), int32(g), int32(b)}
	}
	return d
}

func (d *sixelDiffusion) pixel(x int, r, g, b uint32) uint8 {
	src := diffusionRGB{int32(r), int32(g), int32(b)}
	for i := range src {
		src[i] = min(65535, max(0, src[i]+d.current[x+1][i]/16))
	}
	index := d.nearest(src)
	selected := d.palette[index]
	// Floyd–Steinberg sends 7/16 right; 3/16, 5/16, 1/16 to the
	// left, center and right pixels on the next row.
	for i, v := range src {
		err := v - selected[i]
		d.current[x+2][i] += 7 * err
		d.next[x][i] += 3 * err
		d.next[x+1][i] += 5 * err
		d.next[x+2][i] += err
	}
	return index
}

func (d *sixelDiffusion) nextRow() {
	d.current, d.next = d.next, d.current
	clear(d.next)
}

func (d *sixelDiffusion) nearest(src diffusionRGB) uint8 {
	r, g, b := src[0]>>diffusionShift, src[1]>>diffusionShift, src[2]>>diffusionShift
	bucket := r<<8 | g<<4 | b
	indices := d.candidates[bucket]
	if indices == nil {
		indices = d.bucketColors(diffusionRGB{r << diffusionShift, g << diffusionShift, b << diffusionShift})
		d.candidates[bucket] = indices
	}
	best, distance := indices[0], ^uint32(0)
	for _, i := range indices {
		if score := diffusionDistance(src, d.palette[i]); score < distance {
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

func (d *sixelDiffusion) bucketColors(lo diffusionRGB) []uint8 {
	const size = 1 << diffusionShift
	center := diffusionRGB{lo[0] + size/2, lo[1] + size/2, lo[2] + size/2}
	anchor, minDistance := d.palette[0], ^uint32(0)
	for _, p := range d.palette {
		if score := diffusionDistance(center, p); score < minDistance {
			anchor, minDistance = p, score
		}
	}

	// The difference of squared distances from p and the anchor is linear
	// in each input component. Find its minimum at the nearest box corner
	// and discard p only if it cannot win or tie anywhere in this box.
	// Allow for the low bits discarded by diffusionDistance as well.
	indices := make([]uint8, 0, 4)
	for i, p := range d.palette {
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
