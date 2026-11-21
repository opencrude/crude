package chatprojection

import (
	"strings"
	"sync"

	"crude/tui/bidi"
	"crude/tui/rendering"
)

// A mapped paragraph is the semantic Goldmark block, not one of the lines
// returned by the terminal wrapper. Each piece records one display line in
// that paragraph's columns, and where the renderer put it on the row.
type proseParagraph struct {
	source      *rendering.CopyText
	last        int
	code        int
	units       []bidi.Unit
	cols        int
	fallbackRTL bool

	mu         sync.Mutex
	generation uint64
	layout     *bidi.Layout
}

type prosePiece struct {
	paragraph  *proseParagraph
	column     int
	start, end int
}

// proseEdge is a fixed inline-start box. A right-side marker has its own
// bidi paragraph; it never resolves against the item text or sibling markers.
type proseEdge struct {
	start, end int
	text       string
}

type proseLine struct {
	pieces            []prosePiece
	boxStart          int         // logical columns taken by ancestor indents and markers
	boxLeft, boxRight []proseEdge // outermost first, independently on each side
	align             bool
	headingPrefix     string
	suffix            int                         // end of styled heading suffix, if present
	codeText          string                      // fenced code has a per-row LTR grid
	boxes             []rendering.MarkdownCellBox // Fixed logical cell boxes; independent of placement.
	blockWidth        int                         // Document width at reflow, excluding transcript chrome.
	shift             int                         // transcript-owned chrome, fixed at the left

	mu         sync.Mutex
	generation uint64
	width      int
	layout     *bidi.Line
}
type proseBuilder struct {
	source *rendering.CopyText
	active *proseParagraph
}

func (b *proseBuilder) paragraph(t *rendering.CopyText, fallbackRTL bool) *proseParagraph {
	if b.source != t {
		b.finish()
		b.source, b.active = t, nil
		if t != nil && !t.Code && (fallbackRTL || bidi.NeedsLayout(t.Value)) {
			b.active = &proseParagraph{source: t, code: -1, fallbackRTL: fallbackRTL}
		}
	}
	return b.active
}

func (b *proseBuilder) finish() {
	if p := b.active; p != nil {
		p.hidden(len(p.source.Value))
		p.endCode(-1)
	}
}

func (p *proseParagraph) hidden(end int) {
	if end <= p.last {
		return
	}
	// Wrapping can drop spaces and the ANSI bridge does not paint zero-width
	// controls. They still affect the paragraph's resolved levels, but must
	// not claim any painted columns.
	p.add(strings.ReplaceAll(p.source.Value[p.last:end], "\n", " "), p.last, true, true)
	p.last = end
}

func (p *proseParagraph) endCode(next int) {
	if p.code >= 0 && next != p.code && len(p.units) > 0 {
		p.units[len(p.units)-1].After = bidi.PDI
	}
}

func (p *proseParagraph) add(text string, offset int, linear, hidden bool) {
	for len(text) > 0 {
		glyph, width := rendering.FirstGrapheme(text)
		if glyph == "" {
			break
		}
		index := -1
		if offset >= 0 {
			index = p.source.InlineCode(offset)
		}
		p.endCode(index)
		if hidden {
			width = 0
		}
		unit := bidi.Unit{Text: glyph, Width: width}
		if index >= 0 && p.code != index {
			unit.Before = bidi.LRI
		}
		p.units = append(p.units, unit)
		p.code = index
		p.cols += width
		text = text[len(glyph):]
		if offset >= 0 && linear {
			offset += len(glyph)
		}
	}
}

func (p *proseParagraph) append(span RowSpan) {
	at, linear := -1, false
	if span.Copy.Valid() {
		p.hidden(span.Copy.Start)
		at = span.Copy.Start
		linear = span.Copy.End-span.Copy.Start == len(span.Text)
		p.last = max(p.last, span.Copy.End)
	}
	p.add(span.Text, at, linear, false)
}

func (p *proseParagraph) line(start, end int, generation uint64) *bidi.Line {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation != generation {
		p.layout = bidi.Analyze(p.units, bidi.Auto, p.fallbackRTL)
		p.generation = generation
	}
	return p.layout.Line(start, end, end-start)
}

