package compositor

import (
	"unicode/utf8"

	"crude/tui/cell"
	"crude/tui/surface"

	uv "github.com/charmbracelet/ultraviolet"
)

// PresentSurface finalizes the frame without constructing ANSI. The drawable
// borrows the completed grid until the next BeginFrame/Resize; Bubble Tea
// snapshots it synchronously before allowing another Update. CurrentSurface
// reuses that same frame during presentation coalescing.
func (c *Compositor) PresentSurface() uv.Surface {
	c.finishFrame()
	return c.CurrentSurface()
}

func (c *Compositor) CurrentSurface() uv.Surface {
	if cap(c.direct.row) < c.width {
		c.direct.row = make([]uv.Cell, c.width)
	}
	return cellSurface{buffer: c.front, row: c.direct.row[:c.width], token: uv.SnapshotToken{Owner: &c.direct.owner, Generation: c.direct.generation}}
}

type directCells struct {
	row        []uv.Cell
	owner      uv.SnapshotOwner
	generation uint64
}

type cellSurface struct {
	buffer *surface.CellBuffer
	row    []uv.Cell
	token  uv.SnapshotToken
}

func (s cellSurface) SnapshotToken() uv.SnapshotToken { return s.token }

// Only producer snapshots opt into complete-row writes. A general uv.Screen
// may track per-cell damage, so exposing Line/Buffer is not enough to opt in.
type cellRowScreen interface {
	SetCellRow(y int, row []uv.Cell)
}

func (s cellSurface) Bounds() uv.Rectangle {
	return uv.Rect(0, 0, s.buffer.Width(), s.buffer.Height())
}

// Draw preserves the producer's measured width rather than asking a parser to
// infer it again. UV creates continuation cells atomically from the primary.
func (s cellSurface) Draw(dst uv.Screen, area uv.Rectangle) {
	clip := area.Intersect(dst.Bounds())
	rows, rowOK := dst.(cellRowScreen)
	rowOK = rowOK && area.Min.X == 0 && clip.Min.X == 0 &&
		clip.Max.X == s.buffer.Width() && clip.Max.X == dst.Bounds().Max.X &&
		len(s.row) == s.buffer.Width()
	var previous cell.Cell
	var style uv.Style
	var out uv.Cell
	haveStyle := false
	for y := clip.Min.Y; y < clip.Max.Y && y-area.Min.Y < s.buffer.Height(); y++ {
		row := s.buffer.Row(y - area.Min.Y)
		// A complete, well-formed row can replace old graphemes atomically.
		// Malformed or cropped rows retain UV's individual SetCell semantics.
		if rowOK && completeCellRow(row) {
			for x := 0; x < len(row); {
				c := row[x]
				if !haveStyle || c.FG != previous.FG || c.BG != previous.BG || c.Attr != previous.Attr {
					style = directStyle(c)
					previous, haveStyle = c, true
				}
				text := c.Content
				if text == "" {
					text = runeText(c.Char)
				}
				s.row[x] = uv.Cell{Content: text, Width: int(c.Width), Style: style}
				if c.Width == 1 {
					// Filled backgrounds often occupy most of a row. Convert the
					// run once, then bulk-copy UV values rather than reconstructing
					// the same large cell at every column.
					end := x + 1
					for end < len(row) && row[end] == c {
						end++
					}
					for n := 1; n < end-x; n *= 2 {
						copy(s.row[x+n:end], s.row[x:x+n])
					}
					x = end
					continue
				}
				end := x + int(c.Width)
				for x++; x < end; x++ {
					s.row[x] = uv.Cell{}
				}
			}
			rows.SetCellRow(y, s.row)
			continue
		}
		for x := clip.Min.X; x < clip.Max.X && x-area.Min.X < len(row); x++ {
			c := row[x-area.Min.X]
			if c.Width == 0 {
				continue
			}
			if !haveStyle || c.FG != previous.FG || c.BG != previous.BG || c.Attr != previous.Attr {
				style = directStyle(c)
				previous, haveStyle = c, true
			}
			text := c.Content
			if text == "" {
				text = runeText(c.Char)
			}
			out = uv.Cell{Content: text, Width: int(c.Width), Style: style}
			dst.SetCell(x, y, &out)
		}
	}
}

func completeCellRow(row []cell.Cell) bool {
	for x := 0; x < len(row); {
		width := int(row[x].Width)
		if width == 0 || x+width > len(row) {
			return false
		}
		end := x + width
		for x++; x < end; x++ {
			if row[x].Width != 0 {
				return false
			}
		}
	}
	return true
}

// Static ASCII strings avoid allocating on rune-only border/space cells.
const asciiCells = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

func runeText(r rune) string {
	if r == 0 {
		return " "
	}
	if r >= ' ' && r <= '~' {
		return asciiCells[r-' ' : r-' '+1]
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	return string(buf[:n])
}

func directColor(c cell.Color) uv.Color {
	switch c.Type {
	case cell.ColorPalette:
		return uv.IndexedColor(c.Palette)
	case cell.ColorTrueColor:
		return uv.RGBColor(c.R, c.G, c.B)
	default:
		return 0
	}
}

func directStyle(c cell.Cell) uv.Style {
	s := uv.Style{Fg: directColor(c.FG), Bg: directColor(c.BG)}
	// Terminal video changes every RGB cell, but usually has no attributes.
	// Do not walk the attribute mapping for every pixel in that common case.
	if c.Attr == 0 {
		return s
	}
	// These fixed bit positions are exhaustively checked against parsed ANSI
	// for all 512 source combinations. Keep the conversion scalar: it is run
	// for every newly colored terminal cell, not just styled text runs.
	a := c.Attr
	s.Attrs = uint8(a&cell.AttrBold) |
		uint8((a&(cell.AttrItalic|cell.AttrReverse))<<1) |
		uint8((a&cell.AttrDim)>>5) |
		uint8((a&cell.AttrBlink)>>2) |
		uint8((a&cell.AttrStrikethrough)<<4)
	if c.Attr&cell.AttrUnderline != 0 {
		s.Underline = uv.UnderlineSingle
	}
	if c.Attr&cell.AttrUndercurl != 0 {
		s.Underline = uv.UnderlineCurly
	}
	if c.Attr&cell.AttrUnderlineDotted != 0 {
		s.Underline = uv.UnderlineDotted
	}
	return s
}
