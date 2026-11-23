package sixel

import (
	"container/heap"
	"context"
	"image"
	"math"
)

// sixelPalette is a palette of up to 256 colors that lists the colors that will be used by
// a SixelImage. Most images, especially jpegs, have more than 256 colors, so creating a sixelPalette
// requires the use of color quantization. For this we use the Median Cut algorithm.
//
// Median cut requires all pixels in an image to be positioned in a 4D color cube, with one axis per channel.
// The cube is sliced in half along its longest axis such that half the pixels in the cube end up in one of
// the sub-cubes and half end up in the other. We continue slicing the cube with the longest axis in half
// along that axis until there are 256 sub-cubes.  Then, the average of all pixels in each subcube is used
// as that cube's color.
//
// Colors are converted to palette colors based on which they are closest to (it's not
// always their cube's color).
//
// This implementation has a few minor (but seemingly very common) differences from the Official algorithm:
//   - When determining the longest axis, the number of pixels in the cube are multiplied against axis length
//     This improves color selection by quite a bit in cases where an image has a lot of space taken up by different
//     shades of the same color.
//   - If a single color sits on a cut line, all pixels of that color are assigned to one of the subcubes
//     rather than try to split them up between the subcubes.  This allows us to use a slice of unique colors
//     with a pixel count per color rather than trying to represent each pixel individually.
type sixelPalette struct {
	// index maps an image color to its palette entry. Diffusion selects
	// entries from the dithered value instead and leaves it nil.
	index         *sixelColorIndex
	colors        []weightedSixelColor // keys to clear when index is released
	PaletteColors []sixelColor
}

// lookup returns the palette entry for an exact image color.
func (p sixelPalette) lookup(c sixelColor) (uint8, bool) {
	if p.index == nil {
		return 0, false
	}
	v, ok := p.index.get(c)
	return uint8(v), ok //nolint:gosec // palette entries are below MaxColors
}

// release returns the pooled lookup table. The palette colors remain valid.
func (p *sixelPalette) release() {
	p.index.release(p.colors)
	p.index, p.colors = nil, nil
}

type weightedSixelColor struct {
	sixelColor
	count uint64
}

// quantizationChannel is an enum type which indicates an axis in the color cube. Used to indicate which
// axis in a cube is the longest.
type quantizationChannel int

const (
	// MaxColors is the maximum number of colors a sixelPalette can contain.
	MaxColors int = 256

	quantizationRed quantizationChannel = iota
	quantizationGreen
	quantizationBlue
	quantizationAlpha
)

// quantizationCube represents a single cube in the median cut algorithm.
type quantizationCube struct {
	// startIndex is the index in the uniqueColors slice where this cube starts
	startIndex int
	// length is the number of elements in the uniqueColors slice this cube occupies
	length int
	// sliceChannel is the axis that will be cut if this cube is cut in half
	sliceChannel quantizationChannel
	// score is a heuristic value: higher means this cube is more likely to be cut
	score uint64
	// pixelCount is how many pixels are contained in this cube
	pixelCount uint64
}

// cubePriorityQueue is a heap used to sort quantizationCube objects in order to select the correct
// one to cut next. Pop will remove the queue with the highest score.
type cubePriorityQueue []any

func (p *cubePriorityQueue) Push(x any) {
	*p = append(*p, x)
}

func (p *cubePriorityQueue) Pop() any {
	popped := (*p)[len(*p)-1]
	*p = (*p)[:len(*p)-1]
	return popped
}

func (p *cubePriorityQueue) Len() int {
	return len(*p)
}

func (p *cubePriorityQueue) Less(i, j int) bool {
	left := (*p)[i].(quantizationCube)
	right := (*p)[j].(quantizationCube)

	// We want the largest channel variance
	return left.score > right.score
}

func (p *cubePriorityQueue) Swap(i, j int) {
	(*p)[i], (*p)[j] = (*p)[j], (*p)[i]
}

// createCube is used to initialize a new quantizationCube containing a region of the uniqueColors slice.
func (p *sixelPalette) createCube(uniqueColors []weightedSixelColor, startIndex, bucketLength int) quantizationCube {
	minRed, minGreen, minBlue, minAlpha := uint8(0xff), uint8(0xff), uint8(0xff), uint8(0xff)
	var maxRed, maxGreen, maxBlue, maxAlpha uint8
	totalWeight := uint64(0)

	// Figure out which channel has the greatest variance
	for i := startIndex; i < startIndex+bucketLength; i++ {
		c := uniqueColors[i]
		r, g, b, a := c.Red, c.Green, c.Blue, c.Alpha
		totalWeight += c.count

		if r < minRed {
			minRed = r
		}
		if r > maxRed {
			maxRed = r
		}
		if g < minGreen {
			minGreen = g
		}
		if g > maxGreen {
			maxGreen = g
		}
		if b < minBlue {
			minBlue = b
		}
		if b > maxBlue {
			maxBlue = b
		}
		if a < minAlpha {
			minAlpha = a
		}
		if a > maxAlpha {
			maxAlpha = a
		}
	}

	dRed := maxRed - minRed
	dGreen := maxGreen - minGreen
	dBlue := maxBlue - minBlue
	dAlpha := maxAlpha - minAlpha

	cube := quantizationCube{
		startIndex: startIndex,
		length:     bucketLength,
		pixelCount: totalWeight,
	}

	if dRed >= dGreen && dRed >= dBlue && dRed >= dAlpha {
		cube.sliceChannel = quantizationRed
		cube.score = uint64(dRed)
	} else if dGreen >= dBlue && dGreen >= dAlpha {
		cube.sliceChannel = quantizationGreen
		cube.score = uint64(dGreen)
	} else if dBlue >= dAlpha {
		cube.sliceChannel = quantizationBlue
		cube.score = uint64(dBlue)
	} else {
		cube.sliceChannel = quantizationAlpha
		cube.score = uint64(dAlpha)
	}

	// Boost the score of cubes with more pixels in them
	cube.score *= totalWeight

	return cube
}

