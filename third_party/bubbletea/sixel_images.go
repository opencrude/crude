package tea

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
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

type encodedSixel struct {
	tile     sixelTile
	sequence string
}

// Raster identity excludes the terminal position. Only movement of an otherwise
// identical crop can use the same pixels from an older frame.
type sixelRaster struct {
	placement    ImagePlacement
	size, offset image.Point
	background   uv.Color
}

func (t sixelTile) raster() sixelRaster {
	return sixelRaster{t.placement, t.rect.Size(), image.Pt(t.column, t.row), t.background}
}

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
					// Retain the unused identity foreground. This also prevents
					// UV's blank-bottom erase from spanning a native image.
					buf.CellAt(x, y).Content = " "
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

func encodeSixel(ctx context.Context, tile sixelTile) (string, error) {
	p := tile.placement
	if !SixelSizeSupported(tile.rect.Dx(), tile.rect.Dy(), p.CellWidth, p.CellHeight) {
		return "", nil
	}
	w, h := tile.rect.Dx()*p.CellWidth, tile.rect.Dy()*p.CellHeight
	if err := ctx.Err(); err != nil {
		return "", err
	}
	src := p.Image.pixels
	bounds := src.Bounds()
	if p.hasSourceRect() {
		bounds = image.Rect(bounds.Min.X+p.SourceX, bounds.Min.Y+p.SourceY,
			bounds.Min.X+p.SourceX+p.SourceWidth, bounds.Min.Y+p.SourceY+p.SourceHeight).Intersect(bounds)
	}
	if bounds.Empty() {
		return "", nil
	}
	// Scale directly into the visible crop. Never allocate the virtual zoomed
	// image. The established resampler preserves sub-cell pixels and alpha.
	scale := math.Min(float64(p.Columns)*float64(p.CellWidth)/float64(bounds.Dx()),
		float64(p.Rows)*float64(p.CellHeight)/float64(bounds.Dy()))
	pixels := image.NewRGBA(image.Rect(0, 0, w, h))
	r, g, b, _ := tile.background.RGBA()
	background := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255}
	draw.Draw(pixels, pixels.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	origin := image.Pt(-tile.column*p.CellWidth, -tile.row*p.CellHeight)
	target := image.Rectangle{Min: origin, Max: origin.Add(image.Pt(
		max(1, int(math.Round(float64(bounds.Dx())*scale))),
		max(1, int(math.Round(float64(bounds.Dy())*scale)))))}
	draw.ApproxBiLinear.Scale(pixels, target, src, bounds, draw.Over, nil)
	var payload bytes.Buffer
	if err := (&sixel.Encoder{Dither: true}).EncodeContext(ctx, &payload, pixels); err != nil {
		return "", err
	}
	// x/ansi ends every raster with a graphics newline. No pixels follow the
	// final one; omit it so an image at the bottom cannot scroll the text grid.
	data := bytes.TrimSuffix(payload.Bytes(), []byte{'-'})
	return ansi.SixelGraphics(0, 1, 0, data), nil
}

// sixelWork prepares only the latest writer-clipped set. It has no terminal
// access. There is one active crop and one replaceable set per renderer; a late
// result cannot be admitted after that crop's exact raster identity disappears.
type sixelWork struct {
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	changed      chan struct{}
	wanted       []sixelTile
	results      map[sixelRaster]string
	active       sixelRaster
	cancelActive context.CancelFunc
	revision     atomic.Uint64
}

func newSixelWork(wake func(), tmux bool) *sixelWork {
	ctx, cancel := context.WithCancel(context.Background())
	w := &sixelWork{
		cancel:  cancel,
		done:    make(chan struct{}),
		changed: make(chan struct{}, 1),
		results: make(map[sixelRaster]string),
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
			var tile sixelTile
			for _, wanted := range w.wanted {
				if _, ok := w.results[wanted.raster()]; !ok {
					tile = wanted
					break
				}
			}
			if tile.placement.Image == nil {
				w.mu.Unlock()
				break
			}
			task, cancel := context.WithCancel(ctx)
			key := tile.raster()
			w.active, w.cancelActive = key, cancel
			w.mu.Unlock()

			// Encoding is into memory; the only writer error is cancellation.
			sequence, _ := encodeSixel(task, tile)
			if sequence != "" {
				sequence = ansi.SaveCursor + sequence + ansi.RestoreCursor
				if tmux {
					sequence = ansi.TmuxPassthrough(sequence)
				}
			}

			w.mu.Lock()
			admitted := task.Err() == nil
			if admitted {
				w.results[key] = sequence
				w.revision.Add(1)
			}
			cancel()
			w.active, w.cancelActive = sixelRaster{}, nil
			w.mu.Unlock()
			if admitted && wake != nil {
				wake()
			}
		}
	}
}

