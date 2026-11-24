package surface

import (
	"strings"
	"sync"
	"unicode/utf8"

	"crude/tui/bidi"
	"crude/tui/cell"
)

// ApplyLine moves the cells of one display line, painted in logical order
// at (x, y), into visual order. Every cell keeps its colors, attributes and
// link, so selection, search marks, diagnostics and a block cursor travel
// with their characters. A block whose source character is still painted
// takes its arranged glyph (joined Arabic forms, mirrored brackets).
//
// The move is refused, and the line stays in logical order, when the line
// leaves the buffer or a block would split a wide grapheme: a half grapheme
// must never reach the terminal.
func (b *CellBuffer) ApplyLine(x, y int, ln *bidi.Line) bool {
	if ln.Identity() {
		return false
	}
	span := ln.Span()
	if y < 0 || y >= b.height || x < 0 || x+span > b.width || span == 0 {
		return false
	}
	row := b.cells[y*b.width+x : y*b.width+x+span]
	blocks := ln.Blocks()
	for i := range blocks {
		bl := &blocks[i]
		if bl.Width == 0 {
			continue
		}
		if row[bl.Logical].Width == 0 {
			return false
		}
		if end := bl.Logical + bl.Width; end < span && row[end].Width == 0 {
			return false
		}
	}
	scratch := lineScratch.Get().(*[]cell.Cell)
	tmp := append((*scratch)[:0], row...)
	for i := range blocks {
		bl := &blocks[i]
		if bl.Width == 0 {
			continue
		}
		copy(row[bl.Visual:bl.Visual+bl.Width], tmp[bl.Logical:bl.Logical+bl.Width])
		presentBlock(row[bl.Visual:bl.Visual+bl.Width], bl, 0, bl.Visual == 0)
	}
	*scratch = tmp[:0]
	lineScratch.Put(scratch)
	return true
}

// PresentVisualLine applies glyph substitutions and bidi marks to cells a
// painter has already placed in visual order. The viewport is a crop, not
// a line boundary: clipped glyphs are not reshaped or measured again.
func (b *CellBuffer) PresentVisualLine(x, y, width, offset int, ln *bidi.Line) {
	if ln.Identity() || width <= 0 {
		return
	}
	row := b.Row(y)
	if x < 0 || x+width > len(row) {
		return
	}
	row = row[x : x+width]
	for _, bl := range ln.BlocksFrom(offset) {
		start := bl.Visual - offset
		if start >= width {
			break
		}
		lo, hi := max(0, start), min(width, start+bl.Width)
		presentBlock(row[lo:hi], &bl, lo-start, lo == 0)
	}
}

func presentBlock(cells []cell.Cell, bl *bidi.Block, skip int, first bool) {
	if len(cells) == 0 {
		return
	}
	glyph := skip == 0 && bl.Display != "" && shows(cells[0], bl.Source)
	if glyph {
		cells[0].Content = bl.Display
		cells[0].Char, _ = utf8.DecodeRuneInString(bl.Display)
	}
	for k := range cells {
		cells[k].Bidi = cell.NewBidiMark(int(bl.Level), first && k == 0, glyph && k == 0)
	}
}

var lineScratch = sync.Pool{New: func() any { s := make([]cell.Cell, 0, 256); return &s }}

// shows reports whether a painted cell still shows the unit's source text.
// Painters may draw something else in a unit's place (a whitespace marker,
// an indent guide); those keep their own glyph.
func shows(c cell.Cell, source string) bool {
	if c.Content != "" {
		return c.Content == source
	}
	r, size := utf8.DecodeRuneInString(source)
	return size == len(source) && c.Char == r
}

// WriteLogical writes text exactly in the given order. Painters that build a
// display line from several runs (an editor line, a text field, a chat row)
// write each run with WriteLogical and then arrange the whole line with
// ApplyLine. Arranging each run on its own would split a right-to-left
// phrase at every color change.
func (b *CellBuffer) WriteLogical(x, y int, text string, fg, bg cell.Color, attr cell.Attrs, links ...*cell.Link) int {
	end, _ := b.writeText(x, y, b.width, text, fg, bg, attr, links)
	return end
}

