package tea

import (
	"context"
	"image"
	"image/color"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/ansi/sixel"
	"golang.org/x/image/draw"
)

// MaxSixelPixels bounds the visible raster, not the source or virtual zoom grid.
// It bounds resampling, palette construction and the encoded payload.
const MaxSixelPixels = 2 * 1024 * 1024

// SixelSizeSupported checks visible raster bounds without multiplying unchecked
// dimensions. Unknown cell geometry must keep the ordinary cell preview.
func SixelSizeSupported(columns, rows, cellWidth, cellHeight int) bool {
	return columns > 0 && rows > 0 && cellWidth > 0 && cellWidth <= 1024 &&
		cellHeight > 0 && cellHeight <= 1024 &&
		columns <= MaxSixelPixels/cellWidth && rows <= MaxSixelPixels/cellHeight &&
		columns*cellWidth <= MaxSixelPixels/(rows*cellHeight)
}

// A tile is derived ONLY from surviving compositor cells. It is not a second
// layout or z-order authority. Rectangle is in screen cells; column/row locate
// its top-left in the placement's image grid.
type sixelTile struct {
	placement   ImagePlacement
	rect        image.Rectangle
	column, row int
	background  uv.Color
}

// sixelBaseKey identifies the pixels of one placement composed over one
// background. Screen position and occlusion are not part of it: moving a pane
// or opening a menu over it never prepares the image again. It names the
// resource by ID, so a cached raster never keeps decoded source pixels alive.
type sixelBaseKey struct {
	image                 uint32
	columns, rows         int
	cellWidth, cellHeight int
	source                image.Rectangle
	virtual, origin       image.Point
	background            uv.Color
}

func (t sixelTile) key() sixelBaseKey {
	p := t.placement
	virtual, origin := p.virtualGrid()
	return sixelBaseKey{p.Image.ID(), p.Columns, p.Rows, p.CellWidth, p.CellHeight,
		image.Rect(p.SourceX, p.SourceY, p.SourceX+p.SourceWidth, p.SourceY+p.SourceHeight),
		virtual, origin, t.background}
}

// grid is the tile's rectangle in placement cells.
func (t sixelTile) grid() image.Rectangle {
	return image.Rect(t.column, t.row, t.column+t.rect.Dx(), t.row+t.rect.Dy())
}

// sixelBase is one quantized raster for a rectangle of placement cells. Every
// visible tile of the placement is a crop of it, so tiles share one palette
// and one dither pattern and join without seams. A nil raster means the area
// exceeds the Sixel bounds; its cells keep the composed text.
type sixelBase struct {
	id     uint64
	key    sixelBaseKey
	grid   image.Rectangle
	raster *sixel.Quantized
	full   string // framed sequence for the whole grid
	used   uint64 // last prepare call that painted from it
}

func (b *sixelBase) bytes() int {
	if b.raster == nil {
		return 0
	}
	return b.raster.Bounds().Dx()*b.raster.Bounds().Dy() + len(b.full)
}

// sixelWant is an area to prepare. Only a want refers to the resource itself;
// prepared rasters keep its key.
type sixelWant struct {
	placement  ImagePlacement
	background uv.Color
	grid       image.Rectangle
}

func (w sixelWant) key() sixelBaseKey {
	return sixelTile{placement: w.placement, background: w.background}.key()
}

// sixelCell is what one terminal cell shows: a cell of a prepared raster, or
// nothing (base 0). sixelPending marks an image cell whose raster is not ready.
type sixelCell struct {
	base        uint64
	column, row int32
}

const sixelPending = ^uint64(0)

// sixelCellForeground replaces the image identity on consumed marker cells.
// One constant keeps the cells non-blank, so UV's blank-bottom erase cannot span
// an image, and lets a pane that switches images keep identical text cells:
// the old pixels stay until the new raster replaces them.
var sixelCellForeground = uv.RGBColor(1, 2, 3)

var imageDiacritics = func() map[rune]int {
	result := make(map[rune]int, 297)
	for i := range 297 {
		result[kitty.Diacritic(i)] = i
	}
	return result
}()

