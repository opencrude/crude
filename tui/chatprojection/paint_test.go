package chatprojection_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestNativeSelectionReadablePreservesGraphemesAndReadableSyntax(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight(), theme.DefaultMidnight(), theme.DefaultWarm(), theme.DefaultHighContrast()} {
		tokens := ui.NewTokenSet(th)
		bg := tokens.Color(ui.ColorTextSelection)
		if cell.ContrastRatio(bg, tokens.Color(ui.ColorBg)) < 3 || cell.ContrastRatio(bg, tokens.Color(ui.ColorPopupBg)) < 3 || cell.ContrastRatio(bg, tokens.Color(ui.ColorSubPaneBg)) < 3 {
			t.Fatalf("%s: fill lacks contrast against text surfaces", th.Name)
		}
		readable := tokens.Color(ui.ColorTextSelectionFg)
		pl := chatprojection.PaintLine{Spans: []chatprojection.RowSpan{
			{Text: "界", FGSet: true, FGColor: readable},
			{Text: "e\u0301", FGSet: true, FGColor: bg, Attr: cell.AttrDim | cell.AttrReverse | cell.AttrBold},
		}}
		buf := surface.NewCellBuffer(6, 1)
		chatprojection.PaintLineAt(buf, 0, 0, 6, pl, tokens, 1, 5, true)
		if c := buf.Get(0, 0); c.Content != "界" || c.Width != 2 || c.FG != readable || c.BG != bg {
			t.Fatalf("%s: partial wide selection lost readable ink or grapheme: %+v", th.Name, c)
		}
		if c := buf.Get(2, 0); c.Content != "e\u0301" || c.FG != readable || c.BG != bg || c.Attr != cell.AttrBold {
			t.Fatalf("%s: unreadable syntax not corrected: %+v", th.Name, c)
		}
		if buf.Get(4, 0).BG != bg || buf.Get(5, 0).BG != tokens.Color(ui.ColorSubPaneBg) {
			t.Fatalf("%s: selected padding leaks outside range", th.Name)
		}
	}
}

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

func TestSubAgentReviewPaneShowsTerminalFailureInCollapsedAndExpandedForms(t *testing.T) {
	for _, tc := range []struct{ status, label, marker string }{
		{"failed_error", "failed", "×"},
		{"failed_timeout", "timed out", "×"},
		{"failed_quota", "quota exceeded", "×"},
		{"cancelled", "cancelled", "●"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			sp := workspace.NewSubPane("child", workspace.PaneAgent, "Lily")
			sp.Status = tc.status
			sp.Collapse()
			view := sp.View()
			for _, line := range []chatprojection.SubPaneLine{
				chatprojection.BuildSubPaneSummaryLine(view, 75),
				chatprojection.BuildSubPaneHeaderLine(view.Header, 75, 0),
			} {
				text := line.PlainText()
				if !strings.Contains(text, tc.label) || !strings.Contains(text, tc.marker) || strings.Contains(text, "failed_timeout") {
					t.Fatalf("child review status = %q", text)
				}
			}
		})
	}
}

func TestBuildSubPaneToolStates(t *testing.T) {
	messages := []*types.ChatMessage{
		{Role: types.RoleTool, ToolName: "read_file", ToolArgs: "ok", ToolPreviewPhase: "done", ToolStatus: "success"},
		{Role: types.RoleTool, ToolName: "write_file", ToolArgs: "fail", ToolPreviewPhase: "error", ToolStatus: "error"},
		{Role: types.RoleTool, ToolName: "read_file", ToolArgs: "next", ToolPreviewPhase: "streaming", ToolStatus: "streaming"},
	}
	r := chatprojection.NewRenderer(theme.DefaultDark())
	lines, active := r.BuildSubPaneToolLines(messages, 24, 0)
	if !active {
		t.Fatal("compact projection lost its active frame sensitivity")
	}
	if len(lines) != 4 {
		t.Fatalf("got %d tool lines", len(lines))
	}
	for i, l := range lines {
		if l.Native == nil {
			t.Fatalf("line %d not native", i)
		}
	}
	if !strings.Contains(lines[0].PlainText(), "●") {
		t.Fatalf("success line %q", lines[0].PlainText())
	}
	if !strings.Contains(lines[1].PlainText(), "×") {
		t.Fatalf("error line %q", lines[1].PlainText())
	}
	if !strings.Contains(lines[3].PlainText(), "●") {
		t.Fatalf("streaming line %q", lines[3].PlainText())
	}
	next, _ := r.BuildSubPaneToolLines(messages, 24, ui.ActivityBlinkPhaseMillis)
	if !strings.Contains(next[3].PlainText(), "○") || next[0].PlainText() != lines[0].PlainText() {
		t.Fatal("compact blink changed a completed row or failed to animate active work")
	}
	if lines[0].Native.Spans[2].Role != ui.ColorToolTitle || lines[1].Native.Spans[2].Role != ui.ColorError || lines[3].Native.Spans[2].Role != ui.ColorToolTitle {
		t.Fatal("compact tool titles bypassed shared semantic colors")
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
	selBG := tokens.Color(ui.ColorTextSelection)
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
	selBG := tokens.Color(ui.ColorTextSelection)
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
