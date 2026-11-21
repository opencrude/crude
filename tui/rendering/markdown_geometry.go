package rendering

import (
	"strconv"
	"strings"

	"crude/tui/cell"
)

// MarkdownCell holds a parsed cell's source bounds and paragraph direction.
// The source offsets follow Goldmark order, independently of painted columns.
type MarkdownCell struct {
	Source CopyRange
	RTL    bool
}

// MarkdownCellBox is one display row of a sized cell. The whole box includes
// alignment spaces and margins. Content excludes those generated columns.
type MarkdownCellBox struct {
	Cell                     *MarkdownCell
	Start, End               int
	ContentStart, ContentEnd int
}

// MarkdownGeometry describes the immutable structural regions of a native row.
type MarkdownGeometry struct {
	Cells       []MarkdownCellBox
	Prefixes    []MarkdownBoxPrefix
	Decorations []MarkdownDecoration
}

// MarkdownBoxPrefix is one list indentation, hanging marker or quote rail.
// Prefixes arrive outermost first, including on blank and continuation rows.
type MarkdownBoxPrefix struct {
	Start, End int
	RTL        bool
}

// MarkdownDecoration keeps a heading suffix together.
type MarkdownDecoration struct{ Start, End int }

type cellPen struct {
	copy   copyCursor
	fg, bg cell.Color
	attr   cell.Attrs
	link   *cell.Link
}

type geometryParser struct {
	row        *MarkdownGeometry
	box        MarkdownCellBox
	pens       map[*MarkdownCell]cellPen
	outer      cellPen
	decoration int
	prefix     MarkdownBoxPrefix
}

// MarkdownGeometry returns the structural regions from the most recent ParseLine call.
func (p *AnsiRunParser) MarkdownGeometry() *MarkdownGeometry {
	if p.copy == nil {
		return nil
	}
	return p.geometry.row
}

func (p *AnsiRunParser) pen() cellPen {
	return cellPen{copy: copyCursor{p.copy.active, p.copy.atomic}, fg: p.fg, bg: p.bg, attr: p.attr, link: p.link}
}

func (p *AnsiRunParser) setPen(pen cellPen) {
	p.copy.active, p.copy.atomic = pen.copy.source, pen.copy.atomic
	p.fg, p.bg, p.attr, p.link = pen.fg, pen.bg, pen.attr, pen.link
}

func geometryControl(sequence string) (string, bool) {
	if body, ok := strings.CutPrefix(sequence, sourcePrefix+":"); ok {
		return strings.CutSuffix(body, sourceEnd)
	}
	return "", false
}

func (p *AnsiRunParser) layoutControl(mark string, col int) {
	g := &p.geometry
	switch {
	case mark == "i" || mark == "r":
		g.prefix = MarkdownBoxPrefix{Start: col, RTL: mark == "r"}
	case mark == "f":
		g.prefix.End = col
		if g.row == nil {
			g.row = &MarkdownGeometry{}
		}
		g.row.Prefixes = append(g.row.Prefixes, g.prefix)
	case mark == "p":
		g.decoration = col
	case mark == "q" && col > g.decoration:
		if g.row == nil {
			g.row = &MarkdownGeometry{}
		}
		g.row.Decorations = append(g.row.Decorations, MarkdownDecoration{g.decoration, col})
	case strings.HasPrefix(mark, "b"):
		id, err := strconv.Atoi(mark[1:])
		if err == nil && p.copy.trace != nil && id > 0 && id <= len(p.copy.trace.cells) {
			g.box = MarkdownCellBox{Cell: p.copy.trace.cells[id-1], Start: col, ContentStart: col, ContentEnd: col}
		}
	case mark == "c" && g.box.Cell != nil:
		g.outer = p.pen()
		// A word or link can span multiple display rows. It resumes in
		// this cell's content only, never its padding or the next cell.
		if pen, ok := g.pens[g.box.Cell]; ok {
			p.setPen(pen)
		}
		g.box.ContentStart = col
	case mark == "d" && g.box.Cell != nil:
		if g.pens == nil {
			g.pens = make(map[*MarkdownCell]cellPen)
		}
		g.pens[g.box.Cell] = p.pen()
		p.setPen(g.outer)
		g.box.ContentEnd = col
	case mark == "e" && g.box.Cell != nil:
		g.box.End = col
		if g.row == nil {
			g.row = &MarkdownGeometry{}
		}
		g.row.Cells = append(g.row.Cells, g.box)
		g.box = MarkdownCellBox{}
	}
}