// These are our own exact four-rune cell markers, not a terminal byte parser.
// VT response parsing and Sixel encoding stay in UV and x/ansi respectively.
func imageCoordinates(c *uv.Cell, p ImagePlacement) (column, row int, ok bool) {
	if c == nil || c.Width != 1 || c.Style.Fg != uv.RGBColor(uint8(p.Image.id>>16), uint8(p.Image.id>>8), uint8(p.Image.id)) {
		return 0, 0, false
	}
	text := c.Content
	var runes [4]rune
	for i := range runes {
		r, n := utf8.DecodeRuneInString(text)
		if n == 0 {
			return 0, 0, false
		}
		runes[i], text = r, text[n:]
	}
	if text != "" || runes[0] != kitty.Placeholder || runes[3] != kitty.Diacritic(int(p.Image.id>>24)) {
		return 0, 0, false
	}
	row, rok := imageDiacritics[runes[1]]
	column, cok := imageDiacritics[runes[2]]
	return column, row, rok && cok && column < p.Columns && row < p.Rows
}

// snapshotSixelTiles runs during the existing synchronous cell handoff. It only
// reads cell metadata, never pixels. The detached text snapshot contains spaces,
// so a terminal without Kitty support never receives placeholder glyphs.
func snapshotSixelTiles(buf *uv.Buffer, placements []ImagePlacement) []sixelTile {
	var tiles []sixelTile
	for _, p := range placements {
		if p.Protocol != ImageSixel || !p.valid() || p.Image.pixels == nil {
			continue
		}
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); {
				column, row, ok := imageCoordinates(buf.CellAt(x, y), p)
				if !ok {
					x++
					continue
				}
				bg := buf.CellAt(x, y).Style.Bg
				start := x
				for x < buf.Width() {
					col, r, match := imageCoordinates(buf.CellAt(x, y), p)
					if !match || r != row || col != column+x-start || buf.CellAt(x, y).Style.Bg != bg {
						break
					}
					cell := buf.CellAt(x, y)
					cell.Content = " "
					cell.Style.Fg = sixelCellForeground
					x++
				}
				tile := sixelTile{placement: p, rect: image.Rect(start, y, x, y+1), column: column, row: row, background: bg}
				merged := false
				for i := len(tiles) - 1; i >= 0; i-- {
					previous := &tiles[i]
					if previous.placement == p && previous.rect.Min.X == start && previous.rect.Max.X == x &&
						previous.rect.Max.Y == y && previous.column == column &&
						previous.row+previous.rect.Dy() == row && previous.background == bg {
						previous.rect.Max.Y++
						merged = true
						break
					}
				}
				if !merged {
					tiles = append(tiles, tile)
				}
			}
		}
	}
	return tiles
}