func (b *proseBuilder) build(spans []RowSpan, geometry *rendering.MarkdownGeometry) (*proseLine, bool) {
	var line *proseLine
	boxStart, fallbackRTL := 0, false
	if geometry != nil && len(geometry.Prefixes) != 0 {
		inner := geometry.Prefixes[len(geometry.Prefixes)-1]
		boxStart, fallbackRTL = inner.End, inner.RTL
	}
	col, blocks := 0, 0
	code := false
	for i := 0; i < len(spans); {
		s := spans[i]
		if !s.Copy.Valid() {
			// No widths to measure on decoration-only or left-to-right rows.
			i++
			continue
		}
		blocks++
		code = code || s.Copy.Text.Code
		p := b.paragraph(s.Copy.Text, fallbackRTL)
		// Keep same-source inline decorations between its first and last
		// span. Leave external prefixes, suffixes and padding out.
		j, end := i+1, i+1
		for ; j < len(spans); j++ {
			if r := spans[j].Copy; r.Valid() {
				if r.Text != s.Copy.Text {
					break
				}
				end = j + 1
			}
		}
		if p != nil {
			if line == nil {
				line = &proseLine{}
			}
			// The first bidi fragment can follow a left-to-right block.
			if len(line.pieces) == 0 {
				for _, prior := range spans[:i] {
					col += rendering.VisibleWidth(prior.Text)
				}
			}
			first, start := col, p.cols
			for _, span := range spans[i:end] {
				p.append(span)
			}
			col += p.cols - start
			line.pieces = append(line.pieces, prosePiece{p, first, start, p.cols})
		} else if line != nil {
			for _, span := range spans[i:end] {
				col += rendering.VisibleWidth(span.Text)
			}
		}
		if line != nil {
			for _, span := range spans[end:j] {
				col += rendering.VisibleWidth(span.Text)
			}
		}
		i = j
	}
	if line == nil {
		line = boxedProseLine(spans, geometry)
		if line != nil && code {
			line.codeText = proseText(spans, line.boxStart, -1)
		}
		return line, code
	}
	line.setBox(spans, geometry)
	line.align = blocks == 1
	if line.align {
		piece := line.pieces[0]
		line.headingPrefix = proseText(spans, boxStart, piece.column)
		line.suffix = piece.column + piece.end - piece.start
		if geometry != nil {
			for _, decoration := range geometry.Decorations {
				if decoration.Start == line.suffix {
					line.suffix = decoration.End
				}
			}
		}
	}
	return line, code
}

// boxedProseLine retains just a moving container gutter when there is no
// prose paragraph on the row (e.g. a quote's blank row or a table rule).
func boxedProseLine(spans []RowSpan, geometry *rendering.MarkdownGeometry) *proseLine {
	if geometry != nil {
		for _, prefix := range geometry.Prefixes {
			if prefix.RTL && prefix.End > prefix.Start {
				line := &proseLine{}
				line.setBox(spans, geometry)
				return line
			}
		}
	}
	return nil
}

func (p *proseLine) setBox(spans []RowSpan, geometry *rendering.MarkdownGeometry) {
	if geometry == nil {
		return
	}
	for _, prefix := range geometry.Prefixes {
		if prefix.RTL && prefix.End > prefix.Start {
			if prefix.Start > p.boxStart {
				p.boxLeft = append(p.boxLeft, proseEdge{start: p.boxStart, end: prefix.Start})
			}
			p.boxRight = append(p.boxRight, proseEdge{start: prefix.Start, end: prefix.End,
				text: proseText(spans, prefix.Start, prefix.End)})
			p.boxStart = prefix.End
		}
	}
	if n := len(geometry.Prefixes); n > 0 {
		if end := geometry.Prefixes[n-1].End; end > p.boxStart {
			p.boxLeft = append(p.boxLeft, proseEdge{start: p.boxStart, end: end})
			p.boxStart = end
		}
	}
}

