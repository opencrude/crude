package chatprojection

import (
	"strings"

	"charm.land/lipgloss/v2"
	"crude/tui/bidi"
	"crude/tui/rendering"
	"crude/tui/ui"
)

func renderMarkdown(content string, mdRenderer *rendering.MarkdownRenderer) (string, bool) {
	if mdRenderer == nil {
		return "", false
	}
	content = presentPlan(content, true)
	if plainMarkdown(content) {
		return "", false
	}
	r, err := mdRenderer.Render(content)
	if err != nil {
		return "", false
	}
	r = strings.TrimPrefix(r, "\n")
	return strings.TrimRight(r, "\n"), true
}

// The completed prefix and unfinished paragraph have different invalidation
// boundaries. In particular, a cursor blink changes neither. Keep their
// plain-text mirrors as well as their native rows; keep no history of tails.
type streamBodyCache struct {
	id, prefix, pending string
	md                  *rendering.MarkdownRenderer
	width               int
	head, tail          messageLines
}

func (c *streamBodyCache) project(source, id string, md *rendering.MarkdownRenderer, tokens *ui.TokenSet, width int) (messageLines, messageLines) {
	if c.md != md || c.id != id || c.width != width {
		*c = streamBodyCache{md: md, id: id, width: width}
	}
	end := strings.LastIndexByte(source, '\n') + 1
	prefix, pending := source[:end], source[end:]
	if prefix != c.prefix {
		rows, ok := renderMarkdownRows(prefix, md, tokens, id, true)
		if !ok && prefix != "" {
			rows = plainSourceLines(presentPlan(rendering.SanitizeForTerminal(prefix), false), width)
		}
		c.prefix, c.head = prefix, bodyProjection(rows)
	}
	if pending != c.pending {
		var rows []PaintLine
		if text := presentPlan(rendering.SanitizeForTerminal(pending), false); text != "" {
			rows = plainSourceLines(text, width)
		}
		c.pending, c.tail = pending, bodyProjection(rows)
	}
	return c.head, c.tail
}

func bodyProjection(rows []PaintLine) messageLines {
	lines := make([]string, len(rows))
	for i := range rows {
		lines[i] = rows[i].PlainText()
	}
	return messageLines{native: rows, lines: lines}
}

// presentPlan shows Plan mode's proposed_plan block as a titled
// section between rules: the tags are protocol for Crude, not prose. Live and
// hydrated rows both render through here, so they paint identically.
func presentPlan(content string, markdown bool) string {
	if !strings.Contains(content, "proposed_plan>") {
		return content
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case "<proposed_plan>":
			lines[index] = "Proposed plan"
			if markdown {
				lines[index] = "\n---\n\n**Proposed plan**\n"
			}
		case "</proposed_plan>":
			lines[index] = ""
			if markdown {
				lines[index] = "\n---\n"
			}
		}
	}
	return strings.Join(lines, "\n")
}

func renderMarkdownRows(content string, md *rendering.MarkdownRenderer, tokens *ui.TokenSet, messageID string, codeActions bool) ([]PaintLine, bool) {
	if md == nil || plainMarkdown(content) {
		return nil, false
	}
	p, err := md.RenderMapped(presentPlan(content, true))
	if err != nil {
		return nil, false
	}
	return BridgeSourceRows(p, tokens, messageID, codeActions), true
}

func plainMarkdown(content string) bool {
	return content != "" && strings.TrimSpace(content) == content && strings.IndexFunc(content, func(c rune) bool { return c != ' ' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') }) < 0
}

// Plain messages need no Markdown parser, but still retain source across wraps.
func plainSourceLines(content string, width int) []PaintLine {
	if bidi.NeedsLayout(content) {
		return plainParagraphRows(content, width)
	}
	if strings.ContainsAny(content, "\t\r\x1b") {
		var rows []PaintLine
		for _, line := range BridgeSourceRows(rendering.PlainProjection(content), nil, "", false) {
			rows = append(rows, wrapPaintLine(line, width)...)
		}
		return rows
	}
	source := &rendering.CopyText{Value: content}
	var segments []rendering.WrapRange
	base := 0
	for text := range strings.SplitSeq(content, "\n") {
		for _, r := range rendering.WordWrapRanges(text, max(1, width)) {
			segments = append(segments, rendering.WrapRange{Start: base + r.Start, End: base + r.End})
		}
		base += len(text) + 1
	}
	// A render owns one immutable row/span slab. Each row has disjoint,
	// capacity-fenced storage; no row text or maximum-size buffer is rebuilt.
	lines := make([]PaintLine, len(segments))
	spans := make([]RowSpan, len(segments))
	for i, r := range segments {
		spans[i] = RowSpan{Text: content[r.Start:r.End], Role: ui.ColorFg, Copy: rendering.CopyRange{Text: source, Start: r.Start, End: r.End}}
		lines[i] = PaintLine{SourceAware: true, Spans: spans[i : i+1 : i+1]}
	}
	return lines
}

// Unparsed chat is literal text, including while a code fence is unfinished.
// Wrap each hard line with the Markdown wrapper, preserving its source marks;
// use the same paragraph resolver as parsed prose, not a new bidi paragraph
// per screen row. The shared source still spans hard breaks for copy.
func plainParagraphRows(content string, width int) []PaintLine {
	source := &rendering.CopyText{Value: content}
	var rows []PaintLine
	base := 0
	for hard := range strings.SplitSeq(content, "\n") {
		projection := rendering.PlainProjection(hard)
		projection.ANSI = lipgloss.Wrap(projection.ANSI, max(1, width), "")
		part := BridgeSourceRows(projection, nil, "", false)
		var paragraph proseBuilder
		for i := range part {
			row := &part[i]
			row.prose, _ = paragraph.build(row.Spans, nil)
			row.setBlockWidth(width)
			for j := range row.Spans {
				r := &row.Spans[j].Copy
				if r.Valid() {
					r.Text, r.Start, r.End = source, base+r.Start, base+r.End
				}
			}
		}
		paragraph.finish()
		rows = append(rows, part...)
		base += len(hard) + 1
	}
	return rows
}