// resampleSixelBase scales the source into the base grid, flattened over its
// background. Horizontal stripes are independent, so they resample in
// parallel; each stripe computes exactly the pixels a single pass would.
func resampleSixelBase(ctx context.Context, want sixelWant) (*image.RGBA, error) {
	p := want.placement
	src := p.Image.pixels
	bounds := src.Bounds()
	if p.hasSourceRect() {
		bounds = image.Rect(bounds.Min.X+p.SourceX, bounds.Min.Y+p.SourceY,
			bounds.Min.X+p.SourceX+p.SourceWidth, bounds.Min.Y+p.SourceY+p.SourceHeight).Intersect(bounds)
	}
	w, h := want.grid.Dx()*p.CellWidth, want.grid.Dy()*p.CellHeight
	pixels := image.NewRGBA(image.Rect(0, 0, w, h))
	r, g, b, _ := want.background.RGBA()
	background := image.NewUniform(color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255})
	var target image.Rectangle
	if !bounds.Empty() {
		// Scale directly into the visible crop. Never allocate the virtual
		// zoomed image. The resampler preserves sub-cell pixels and alpha.
		virtual, offset := p.virtualGrid()
		scale := math.Min(float64(virtual.X)*float64(p.CellWidth)/float64(bounds.Dx()),
			float64(virtual.Y)*float64(p.CellHeight)/float64(bounds.Dy()))
		origin := image.Pt(-(offset.X+want.grid.Min.X)*p.CellWidth, -(offset.Y+want.grid.Min.Y)*p.CellHeight)
		target = image.Rectangle{Min: origin, Max: origin.Add(image.Pt(
			max(1, int(math.Round(float64(bounds.Dx())*scale))),
			max(1, int(math.Round(float64(bounds.Dy())*scale)))))}
	}
	// Leave one processor for the UI, as the encoder does.
	stripes := max(1, min(runtime.GOMAXPROCS(0)-1, h/16, w*h/(1<<15)))
	var wg sync.WaitGroup
	for i := range stripes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			stripe := pixels.SubImage(image.Rect(0, i*h/stripes, w, (i+1)*h/stripes)).(*image.RGBA)
			draw.Draw(stripe, stripe.Bounds(), background, image.Point{}, draw.Src)
			if !target.Empty() {
				draw.ApproxBiLinear.Scale(stripe, target, src, bounds, draw.Over, nil)
			}
		}()
	}
	wg.Wait()
	return pixels, ctx.Err()
}

// frameSixel wraps Sixel data for the single terminal writer. x/ansi ends every
// raster with a graphics newline. No pixels follow the final one; omit it so
// an image at the bottom cannot scroll the text grid.
func frameSixel(data []byte, tmux bool) string {
	if n := len(data); n > 0 && data[n-1] == sixel.LineBreak {
		data = data[:n-1]
	}
	sequence := ansi.SaveCursor + ansi.SixelGraphics(0, 1, 0, data) + ansi.RestoreCursor
	if tmux {
		sequence = ansi.TmuxPassthrough(sequence)
	}
	return sequence
}

// encodeSixelCrop frames the Sixel data for a rectangle of base cells.
func encodeSixelCrop(ctx context.Context, base *sixelBase, grid image.Rectangle, tmux bool) (string, error) {
	cw, ch := base.key.cellWidth, base.key.cellHeight
	grid = grid.Sub(base.grid.Min)
	pixels := image.Rect(grid.Min.X*cw, grid.Min.Y*ch, grid.Max.X*cw, grid.Max.Y*ch)
	data, err := base.raster.AppendEncode(ctx, nil, pixels)
	if err != nil {
		return "", err
	}
	return frameSixel(data, tmux), nil
}

func prepareSixelBase(ctx context.Context, want sixelWant, tmux bool) (*sixelBase, error) {
	base := &sixelBase{key: want.key(), grid: want.grid}
	p := want.placement
	if !SixelSizeSupported(want.grid.Dx(), want.grid.Dy(), p.CellWidth, p.CellHeight) {
		return base, ctx.Err()
	}
	pixels, err := resampleSixelBase(ctx, want)
	if err != nil {
		return nil, err
	}
	if base.raster, err = (&sixel.Encoder{Dither: true}).Quantize(ctx, pixels); err != nil {
		return nil, err
	}
	base.full, err = encodeSixelCrop(ctx, base, want.grid, tmux)
	if err != nil {
		return nil, err
	}
	return base, nil
}

// Recently shown rasters stay cached so returning to an image (a tab switch,
// closing a pane above it) paints without preparing it again. A cached raster
// holds only palette entries and its encoded sequence, never source pixels.
const (
	sixelCacheBytes = 32 << 20
	sixelCacheItems = 16
)

// sixelWork prepares rasters for the latest writer-clipped tiles. It has no
// terminal access. One raster is prepared at a time: first every visible area
// without a raster, then, for a zoomed image, a larger area around it so that
// panning crops the same raster instead of preparing it again. A result is
// admitted only while its placement is still wanted, so a superseded crop can
// never reach the terminal.
type sixelWork struct {
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	changed      chan struct{}
	wanted       []sixelWant
	scratch      []sixelWant
	bases        []*sixelBase // cache, bounded by sixelCacheBytes and sixelCacheItems
	active       sixelWant
	cancelActive context.CancelFunc
	nextID       uint64
	clock        uint64 // prepare calls, for least-recently-used eviction
	revision     atomic.Uint64
}