func (w *sixelWork) prepare(tiles []sixelTile) ([]encodedSixel, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !slices.Equal(w.wanted, tiles) {
		w.wanted = tiles
		hasRaster := func(key sixelRaster) bool {
			for _, tile := range tiles {
				if tile.raster() == key {
					return true
				}
			}
			return false
		}
		for key := range w.results {
			if !hasRaster(key) {
				delete(w.results, key)
			}
		}
		if w.cancelActive != nil && !hasRaster(w.active) {
			w.cancelActive()
		}
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}

	var next []encodedSixel
	for _, tile := range tiles {
		if sequence := w.results[tile.raster()]; sequence != "" {
			next = append(next, encodedSixel{tile, sequence})
		}
	}
	return next, w.revision.Load()
}

func (s *cursedRenderer) invalidateSixelArea(rect image.Rectangle) {
	rect = rect.Intersect(s.cellbuf.Bounds())
	s.scr.InvalidateArea(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		s.cellbuf.TouchLine(rect.Min.X, y, rect.Dx())
	}
}

func containsTile(rasters []encodedSixel, tile sixelTile) bool {
	for _, entry := range rasters {
		if entry.tile == tile {
			return true
		}
	}
	return false
}

// The writer owns physical graphics history, separately from prepared payloads.
// A pending or obsolete crop must not hold up the new composed text.
func (s *cursedRenderer) prepareSixels(tiles []sixelTile) []encodedSixel {
	if len(tiles) > 0 && s.sixelWork == nil {
		s.sixelWork = newSixelWork(s.sixelWake, s.kittyTmux)
	}
	var next []encodedSixel
	if s.sixelWork != nil {
		next, s.sixelPrepared = s.sixelWork.prepare(tiles)
	}

	for _, old := range s.sixelImages {
		if s.sixelInvalid || !containsTile(next, old.tile) {
			s.invalidateSixelArea(old.tile.rect)
		}
	}
	for _, rect := range s.sixelUncertain {
		s.invalidateSixelArea(rect)
	}
	return next
}

func (s *cursedRenderer) paintSixels(images []encodedSixel) {
	for _, entry := range images {
		rect := entry.tile.rect
		dirty := s.sixelInvalid || !containsTile(s.sixelImages, entry.tile)
		for y := rect.Min.Y; !dirty && y < rect.Max.Y && y < len(s.sixelTextDamage); y++ {
			span := s.sixelTextDamage[y]
			dirty = span.Start < span.End && span.Start < rect.Max.X && rect.Min.X < span.End
		}
		if !dirty || entry.sequence == "" {
			continue
		}
		s.scr.MoveTo(entry.tile.rect.Min.X, entry.tile.rect.Min.Y)
		_, _ = s.scr.WriteString(entry.sequence)
	}
}

func (s *cursedRenderer) failSixels(images []encodedSixel) {
	for _, entry := range images {
		if !slices.Contains(s.sixelUncertain, entry.tile.rect) {
			s.sixelUncertain = append(s.sixelUncertain, entry.tile.rect)
		}
	}
	s.sixelInvalid = true
}

func (s *cursedRenderer) clearSixels() {
	for _, entry := range s.sixelImages {
		s.invalidateSixelArea(entry.tile.rect)
	}
	for _, rect := range s.sixelUncertain {
		s.invalidateSixelArea(rect)
	}
	if len(s.sixelImages) > 0 || len(s.sixelUncertain) > 0 {
		s.scr.Render(s.cellbuf.RenderBuffer)
	}
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
