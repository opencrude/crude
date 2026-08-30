package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

// Regression: plain tool bodies used to character-wrap (HardWrap), which could
// break mid-token and strand a 1–2 character orphan at the right edge of the
// 4-column gutter band — a fragment like "zqx" dangling after "{". Bodies must
// now break at word boundaries (with a hard-break fallback for tokenless runs)
// and every painted segment must stay inside the band width.
func TestPlainToolBodyWordWrapsAtWordBoundaries(t *testing.T) {
	long := "func (p *LayoutPainter) paintRows(buf *Buffer) { zqx.TokenPair"
	runless := strings.Repeat("x", 80) // no whitespace: exercises hard-break fallback
	msg := types.ChatMessage{
		Role: "tool", ToolName: "run_javascript", ToolStatus: "success",
		ToolPreviewPhase: "done",
		Content:          long + "\n" + runless,
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 56, 56, 56, 0, 20, "done", DisclosureExpanded)
	band := 56 - toolBodyGutterWidth
	for i, pl := range ml.native[1:] { // skip the title row
		for si, span := range pl.Spans {
			if si == 0 { // gutter span
				continue
			}
			if w := rendering.VisibleWidth(span.Text); w > band {
				t.Fatalf("body row %d span overflows band: %d > %d: %q", i, w, band, span.Text)
			}
			if strings.HasSuffix(span.Text, "zqx") {
				t.Fatalf("mid-token orphan at row end: %q", span.Text)
			}
		}
	}
	var tokenRow string
	for _, row := range ml.lines[1:] {
		if strings.TrimSpace(strings.TrimPrefix(row, "│   ")) == "zqx.TokenPair" {
			tokenRow = row
		}
	}
	if tokenRow == "" {
		t.Fatalf("qualified identifier split across rows:\n%s", strings.Join(ml.lines, "\n"))
	}
}

// Collapsed previews clip wrapped rows behind "+N more lines"; a mid-word
// break used to leave the fragment as the last visible cell in the band.
func TestCollapsedToolPreviewWrapsInsideBand(t *testing.T) {
	content := strings.Join([]string{
		strings.Repeat("m", 50) + " tailone",
		strings.Repeat("n", 50) + " tailtwo",
		"final line here",
	}, "\n")
	msg := types.ChatMessage{
		Role: "tool", ToolName: "grep_files", ToolStatus: "success",
		ToolPreviewPhase: "done", Content: content,
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 56, 56, 56, 0, 20, "done", DisclosureCollapsed)
	band := 56 - toolBodyGutterWidth
	plain := strings.Join(ml.lines, "\n")
	if !strings.Contains(plain, "more lines") {
		t.Fatalf("collapsed preview missing truncation marker:\n%s", plain)
	}
	if !strings.Contains(plain, "tailone") {
		t.Fatalf("first wrapped tail missing:\n%s", plain)
	}
	for i, pl := range ml.native[1:] {
		for si, span := range pl.Spans {
			if si == 0 {
				continue
			}
			if w := rendering.VisibleWidth(span.Text); w > band {
				t.Fatalf("collapsed row %d span overflows band: %d > %d: %q", i, w, band, span.Text)
			}
		}
	}
}