func newSixelWork(wake func(), tmux bool) *sixelWork {
	ctx, cancel := context.WithCancel(context.Background())
	w := &sixelWork{
		cancel:  cancel,
		done:    make(chan struct{}),
		changed: make(chan struct{}, 1),
	}
	go w.run(ctx, wake, tmux)
	return w
}

func (w *sixelWork) run(ctx context.Context, wake func(), tmux bool) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.changed:
		}
		for ctx.Err() == nil {
			w.mu.Lock()
			job, ok := w.nextJob()
			if !ok {
				w.mu.Unlock()
				break
			}
			task, cancel := context.WithCancel(ctx)
			w.active, w.cancelActive = job, cancel
			w.mu.Unlock()

			base, err := prepareSixelBase(task, job, tmux)

			w.mu.Lock()
			admitted := err == nil && task.Err() == nil
			if admitted {
				w.nextID++
				base.id = w.nextID
				w.bases = append(w.bases, base)
				w.evict()
				w.revision.Add(1)
			}
			cancel()
			w.active, w.cancelActive = sixelWant{}, nil
			w.mu.Unlock()
			if admitted && wake != nil {
				wake()
			}
		}
	}
}

// nextJob returns the first visible area no raster covers, then a panning
// margin for an area whose only raster has none. Panning inside a margin
// therefore prepares nothing.
func (w *sixelWork) nextJob() (sixelWant, bool) {
	for _, want := range w.wanted {
		if w.covering(want.key(), want.grid) == nil {
			return want, true
		}
	}
	for _, want := range w.wanted {
		key := want.key()
		if base := w.covering(key, want.grid); base.grid != want.grid {
			continue
		}
		if grid := expandedSixelGrid(want); grid != want.grid && w.covering(key, grid) == nil {
			want.grid = grid
			return want, true
		}
	}
	return sixelWant{}, false
}

// expandedSixelGrid grows a zoomed image's visible area by a margin, so a pan
// crops the same raster. The margin shrinks until the area fits the raster cap.
func expandedSixelGrid(want sixelWant) image.Rectangle {
	p := want.placement
	full := image.Rect(0, 0, p.Columns, p.Rows)
	for margin := max(want.grid.Dx(), want.grid.Dy()); margin > 0; margin /= 2 {
		grid := want.grid.Inset(-margin).Intersect(full)
		if SixelSizeSupported(grid.Dx(), grid.Dy(), p.CellWidth, p.CellHeight) {
			return grid
		}
	}
	return want.grid
}

// covering returns the raster to paint grid from. A placement keeps painting
// from the raster it used last while that still covers it: switching rasters
// changes the dither pattern, which would be a visible shimmer.
func (w *sixelWork) covering(key sixelBaseKey, grid image.Rectangle) *sixelBase {
	var found *sixelBase
	for _, base := range w.bases {
		if base.key != key || !grid.In(base.grid) {
			continue
		}
		if found == nil || base.used > found.used ||
			base.used == found.used && base.id > found.id {
			found = base
		}
	}
	return found
}

// known reports whether a raster of this resource was prepared recently.
func (w *sixelWork) known(image uint32) bool {
	for _, base := range w.bases {
		if base.key.image == image {
			return true
		}
	}
	return false
}

// evict drops least recently used rasters over the cache bounds. Rasters
// painted by the latest prepare call are never evicted.
func (w *sixelWork) evict() {
	total := 0
	for _, base := range w.bases {
		total += base.bytes()
	}
	for len(w.bases) > 1 && (total > sixelCacheBytes || len(w.bases) > sixelCacheItems) {
		oldest := -1
		for i, base := range w.bases {
			if base.used != w.clock && (oldest < 0 || base.used < w.bases[oldest].used ||
				base.used == w.bases[oldest].used && base.id < w.bases[oldest].id) {
				oldest = i
			}
		}
		if oldest < 0 {
			return
		}
		total -= w.bases[oldest].bytes()
		w.bases = slices.Delete(w.bases, oldest, oldest+1)
	}
}