// quantize is a method that will initialize the palette's colors and lookups, provided a set
// of unique colors and a map containing pixel counts for those colors.
func (p *sixelPalette) quantize(ctx context.Context, uniqueColors []weightedSixelColor, maxColors int) error {
	// We don't need to quantize if we don't even have more than the maximum colors, and in fact, this code will explode
	// if we have fewer than maximum colors
	if len(uniqueColors) <= maxColors {
		for _, c := range uniqueColors {
			p.PaletteColors = append(p.PaletteColors, c.sixelColor)
		}
		return ctx.Err()
	}

	cubeHeap := make(cubePriorityQueue, 0, maxColors)
	scratch := make([]weightedSixelColor, len(uniqueColors))

	// Start with a cube that contains all colors
	heap.Init(&cubeHeap)
	heap.Push(&cubeHeap, p.createCube(uniqueColors, 0, len(uniqueColors)))

	// Slice the best cube into two cubes until we have max colors, then we have our palette
	for cubeHeap.Len() < maxColors {
		if err := ctx.Err(); err != nil {
			return err
		}
		cubeToSplit := heap.Pop(&cubeHeap).(quantizationCube)

		sortByChannel(uniqueColors[cubeToSplit.startIndex:cubeToSplit.startIndex+cubeToSplit.length], scratch,
			cubeToSplit.sliceChannel)

		// We need to split up the colors in this cube so that the pixels are evenly split between the two,
		// or at least as close as we can reasonably get. What we do is count up the pixels as we go through
		// and place the cut point where around half of the pixels are on the left side
		countSoFar := uniqueColors[cubeToSplit.startIndex].count
		targetCount := cubeToSplit.pixelCount / 2
		leftLength := 1

		for i := cubeToSplit.startIndex + 1; i < cubeToSplit.startIndex+cubeToSplit.length; i++ {
			weight := uniqueColors[i].count
			if countSoFar+weight > targetCount {
				break
			}
			leftLength++
			countSoFar += weight
		}

		rightLength := cubeToSplit.length - leftLength
		rightIndex := cubeToSplit.startIndex + leftLength
		heap.Push(&cubeHeap, p.createCube(uniqueColors, cubeToSplit.startIndex, leftLength))
		heap.Push(&cubeHeap, p.createCube(uniqueColors, rightIndex, rightLength))
	}

	// Once we've got max cubes in the heap, pull them all out and load them into the palette
	for cubeHeap.Len() > 0 {
		bucketToLoad := heap.Pop(&cubeHeap).(quantizationCube)
		p.loadColor(uniqueColors, bucketToLoad.startIndex, bucketToLoad.length)
	}
	return ctx.Err()
}

// sortByChannel orders colors by one channel with a stable counting sort.
// Sixel channels have 101 levels, so this is linear in the number of colors,
// where a comparison sort of a photo's colors dominated palette time.
func sortByChannel(colors, scratch []weightedSixelColor, channel quantizationChannel) {
	key := func(c sixelColor) uint8 {
		switch channel { //nolint:exhaustive // alpha is the remaining channel
		case quantizationRed:
			return c.Red
		case quantizationGreen:
			return c.Green
		case quantizationBlue:
			return c.Blue
		default:
			return c.Alpha
		}
	}
	var starts [257]int
	for _, c := range colors {
		starts[int(key(c.sixelColor))+1]++
	}
	for i := 1; i < len(starts); i++ {
		starts[i] += starts[i-1]
	}
	scratch = scratch[:len(colors)]
	for _, c := range colors {
		k := key(c.sixelColor)
		scratch[starts[k]] = c
		starts[k]++
	}
	copy(colors, scratch)
}

