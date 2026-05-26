package surface

import (
	"crude/tui/cell"

	"github.com/clipperhouse/displaywidth"
	"github.com/rivo/uniseg"
)

// CellBuffer is a 2D grid of Cells. It is the fundamental rendering target
// for the compositing engine.
//
// CellBuffer uses a flat []Cell array (not [][]Cell) for memory locality.
// Access pattern: buffer.cells[y * buffer.width + x]
//
// Operations on CellBuffer:
//   - Set/Get individual cells
//   - Fill a region with a base cell
//   - Write a string of text into a row
//   - Resize (reallocates the buffer, preserving existing content)
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
	for i := range b.cells {
		b.cells[i] = c
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

// WriteString writes a string of text starting at position (x, y) using the
// given style. Handles wide characters correctly:
//   - Wide chars (width=2) write the char at column N and a continuation cell at N+1
//   - If a wide char would be split across the buffer edge, it is truncated
//
// Uses uniseg for grapheme cluster iteration and width measurement.
// Returns the x position after the last written cell.
func (b *CellBuffer) WriteString(x, y int, text string, fg, bg cell.Color, attr cell.Attrs) int {
	if y < 0 || y >= b.height {
		return x
	}
	curX := x
	gr := uniseg.NewGraphemes(text)
	for gr.Next() {
		cluster := gr.Str()
		w := displaywidth.String(cluster)
		if w == 0 {
			// Skip zero-width combining characters visually
			continue
		}
		if curX >= b.width {
			break
		}
		r := []rune(cluster)[0] // First rune of the grapheme cluster
		if w == 2 {
			// Wide character: needs 2 columns
			if curX+1 >= b.width {
				// Wide char would be split at the buffer edge, truncate
				break
			}
			b.Set(curX, y, cell.Cell{Char: r, Width: 2, FG: fg, BG: bg, Attr: attr})
			b.Set(curX+1, y, cell.Cell{Char: 0, Width: 0, FG: fg, BG: bg, Attr: attr})
			curX += 2
		} else {
			// Normal-width character (width=1)
			b.Set(curX, y, cell.Cell{Char: r, Width: 1, FG: fg, BG: bg, Attr: attr})
			curX++
		}
	}
	return curX
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
	blank := cell.TransparentBlank()
	for i := range b.cells {
		b.cells[i] = blank
	}
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
