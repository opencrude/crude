package surface

import (
	"unicode/utf8"

	"crude/tui/bidi"
	"crude/tui/cell"

	"github.com/charmbracelet/x/ansi"
)

// CellBuffer is a 2D grid of Cells. It is the fundamental rendering target
// for the compositing engine.
//
// CellBuffer uses a flat []Cell array (not [][]Cell) for memory locality.
// Access pattern: buffer.cells[y * buffer.width + x]
//
// Operations on CellBuffer:
// - Set/Get individual cells
// - Fill a region with a base cell
// - Write a string of text into a row
// - Resize (reallocates the buffer, preserving existing content)
type CellBuffer struct {
	cells  []cell.Cell
	width  int
	height int
}

// NewCellBuffer creates a buffer of the given dimensions, filled with
// transparent blank cells.
func NewCellBuffer(w, h int) *CellBuffer {
	b := &CellBuffer{
		width:  w,
		height: h,
		cells:  make([]cell.Cell, w*h),
	}
	b.Clear()
	return b
}

// Width returns the buffer width in columns.
func (b *CellBuffer) Width() int {
	return b.width
}

// Height returns the buffer height in rows.
func (b *CellBuffer) Height() int {
	return b.height
}

// Set writes a cell at the given coordinates.
// Out-of-bounds writes are silently ignored (implicit clipping).
func (b *CellBuffer) Set(x, y int, c cell.Cell) {
	if x < 0 || y < 0 || x >= b.width || y >= b.height {
		return
	}
	if c.Width > 1 {
		b.SetSpan(x, y, c)
		return
	}
	if c.Width == 1 && b.cells[y*b.width+x].Width == 1 {
		b.cells[y*b.width+x] = c
		return
	}
	if c.Width > 0 {
		clearIntersectingSpans(b, x, y, max(1, int(c.Width)))
	}
	b.setRaw(x, y, c)
}

// SetSpan writes one primary cell and every continuation column atomically.
// The write is ignored if the measured grapheme would cross the row edge.
func (b *CellBuffer) SetSpan(x, y int, c cell.Cell) {
	span := max(1, int(c.Width))
	if x < 0 || y < 0 || y >= b.height || x+span > b.width {
		return
	}
	clearIntersectingSpans(b, x, y, span)
	b.setRaw(x, y, c)
	continuation := cell.Cell{
		Width:       0,
		FG:          c.FG,
		BG:          c.BG,
		Attr:        c.Attr,
		Link:        c.Link,
		Transparent: c.Transparent,
	}
	for offset := 1; offset < span; offset++ {
		b.setRaw(x+offset, y, continuation)
	}
}

func (b *CellBuffer) setRaw(x, y int, c cell.Cell) {
	if x < 0 || y < 0 || x >= b.width || y >= b.height {
		return
	}
	b.cells[y*b.width+x] = c
}

// Get reads the cell at the given coordinates.
// Out-of-bounds reads return a transparent blank cell.
func (b *CellBuffer) Get(x, y int) cell.Cell {
	if x < 0 || y < 0 || x >= b.width || y >= b.height {
		return cell.TransparentBlank()
	}
	return b.cells[y*b.width+x]
}

// Fill fills the entire buffer with the given cell.
func (b *CellBuffer) Fill(c cell.Cell) {
	if len(b.cells) == 0 {
		return
	}
	// Seed once, then double the initialized prefix with bulk copies. Cells
	// contain immutable strings, so copying values does not alias mutable state.
	b.cells[0] = c
	for n := 1; n < len(b.cells); n *= 2 {
		copy(b.cells[n:], b.cells[:n])
	}
}

// FillRect fills a rectangular region with the given cell.
// Out-of-bounds regions are clipped.
func (b *CellBuffer) FillRect(r Rect, c cell.Cell) {
	// Clip the rectangle to buffer bounds
	bufRect := FullRect(b.width, b.height)
	r = r.Intersect(bufRect)
	if r.Empty() {
		return
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		clearIntersectingSpans(b, r.X, y, r.W)
		rowStart := y * b.width
		for x := r.X; x < r.X+r.W; x++ {
			b.cells[rowStart+x] = c
		}
	}
}