// sixelWants groups tiles by placement. Each want covers the bounding grid of
// that placement's visible tiles, so occlusion splits share one raster.
func sixelWants(dst []sixelWant, tiles []sixelTile) []sixelWant {
	dst = dst[:0]
	for _, tile := range tiles {
		grid := tile.grid()
		found := false
		for i := range dst {
			if dst[i].placement == tile.placement && dst[i].background == tile.background {
				dst[i].grid = dst[i].grid.Union(grid)
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, sixelWant{tile.placement, tile.background, grid})
		}
	}
	return dst
}

// sixelPrepared is the raster for one tile, nil while it is pending. Known
// reports whether the tile's image was shown recently: only then may pending
// cells keep what they show.
type sixelPrepared struct {
	base  *sixelBase
	known bool
}

// prepare publishes the latest tiles and returns the raster for each tile.
// Steady frames allocate nothing.
func (w *sixelWork) prepare(tiles []sixelTile, out []sixelPrepared) ([]sixelPrepared, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clock++
	w.scratch = sixelWants(w.scratch, tiles)
	if !slices.Equal(w.scratch, w.wanted) {
		w.wanted = append(w.wanted[:0], w.scratch...)
		if w.cancelActive != nil {
			useful := false
			key := w.active.key()
			for _, want := range w.wanted {
				useful = useful || want.key() == key && want.grid.In(w.active.grid) && w.covering(key, w.active.grid) == nil
			}
			if !useful {
				w.cancelActive()
			}
		}
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}
	// A placement paints from one raster that covers all its visible tiles,
	// never from a mix of an old raster and a new one: mixed quantizations
	// show seams and would send the same cells twice.
	out = out[:0]
	for _, tile := range tiles {
		prepared := sixelPrepared{known: true}
		for _, want := range w.wanted {
			if want.placement == tile.placement && want.background == tile.background {
				prepared.base = w.covering(want.key(), want.grid)
				break
			}
		}
		if prepared.base != nil {
			prepared.base.used = w.clock
		} else {
			prepared.known = w.known(tile.placement.Image.ID())
		}
		out = append(out, prepared)
	}
	return out, w.revision.Load()
}

func (s *cursedRenderer) invalidateSixelArea(rect image.Rectangle) {
	rect = rect.Intersect(s.cellbuf.Bounds())
	s.scr.InvalidateArea(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		s.cellbuf.TouchLine(rect.Min.X, y, rect.Dx())
	}
}

