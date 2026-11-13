package chatprojection

import (
	"strings"

	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types"
)

func renderMarkdown(content string, mdRenderer *rendering.MarkdownRenderer) (string, bool) {
	if mdRenderer == nil {
		return "", false
	}
	content = presentProposedPlan(content)
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

func streamingCompletedRows(msg *types.ChatMessage, md *rendering.MarkdownRenderer, tokens *ui.TokenSet, width int) []PaintLine {
	end := strings.LastIndex(msg.Content, "\n") + 1
	if end == 0 {
		return nil
	}
	native, ok := renderMarkdownRows(msg.Content[:end], md, tokens, msg.InternalID, true)
	if !ok {
		native = buildPlainBodyLines(rendering.WrapText(msg.Content[:end], width))
	}
	return native
}

// presentProposedPlan shows Plan mode's proposed_plan block as a titled
// section between rules: the tags are protocol for Crude, not prose. Live and
// hydrated rows both render through here, so they paint identically.
func presentProposedPlan(content string) string {
	if !strings.Contains(content, "proposed_plan>") {
		return content
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case "<proposed_plan>":
			lines[index] = "\n---\n\n**Proposed plan**\n"
		case "</proposed_plan>":
			lines[index] = "\n---\n"
		}
	}
	return strings.Join(lines, "\n")
}

func renderMarkdownRows(content string, md *rendering.MarkdownRenderer, tokens *ui.TokenSet, messageID string, codeActions bool) ([]PaintLine, bool) {
	if md == nil || plainMarkdown(content) {
		return nil, false
	}
	p, err := md.RenderMapped(presentProposedPlan(content))
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