// loadColor accepts a range of colors representing a single median cut cube. It calculates the
// average color in the cube and adds it to the palette.
func (p *sixelPalette) loadColor(uniqueColors []weightedSixelColor, startIndex, cubeLen int) {
	totalRed, totalGreen, totalBlue, totalAlpha := uint64(0), uint64(0), uint64(0), uint64(0)
	totalCount := uint64(0)
	for i := startIndex; i < startIndex+cubeLen; i++ {
		c := uniqueColors[i]
		count := c.count
		totalRed += uint64(c.Red) * count
		totalGreen += uint64(c.Green) * count
		totalBlue += uint64(c.Blue) * count
		totalAlpha += uint64(c.Alpha) * count
		totalCount += count
	}

	averageColor := sixelColor{
		Red:   uint8(totalRed / totalCount),   //nolint:gosec
		Green: uint8(totalGreen / totalCount), //nolint:gosec
		Blue:  uint8(totalBlue / totalCount),  //nolint:gosec
		Alpha: uint8(totalAlpha / totalCount), //nolint:gosec
	}

	p.PaletteColors = append(p.PaletteColors, averageColor)
}

// sixelColor is a flat struct that contains a single color: all channels are 0-100
// instead of anything sensible.
type sixelColor struct {
	Red   uint8
	Green uint8
	Blue  uint8
	Alpha uint8
}

// sixelConvertColor converts premultiplied 16-bit RGBA to Sixel's 0-100 channels.
func sixelConvertColor(r, g, b, a uint32) sixelColor {
	return sixelColor{
		Red:   uint8(ConvertChannel(r)), //nolint:gosec // 0-100
		Green: uint8(ConvertChannel(g)), //nolint:gosec // 0-100
		Blue:  uint8(ConvertChannel(b)), //nolint:gosec // 0-100
		Alpha: uint8(ConvertChannel(a)), //nolint:gosec // 0-100
	}
}

func readSixelRGBA(img image.Image) func(int, int) (uint32, uint32, uint32, uint32) {
	// Reading through At boxes common decoded pixels into a color.Color for
	// every sample. RGBA64At retains the same precision without the allocation.
	if pixels, ok := img.(image.RGBA64Image); ok {
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			return pixels.RGBA64At(x, y).RGBA()
		}
	}
	return func(x, y int) (uint32, uint32, uint32, uint32) { return img.At(x, y).RGBA() }
}

// newSixelPalette accepts an image and produces an N-color quantized color palette using the median cut
// algorithm. Only the non-dithered path needs a reverse lookup for the unmodified source values; the
// caller releases it.
func newSixelPalette(ctx context.Context, img image.Image, maxColors int, dither bool) (sixelPalette, bool, error) {
	index := newSixelColorIndex()
	var uniqueColors []weightedSixelColor
	keep := false
	defer func() {
		if !keep {
			index.release(uniqueColors)
		}
	}()
	opaque := true
	pixels := newSixelPixels(img)
	bounds := img.Bounds()
	row := make([]sixelColor, bounds.Dx())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return sixelPalette{}, false, err
		}
		pixels.colors(y, row)
		last := -1
		for x, c := range row {
			// Runs of one color are common in flat artwork and backgrounds.
			if last >= 0 && c == row[x-1] {
				uniqueColors[last].count++
				continue
			}
			if i, ok := index.get(c); ok {
				last = int(i)
				uniqueColors[i].count++
				continue
			}
			// Stable first-seen order makes median cut's tied splits and
			// the wire payload deterministic across encodes.
			last = len(uniqueColors)
			index.set(c, int32(last)) //nolint:gosec // bounded by the pixel count of a bounded raster
			uniqueColors = append(uniqueColors, weightedSixelColor{c, 1})
			opaque = opaque && c.Alpha == 100
		}
	}

	p := sixelPalette{}
	if err := p.quantize(ctx, uniqueColors, maxColors); err != nil {
		return sixelPalette{}, false, err
	}
	if dither && opaque && len(uniqueColors) > len(p.PaletteColors) {
		return p, true, nil
	}

	// With no more colors than entries, quantize kept first-seen order, so
	// each color's position is already its palette entry.
	if len(uniqueColors) > len(p.PaletteColors) {
		// The average color of a cube is not necessarily the nearest palette color.
		for i, c := range uniqueColors {
			if i%256 == 0 {
				if err := ctx.Err(); err != nil {
					return sixelPalette{}, false, err
				}
			}
			var bestColorIndex int32
			bestScore := uint32(math.MaxUint32)

			for paletteIndex, paletteColor := range p.PaletteColors {
				redDiff := uint32(c.Red) - uint32(paletteColor.Red)
				greenDiff := uint32(c.Green) - uint32(paletteColor.Green)
				blueDiff := uint32(c.Blue) - uint32(paletteColor.Blue)
				alphaDiff := uint32(c.Alpha) - uint32(paletteColor.Alpha)

				score := (redDiff * redDiff) + (greenDiff * greenDiff) + (blueDiff * blueDiff) + (alphaDiff * alphaDiff)
				if score < bestScore {
					bestColorIndex = int32(paletteIndex) //nolint:gosec // at most MaxColors entries
					bestScore = score
				}
			}
			index.set(c.sixelColor, bestColorIndex)
		}
	}
	keep = true
	p.index, p.colors = index, uniqueColors
	return p, false, ctx.Err()
}