// planSixels runs before the text diff. It records which raster each cell
// should show and repaints the text of cells whose pixels are no longer
// wanted. A cell whose replacement raster is still pending keeps whatever it
// shows, pixels or text (paint holding): the image never blinks to blank
// while a new zoom, crop or image is prepared, and a motion preview stays
// until the native raster replaces it. It reports whether the text diff must
// record physical damage.
func (s *cursedRenderer) planSixels(tiles []sixelTile) bool {
	width, height := s.cellbuf.Width(), s.cellbuf.Height()
	n := width * height
	if len(s.sixelShown) != n || s.sixelInvalid {
		// Resize, reset, clear, screen changes and failed writes leave no
		// trusted graphics; uncertain areas are repainted below.
		s.sixelShown = slices.Grow(s.sixelShown[:0], n)[:n]
		clear(s.sixelShown)
		s.sixelAny = false
	}
	if len(tiles) == 0 && !s.sixelAny && len(s.sixelUncertain) == 0 {
		s.sixelBases, s.sixelPending = s.sixelBases[:0], false
		if s.sixelWork != nil {
			s.sixelBases, s.sixelPrepared = s.sixelWork.prepare(nil, s.sixelBases)
		}
		return false
	}
	if len(tiles) > 0 && s.sixelWork == nil {
		s.sixelWork = newSixelWork(s.sixelWake, s.kittyTmux)
	}
	s.sixelBases, s.sixelPrepared = s.sixelWork.prepare(tiles, s.sixelBases)

	s.sixelDesired = slices.Grow(s.sixelDesired[:0], n)[:n]
	clear(s.sixelDesired)
	s.sixelNext = append(s.sixelNext[:0], s.sixelShown...)
	s.sixelPending, s.sixelHold = false, s.sixelHold[:0]
	for i, tile := range tiles {
		base := s.sixelBases[i].base
		p := tile.placement
		if base != nil && base.raster == nil || !SixelSizeSupported(tile.rect.Dx(), tile.rect.Dy(), p.CellWidth, p.CellHeight) {
			continue // no native raster: the cells show their text
		}
		if base == nil {
			s.sixelPending = true
			if !s.sixelBases[i].known {
				// A different image is never held: its cells show their
				// composed text until the raster is ready.
				continue
			}
			// The same image at a new zoom, crop or size, or its drag
			// preview: keep showing it.
			s.sixelHold = append(s.sixelHold, tile.rect)
		}
		for y := tile.rect.Min.Y; y < tile.rect.Max.Y; y++ {
			for x := tile.rect.Min.X; x < tile.rect.Max.X; x++ {
				cell := sixelCell{base: sixelPending}
				if base != nil {
					cell = sixelCell{base.id,
						int32(tile.column + x - tile.rect.Min.X), //nolint:gosec // bounded by the image grid
						int32(tile.row + y - tile.rect.Min.Y)}    //nolint:gosec // bounded by the image grid
				}
				s.sixelDesired[y*width+x] = cell
			}
		}
	}
	for _, rect := range s.sixelUncertain {
		rect = rect.Intersect(s.cellbuf.Bounds())
		s.invalidateSixelArea(rect)
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			clear(s.sixelNext[y*width+rect.Min.X : y*width+rect.Max.X])
		}
	}
	// After invalidation, so uncertain cells are never held.
	for _, rect := range s.sixelHold {
		s.scr.HoldArea(s.cellbuf.RenderBuffer, rect)
	}
	// Repaint the text of each run of cells whose pixels are not wanted.
	for y := range height {
		row := y * width
		for x := 0; x < width; {
			if s.sixelNext[row+x].base == 0 || s.sixelDesired[row+x].base != 0 {
				x++
				continue
			}
			start := x
			for x < width && s.sixelNext[row+x].base != 0 && s.sixelDesired[row+x].base == 0 {
				s.sixelNext[row+x] = sixelCell{}
				x++
			}
			s.invalidateSixelArea(image.Rect(start, y, x, y+1))
		}
	}
	return true
}

