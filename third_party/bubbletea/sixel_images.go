package tea

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/ansi/sixel"
	"golang.org/x/image/draw"
)

// MaxSixelPixels bounds the visible raster, not the source or virtual zoom grid.
// The encoder uses up to 256 palette bitplanes as well as the output bitmap.
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

func sameSixelRaster(a, b sixelTile) bool {
	return a.placement == b.placement && a.rect.Size() == b.rect.Size() &&
		a.column == b.column && a.row == b.row && a.background == b.background
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

func encodeSixel(tile sixelTile) (string, error) {
	p := tile.placement
	w, h := tile.rect.Dx()*p.CellWidth, tile.rect.Dy()*p.CellHeight
	if w <= 0 || h <= 0 || w > MaxSixelPixels/h {
		return "", nil
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
	if err := (&sixel.Encoder{Dither: true}).Encode(&payload, pixels); err != nil {
		return "", err
	}
	// x/ansi ends every raster with a graphics newline. No pixels follow the
	// final one; omit it so an image at the bottom cannot scroll the text grid.
	data := bytes.TrimSuffix(payload.Bytes(), []byte{'-'})
	return ansi.SixelGraphics(0, 1, 0, data), nil
}

func (s *cursedRenderer) invalidateSixelArea(rect image.Rectangle) {
	rect = rect.Intersect(s.cellbuf.Bounds())
	s.scr.InvalidateArea(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		s.cellbuf.TouchLine(rect.Min.X, y, rect.Dx())
	}
}

// prepareSixels runs only on the single writer, after the producer lock is
// released. Cached rasters belong to the last successful publication, not to a
// tab's mutable geometry. Failed/partial writes keep their exact damaged areas.
func (s *cursedRenderer) prepareSixels(view View) ([]encodedSixel, []bool, error) {
	if len(view.sixels) == 0 && len(s.sixelImages) == 0 && len(s.sixelUncertain) == 0 {
		return nil, nil, nil
	}
	next := make([]encodedSixel, len(view.sixels))
	dirty := make([]bool, len(next))
	changed := s.sixelInvalid || len(next) != len(s.sixelImages)
	for i, tile := range view.sixels {
		next[i].tile = tile
		for _, old := range s.sixelImages {
			if sameSixelRaster(old.tile, tile) {
				next[i].sequence = old.sequence
				changed = changed || old.tile != tile
				break
			}
		}
		if next[i].sequence == "" {
			changed = true
			sequence, err := encodeSixel(tile)
			if err != nil {
				return nil, nil, err
			}
			next[i].sequence = sequence
		}
	}
	if changed || len(s.sixelUncertain) > 0 {
		for _, old := range s.sixelImages {
			s.invalidateSixelArea(old.tile.rect)
		}
		for _, rect := range s.sixelUncertain {
			s.invalidateSixelArea(rect)
		}
	}
	for i, tile := range view.sixels {
		dirty[i] = changed
		for y := tile.rect.Min.Y; !dirty[i] && y < tile.rect.Max.Y && y < len(s.cellbuf.Touched); y++ {
			line := s.cellbuf.Touched[y]
			dirty[i] = line != nil && (line.FirstCell != -1 || line.LastCell != -1)
		}
	}
	return next, dirty, nil
}

func (s *cursedRenderer) paintSixels(images []encodedSixel, dirty []bool) {
	for i, entry := range images {
		if !dirty[i] || entry.sequence == "" {
			continue
		}
		s.scr.MoveTo(entry.tile.rect.Min.X, entry.tile.rect.Min.Y)
		sequence := ansi.SaveCursor + entry.sequence + ansi.RestoreCursor
		if s.kittyTmux {
			sequence = ansi.TmuxPassthrough(sequence)
		}
		_, _ = s.scr.WriteString(sequence)
	}
}

func (s *cursedRenderer) failSixels(images []encodedSixel) {
	for _, entry := range images {
		s.sixelUncertain = append(s.sixelUncertain, entry.tile.rect)
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