// Resize changes the buffer dimensions. Existing content is preserved
// where possible; new areas are filled with transparent blanks.
func (b *CellBuffer) Resize(w, h int) {
	if w == b.width && h == b.height {
		return
	}
	newCells := make([]cell.Cell, w*h)
	// Fill new buffer with transparent blanks
	blank := cell.TransparentBlank()
	for i := range newCells {
		newCells[i] = blank
	}
	// Copy existing content, row by row
	copyW := min(b.width, w)
	copyH := min(b.height, h)
	for y := 0; y < copyH; y++ {
		srcStart := y * b.width
		dstStart := y * w
		copy(newCells[dstStart:dstStart+copyW], b.cells[srcStart:srcStart+copyW])
	}
	b.cells = newCells
	b.width = w
	b.height = h
}

// WriteString writes one label starting at position (x, y) using the given
// style. Handles multi-column graphemes correctly by writing one primary cell
// followed by Width-1 continuation cells. If the grapheme would be split
// across the buffer edge, it is truncated.
//
// The label is one paragraph with an automatic direction: right-to-left text
// in it is arranged in visual order and Arabic letters are joined (see
// crude/tui/bidi). A display line built from several runs must use
// WriteLogical for each run and arrange the whole line with ApplyLine.
//
// Uses charmbracelet/x/ansi for grapheme cluster iteration and width measurement.
// Returns the x position after the last written cell.
func (b *CellBuffer) WriteString(x, y int, text string, fg, bg cell.Color, attr cell.Attrs, links ...*cell.Link) int {
	end, arrange := b.writeText(x, y, text, fg, bg, attr, links)
	if arrange {
		b.arrangeLabel(x, y, end, text)
	}
	return end
}

// firstCluster returns the first grapheme cluster of s and its width with the
// shared measurement. Printable ASCII before ASCII is its own cluster.
func firstCluster(s string) (string, int) {
	if s[0] > 0x1f && s[0] < 0x7f && (len(s) == 1 || s[1] < utf8.RuneSelf) {
		return s[:1], 1
	}
	// Must match UV cellbuf. See tui/rendering/width.go.
	return ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
}

// writeText writes text in logical order and reports whether it contains a
// character that can change its visual order.
func (b *CellBuffer) writeText(x, y int, text string, fg, bg cell.Color, attr cell.Attrs, links []*cell.Link) (int, bool) {
	if y < 0 || y >= b.height {
		return x, false
	}
	var link *cell.Link
	if len(links) > 0 {
		link = links[0]
	}
	curX := x
	rest := text
	reorders := false
	for len(rest) > 0 {
		cluster, w := firstCluster(rest)
		if len(cluster) == 0 {
			break
		}
		rest = rest[len(cluster):]
		r, _ := utf8.DecodeRuneInString(cluster)
		if r >= 0x0590 && !reorders {
			reorders = bidi.Triggers(r)
		}
		if w == 0 {
			// Skip zero-width combining characters visually
			continue
		}
		if curX >= b.width || curX+w > b.width {
			break
		}
		cellBG := bg
		if bg.IsDefault() {
			// Default means "inherit what is under me". Inside this buffer
			// that is an already painted fill (a popup body, a selected row);
			// only still-transparent cells defer to lower layers at blit time.
			// Writing a literal default here punched holes through the fill.
			if under := b.Get(curX, y); !under.Transparent {
				cellBG = under.BG
			}
		}
		b.Set(curX, y, cell.Cell{Char: r, Content: cluster, Width: uint8(w), Link: link, FG: fg, BG: cellBG, Attr: attr})
		curX += w
	}
	return curX, reorders
}

// Clone creates a deep copy of the buffer.
func (b *CellBuffer) Clone() *CellBuffer {
	cells := make([]cell.Cell, len(b.cells))
	copy(cells, b.cells)
	return &CellBuffer{
		cells:  cells,
		width:  b.width,
		height: b.height,
	}
}

// Clear resets all cells to transparent blanks.
func (b *CellBuffer) Clear() {
	b.Fill(cell.TransparentBlank())
}

// Cells returns the raw cell slice for fast iteration in the compositor.
func (b *CellBuffer) Cells() []cell.Cell {
	return b.cells
}

// Row returns a slice of cells for a specific row.
// Returns nil for out-of-bounds rows.
func (b *CellBuffer) Row(y int) []cell.Cell {
	if y < 0 || y >= b.height {
		return nil
	}
	return b.cells[y*b.width : (y+1)*b.width]
}
