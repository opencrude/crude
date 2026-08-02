package chatprojection_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestBuildSubPaneHeaderThinkingNative(t *testing.T) {
	header := workspace.SubPaneHeaderView{
		Type:    workspace.PaneThinking,
		Title:   "Thinking",
		Done:    false,
		Elapsed: 3 * time.Second,
	}
	line := chatprojection.BuildSubPaneHeaderLine(header, 30, 2)
	if line.Native == nil {
		t.Fatal("expected native header line")
	}
	if got := line.PlainText(); !strings.Contains(got, "Thinking") {
		t.Fatalf("plain text %q missing title", got)
	}
	if w := line.Native.VisibleWidth(); w != 30 {
		t.Fatalf("header width=%d want 30", w)
	}
}

func TestBuildSubPaneToolStates(t *testing.T) {
	view := workspace.SubPaneView{
		ToolLines: []workspace.SubPaneToolLineView{
			{Label: "read ok", Phase: "done", Status: "success"},
			{Label: "write fail", Phase: "error", Status: "error"},
			{Label: "streaming", Phase: "streaming", Status: "streaming"},
		},
	}
	lines := chatprojection.BuildSubPaneToolLines(view, 24)
	if len(lines) != 3 {
		t.Fatalf("got %d tool lines", len(lines))
	}
	for i, l := range lines {
		if l.Native == nil {
			t.Fatalf("line %d not native", i)
		}
	}
	if !strings.Contains(lines[0].PlainText(), "✓") {
		t.Fatalf("success line %q", lines[0].PlainText())
	}
	if !strings.Contains(lines[1].PlainText(), "✗") {
		t.Fatalf("error line %q", lines[1].PlainText())
	}
	if !strings.Contains(lines[2].PlainText(), "●") {
		t.Fatalf("streaming line %q", lines[2].PlainText())
	}
}

func TestPaintLineSelectionAndBackground(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	pl := chatprojection.PaintLine{Spans: []chatprojection.RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: "● hello", Role: ui.ColorAccent},
	}}
	buf := surface.NewCellBuffer(20, 1)
	chatprojection.PaintLineAt(buf, 0, 0, 20, pl, tokens, 3, 7, true)
	selBG := tokens.Color(ui.ColorSelection)
	accentBG := tokens.Color(ui.ColorAccent)
	subBG := tokens.Color(ui.ColorSubPaneBg)
	if c := buf.Get(3, 0); c.BG != selBG {
		t.Fatalf("col 3 bg=%v want selection", c.BG)
	}
	// Regression: text selection must never resolve to the focused-row Accent token.
	if c := buf.Get(3, 0); c.BG.Equal(accentBG) {
		t.Fatalf("col 3 bg is accent — text selection must use Selection, not ListRowColors(StateFocused)")
	}
	if c := buf.Get(0, 0); c.BG != subBG {
		t.Fatalf("col 0 bg=%v want subpane bg", c.BG)
	}
	if c := buf.Get(15, 0); c.Char != ' ' || c.BG != subBG {
		t.Fatalf("padding cell=%+v", c)
	}
}

func TestPaintLineSelectionNeverUsesAccent(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	selBG := tokens.Color(ui.ColorSelection)
	accentBG := tokens.Color(ui.ColorAccent)
	if selBG.Equal(accentBG) {
		t.Fatal("theme Selection and Accent are identical — cannot assert distinction")
	}
	pl := chatprojection.PaintLine{Spans: []chatprojection.RowSpan{
		{Text: "selected text", Role: ui.ColorFg},
	}}
	buf := surface.NewCellBuffer(20, 1)
	chatprojection.PaintLineAt(buf, 0, 0, 20, pl, tokens, 0, 8, false)
	for col := 0; col < 8; col++ {
		c := buf.Get(col, 0)
		if c.BG.Equal(accentBG) {
			t.Fatalf("col %d bg is accent; text selection must use Selection", col)
		}
		if !c.BG.Equal(selBG) {
			t.Fatalf("col %d bg=%v want Selection", col, c.BG)
		}
	}
}

func TestTruncatePaintLinePreservesRoles(t *testing.T) {
	pl := chatprojection.PaintLine{Spans: []chatprojection.RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: "long tool label", Role: ui.ColorSuccess},
	}}
	out := chatprojection.TruncatePaintLine(pl, 8)
	if out.VisibleWidth() > 8 {
		t.Fatalf("truncated width=%d", out.VisibleWidth())
	}
	if len(out.Spans) < 2 {
		t.Fatalf("expected border span preserved, got %#v", out.Spans)
	}
}

func TestSubpaneRenderGuardNoLipgloss(t *testing.T) {
	data, err := os.ReadFile("../viewmodel/subpane_render.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "lipgloss") || strings.Contains(line, `\x1b[`) {
			t.Fatalf("subpane_render.go must not use lipgloss/raw SGR: %s", strings.TrimSpace(line))
		}
	}
}