// paintSixels runs after the text diff, in the same synchronized frame. Text
// written or erased over an image removes its pixels there; only the cells
// that do not show their wanted raster are sent, as one crop per tile.
func (s *cursedRenderer) paintSixels(tiles []sixelTile) {
	width := s.cellbuf.Width()
	for y, span := range s.sixelTextDamage {
		if span.Start < span.End {
			clear(s.sixelNext[y*width+max(0, span.Start) : y*width+min(width, span.End)])
		}
	}
	s.sixelAttempt = s.sixelAttempt[:0]
	for i, tile := range tiles {
		base := s.sixelBases[i].base
		if base == nil || base.raster == nil {
			continue
		}
		var dirty image.Rectangle
		for y := tile.rect.Min.Y; y < tile.rect.Max.Y; y++ {
			for x := tile.rect.Min.X; x < tile.rect.Max.X; x++ {
				if s.sixelNext[y*width+x] != s.sixelDesired[y*width+x] {
					dirty = dirty.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
		if dirty.Empty() {
			continue
		}
		grid := dirty.Sub(tile.rect.Min).Add(image.Pt(tile.column, tile.row))
		sequence := base.full
		if grid != base.grid {
			// Usually the cells under a closed menu: small, and quantized
			// already, so encoding is cheap enough for the writer.
			var err error
			if sequence, err = encodeSixelCrop(context.Background(), base, grid, s.kittyTmux); err != nil {
				continue
			}
		}
		s.scr.MoveTo(dirty.Min.X, dirty.Min.Y)
		_, _ = s.scr.WriteString(sequence)
		s.sixelAttempt = append(s.sixelAttempt, dirty)
		for y := dirty.Min.Y; y < dirty.Max.Y; y++ {
			copy(s.sixelNext[y*width+dirty.Min.X:y*width+dirty.Max.X], s.sixelDesired[y*width+dirty.Min.X:y*width+dirty.Max.X])
		}
	}
}

// commitSixels records the physical cells after a successful write.
func (s *cursedRenderer) commitSixels(planned bool) {
	if planned {
		s.sixelShown, s.sixelNext = s.sixelNext, s.sixelShown
		s.sixelAny = false
		for _, cell := range s.sixelShown {
			if cell.base != 0 {
				s.sixelAny = true
				break
			}
		}
	}
	s.sixelUncertain, s.sixelInvalid = s.sixelUncertain[:0], false
}

// failSixels keeps every area that may show pixels after a failed write.
// The next frame repaints its text, then sends the wanted rasters again.
func (s *cursedRenderer) failSixels() {
	if area := s.sixelShownArea(); !area.Empty() {
		s.sixelUncertain = append(s.sixelUncertain, area)
	}
	for _, rect := range s.sixelAttempt {
		if !slices.Contains(s.sixelUncertain, rect) {
			s.sixelUncertain = append(s.sixelUncertain, rect)
		}
	}
	s.sixelAttempt = s.sixelAttempt[:0]
	s.sixelInvalid = true
}

// sixelShownArea bounds the cells that physically show image pixels.
func (s *cursedRenderer) sixelShownArea() image.Rectangle {
	var area image.Rectangle
	if !s.sixelAny {
		return area
	}
	width := s.cellbuf.Width()
	for i, cell := range s.sixelShown {
		if cell.base != 0 && width > 0 {
			area = area.Union(image.Rect(i%width, i/width, i%width+1, i/width+1))
		}
	}
	return area
}

func (s *cursedRenderer) clearSixels() {
	area := s.sixelShownArea()
	if !area.Empty() {
		s.invalidateSixelArea(area)
	}
	for _, rect := range s.sixelUncertain {
		s.invalidateSixelArea(rect)
	}
	if !area.Empty() || len(s.sixelUncertain) > 0 {
		s.scr.Render(s.cellbuf.RenderBuffer)
	}
}

// forgetSixels drops graphics history once the terminal no longer shows it.
func (s *cursedRenderer) forgetSixels() {
	clear(s.sixelShown)
	s.sixelAny, s.sixelUncertain, s.sixelInvalid = false, s.sixelUncertain[:0], false
}

func hasSixelPlacement(placements []ImagePlacement) bool {
	for _, p := range placements {
		if p.Protocol == ImageSixel {
			return true
		}
	}
	return false
}

// Used for the Content path, which already goes through UV's ANSI parser.
func (s *cursedRenderer) snapshotSixelContent(view *View) {
	if view.Surface == nil && hasSixelPlacement(view.Images) {
		view.sixels = snapshotSixelTiles(s.cellbuf.Buffer, view.Images)
	}
}

func clipSixelTiles(tiles []sixelTile, bounds image.Rectangle) []sixelTile {
	clipped := false
	for _, tile := range tiles {
		clipped = clipped || !tile.rect.In(bounds)
	}
	if !clipped {
		return tiles
	}
	result := make([]sixelTile, 0, len(tiles))
	for _, tile := range tiles {
		rect := tile.rect.Intersect(bounds)
		if rect.Empty() {
			continue
		}
		tile.column += rect.Min.X - tile.rect.Min.X
		tile.row += rect.Min.Y - tile.rect.Min.Y
		tile.rect = rect
		result = append(result, tile)
	}
	return result
}
