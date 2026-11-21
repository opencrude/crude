package chatprojection

import (
	"strings"

	"crude/tui/rendering"
	"crude/tui/ui"
)

// Keep only the recent working set of width-independent paragraphs. This
// is keyed by retained message identity, not content or previous widths.
// A large message can still use the regular mapped renderer.
const (
	markdownReflowEntries = 32
	markdownReflowMaxText = 8 << 10
)

type markdownReflowCache struct {
	entries map[string]preparedMarkdown
	step    uint64
}

type preparedMarkdown struct {
	source     string
	parsed     rendering.MarkdownDocument
	paragraphs []preparedParagraph
	used       uint64
}

type preparedParagraph struct {
	annotated string
	text      string
	spans     []RowSpan
}

func (c *markdownReflowCache) render(source, id string, md *rendering.MarkdownRenderer, width int, tokens *ui.TokenSet, codeActions bool) ([]PaintLine, bool) {
	if c == nil || id == "" || len(source) > markdownReflowMaxText {
		return renderMarkdownRows(source, md, tokens, id, codeActions)
	}
	if md == nil || plainMarkdown(source) {
		return nil, false
	}
	entry, ok := c.entries[id]
	if !ok || entry.source != source {
		entry = preparedMarkdown{source: source, parsed: md.Parse(presentPlan(source, true))}
		captured, err := md.CaptureParagraphs(entry.parsed)
		if err != nil {
			return nil, false
		}
		if len(captured) != 0 {
			state := newAnsiBridgeState(tokens)
			entry.paragraphs = make([]preparedParagraph, len(captured))
			for i, part := range captured {
				row := bridgeAnsiRuns(part.Parser().ParseLine(part.ANSI), state)
				entry.paragraphs[i] = preparedParagraph{part.ANSI, row.PlainText(), row.Spans}
			}
		}
	}
	if len(entry.paragraphs) != 0 {
		if rows, ok := entry.rows(width); ok {
			c.retain(id, entry)
			return rows, true
		}
	}
	projection, err := md.RenderParsedMapped(entry.parsed)
	if err != nil {
		return nil, false
	}
	return BridgeSourceRows(projection, tokens, id, codeActions), true
}

func (c *markdownReflowCache) retain(id string, entry preparedMarkdown) {
	if c.entries == nil {
		c.entries = make(map[string]preparedMarkdown)
	}
	if _, ok := c.entries[id]; !ok && len(c.entries) >= markdownReflowEntries {
		var oldest string
		for k, value := range c.entries {
			if oldest == "" || value.used < c.entries[oldest].used {
				oldest = k
			}
		}
		delete(c.entries, oldest)
	}
	c.step++
	entry.used = c.step
	c.entries[id] = entry
}

func (entry *preparedMarkdown) rows(width int) ([]PaintLine, bool) {
	var local [48]rendering.ParagraphWrapRange
	var localEnds [8]int
	ranges, ends := local[:0], localEnds[:0]
	nSpans := 0
	for _, part := range entry.paragraphs {
		start := len(ranges)
		var ok bool
		ranges, ok = rendering.WrapParagraphRanges(ranges, part.annotated, width)
		if !ok || ranges[len(ranges)-1].End > len(part.text) {
			return nil, false
		}
		ends = append(ends, len(ranges))
		nSpans += len(part.spans) + 2*(len(ranges)-start)
	}
	// Each row cuts at most one additional span. A second slot per row
	// covers the source-free padding that keeps the full document box.
	rows := make([]PaintLine, 0, len(ranges)+len(ends)-1)
	spans := make([]RowSpan, 0, nSpans+len(ends)-1)
	padding := strings.Repeat(" ", width)
	var prose proseBuilder
	for i, part := range entry.paragraphs {
		if i != 0 {
			start := len(spans)
			spans = append(spans, RowSpan{Text: padding, Role: ui.ColorFg})
			rows = append(rows, PaintLine{Spans: spans[start:len(spans):len(spans)], SourceAware: true, mappedMarkdown: true})
		}
		firstRange := 0
		if i > 0 {
			firstRange = ends[i-1]
		}
		span, offset := 0, 0
		for _, r := range ranges[firstRange:ends[i]] {
			start := len(spans)
			for span < len(part.spans) && offset < r.End {
				s := part.spans[span]
				end := offset + len(s.Text)
				if end > r.Start {
					spans = append(spans, s.Slice(max(0, r.Start-offset), min(end, r.End)-offset))
				}
				if end > r.End {
					break
				}
				span++
				offset = end
			}
			cols := rendering.VisibleWidth(part.text[r.Start:r.End])
			if cols > width {
				// The document box must finish exceptionally long break
				// tokens using Glamour's existing outer wrap.
				return nil, false
			}
			if cols < width {
				spans = append(spans, RowSpan{Text: padding[:width-cols], Role: ui.ColorFg})
			}
			row := PaintLine{Spans: spans[start:len(spans):len(spans)], SourceAware: true, mappedMarkdown: true}
			row.prose, _ = prose.build(row.Spans, nil)
			row.setBlockWidth(width)
			rows = append(rows, row)
		}
	}
	prose.finish()
	return rows, true
}