// WriteLogicalUntil writes complete graphemes before the exclusive right edge.
// complete is false when the next grapheme would cross it.
func (b *CellBuffer) WriteLogicalUntil(x, y, right int, text string, fg, bg cell.Color, attr cell.Attrs, link *cell.Link) (end int, complete bool) {
	end, _, complete = b.writeTextRun(x, y, min(right, b.width), text, fg, bg, attr, link)
	return
}

// ArrangeLabel arranges one label that a painter wrote at (x, y) up to
// column end in several WriteLogical runs (for example to highlight matched
// characters), as one paragraph with an automatic direction.
func (b *CellBuffer) ArrangeLabel(x, y, end int, text string) {
	b.arrangeLabel(x, y, end, text)
}

// arrangeLabel arranges one label written at (x, y) as its own paragraph
// with an automatic direction, like an HTML element with dir="auto".
func (b *CellBuffer) arrangeLabel(x, y, end int, text string) {
	if end > x {
		b.ApplyLine(x, y, bidi.TextLine(text, bidi.Auto, end-x, -1))
	}
}

// ArrangeCells arranges the painted cells [x, x+width) of row y as one
// paragraph, reading the text from the cells themselves. Painters that
// decode text while they paint (ANSI lines) use it once they know the line
// holds right-to-left text.
func (b *CellBuffer) ArrangeCells(x, y, width int, dir bidi.Direction) {
	if y < 0 || y >= b.height || x < 0 || width <= 0 || x+width > b.width {
		return
	}
	row := b.cells[y*b.width+x : y*b.width+x+width]
	var key strings.Builder
	for _, c := range row {
		if c.Width == 0 {
			continue
		}
		key.WriteString(cellText(c))
		key.WriteByte(0)
		key.WriteByte(c.Width)
	}
	ln := bidi.CachedLine(key.String(), dir, width, width, func(dst []bidi.Unit) []bidi.Unit {
		for _, c := range row {
			if c.Width != 0 {
				dst = append(dst, bidi.Unit{Text: cellText(c), Width: int(c.Width)})
			}
		}
		return dst
	})
	b.ApplyLine(x, y, ln)
}

func cellText(c cell.Cell) string {
	switch {
	case c.Content != "":
		return c.Content
	case c.Char == 0:
		return " "
	default:
		return string(c.Char)
	}
}

// ReadLogical returns the text of a run of painted cells (one row, left to
// right) in logical order. Cells of an arranged line return to the order of
// their source text and to their source characters.
func ReadLogical(cells []cell.Cell) string {
	var out strings.Builder
	for i := 0; i < len(cells); {
		if !cells[i].Bidi.Arranged() {
			if cells[i].Width != 0 {
				writeCellText(&out, cells[i], false)
			}
			i++
			continue
		}
		j := i + 1
		for j < len(cells) && cells[j].Bidi.Arranged() && !cells[j].Bidi.Start() {
			j++
		}
		var primaries []cell.Cell
		var levels []int8
		for _, c := range cells[i:j] {
			if c.Width == 0 {
				continue
			}
			primaries = append(primaries, c)
			levels = append(levels, int8(c.Bidi.Level()))
		}
		for _, k := range bidi.LogicalOrder(levels, nil) {
			writeCellText(&out, primaries[k], true)
		}
		i = j
	}
	return out.String()
}

func writeCellText(out *strings.Builder, c cell.Cell, arranged bool) {
	text := c.Content
	if text == "" {
		if c.Char == 0 {
			out.WriteByte(' ')
			return
		}
		text = string(c.Char)
	}
	if !arranged {
		out.WriteString(text)
		return
	}
	r, size := utf8.DecodeRuneInString(text)
	out.WriteRune(bidi.Logical(r, c.Bidi.Level(), c.Bidi.Glyph()))
	out.WriteString(text[size:])
}
