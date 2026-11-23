package markdownview

import (
	"sort"

	"crude/tui/chatprojection"
)

// Block locates one rendered top-level block.
type Block struct {
	Line int // first source line, zero-based
	Row  int // first painted row
}

// Document is one immutable rendering of a source revision at one width.
type Document struct {
	Rows     []chatprojection.MessageRow
	Blocks   []Block
	Lines    int // source lines
	Height   int // painted rows
	Revision uint64
	Width    int
}

// RowForLine maps a source line, which may be fractional, to a painted row.
// Inside a block the position moves in proportion to the block's lines, the
// interpolation the VS Code Markdown preview uses between source-mapped
// elements. It is continuous, so a scrolled editor moves the preview smoothly.
func (d *Document) RowForLine(line float64) float64 {
	if d == nil || len(d.Blocks) == 0 {
		return 0
	}
	i := sort.Search(len(d.Blocks), func(i int) bool { return float64(d.Blocks[i].Line) > line }) - 1
	if i < 0 {
		return 0
	}
	start, end, first, next := d.span(i)
	return float64(first) + fraction(line-float64(start), float64(end-start))*float64(next-first)
}

// LineForRow is the inverse of RowForLine.
func (d *Document) LineForRow(row float64) float64 {
	if d == nil || len(d.Blocks) == 0 {
		return 0
	}
	i := sort.Search(len(d.Blocks), func(i int) bool { return float64(d.Blocks[i].Row) > row }) - 1
	if i < 0 {
		return float64(d.Blocks[0].Line)
	}
	start, end, first, next := d.span(i)
	return float64(start) + fraction(row-float64(first), float64(next-first))*float64(end-start)
}

// span returns block i's source lines and painted rows, each up to where the
// next block starts.
func (d *Document) span(i int) (start, end, first, next int) {
	start, first = d.Blocks[i].Line, d.Blocks[i].Row
	end, next = max(d.Lines, start+1), max(d.Height, first+1)
	if i+1 < len(d.Blocks) {
		end, next = d.Blocks[i+1].Line, d.Blocks[i+1].Row
	}
	return start, max(end, start+1), first, max(next, first+1)
}

func fraction(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return min(1, max(0, value/total))
}