func proseText(spans []RowSpan, start, end int) string {
	if start == end {
		return ""
	}
	var text strings.Builder
	for col, i := 0, 0; i < len(spans) && (end < 0 || col < end); i++ {
		span := spans[i].Text
		next := col + rendering.VisibleWidth(span)
		if next > start {
			lo, hi := max(start, col)-col, next-col
			if end >= 0 {
				hi = min(hi, end-col)
			}
			if lo == 0 && hi == next-col {
				text.WriteString(span)
			} else {
				text.WriteString(rendering.ExtractVisibleRange(span, lo, hi))
			}
		}
		col = next
	}
	return text.String()
}

func (p *proseLine) shifted(cols int) *proseLine {
	if p == nil {
		return nil
	}
	return &proseLine{pieces: p.pieces, boxStart: p.boxStart, boxLeft: p.boxLeft, boxRight: p.boxRight,
		align: p.align, headingPrefix: p.headingPrefix, suffix: p.suffix, codeText: p.codeText, boxes: p.boxes, blockWidth: p.blockWidth,
		shift: p.shift + cols}
}

func (p *proseLine) arrange(width int) *bidi.Line {
	generation := bidi.Generation()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation != generation || p.width != width {
		p.layout = p.arrangeLine(width, generation)
		p.generation, p.width = generation, width
	}
	return p.layout
}

func (p *proseLine) arrangeLine(width int, generation uint64) *bidi.Line {
	if p.shift+p.boxStart > width {
		return nil
	}
	bodyWidth := width - p.shift - p.boxStart
	body := p.arrangeBody(bodyWidth, generation)
	if p.shift == 0 && p.boxStart == 0 {
		return body
	}
	if body.Identity() && len(p.boxRight) == 0 {
		return nil
	}
	var scratch [8]bidi.LineRun
	runs := append(scratch[:0], bidi.LineRun{Width: p.shift})
	for _, edge := range p.boxLeft {
		runs = append(runs, bidi.LineRun{Column: edge.start + p.shift, Width: edge.end - edge.start})
	}
	runs = append(runs, bidi.LineRun{Column: p.shift + p.boxStart, Width: bodyWidth, Line: body})
	for i := len(p.boxRight) - 1; i >= 0; i-- {
		edge := p.boxRight[i]
		runs = append(runs, bidi.LineRun{Column: edge.start + p.shift, Width: edge.end - edge.start,
			Line: arrangeProseEdge(edge.text, edge.end-edge.start)})
	}
	return bidi.ComposeLine(width, body.RTL(), runs)
}

// CSS Lists 3 gives ::marker unicode-bidi:isolate and inherits direction.
// Resolve the whole marker (including counter suffix and separator) with the
// existing UAX #9 resolver on its own. Never reverse or mirror glyphs here.
func arrangeProseEdge(text string, width int) *bidi.Line {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return bidi.TextLine(text, bidi.RightToLeft, width, width)
}

func (p *proseLine) arrangeBody(width int, generation uint64) *bidi.Line {
	if p.codeText != "" {
		return bidi.TextLine(p.codeText, bidi.LeftToRight, width, width)
	}
	var scratch [2]bidi.PlacedLine
	parts := scratch[:0]
	for _, piece := range p.pieces {
		column := piece.column - p.boxStart
		if end := column + piece.end - piece.start; column >= 0 && end <= width {
			line := piece.paragraph.line(piece.start, piece.end, generation)
			if p.align && line.RTL() {
				suffix := min(p.suffix-p.boxStart, width)
				// As in an ordinary RTL line, the first unused column (the
				// streaming caret's logical end) belongs beside the content.
				padding := min(1, width-suffix)
				return bidi.ComposeLine(width, true, []bidi.LineRun{
					{Column: suffix + padding, Width: width - suffix - padding},
					{Column: suffix, Width: padding},
					{Column: end, Width: suffix - end},
					{Column: column, Width: end - column, Line: line},
					{Width: column, Line: arrangeProseEdge(p.headingPrefix, column)},
				})
			}
			if !line.Identity() {
				parts = append(parts, bidi.PlacedLine{Column: column, Line: line})
			}
		}
	}
	return bidi.JoinLines(width, parts)
}
