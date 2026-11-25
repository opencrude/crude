package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/ui"
	"crude/types/conversation"
)

func TestDenseChildPanePaintRetainsSettledRows(t *testing.T) {
	app, panes := denseChildPanes(t, 2000)
	reg, pane := app.subPaneProjections, panes[0]
	on := reg.Render(pane.ID, pane, 80, 8, 0, true)
	off := reg.Render(pane.ID, pane, 80, 8, ui.ActivityBlinkPhaseMillis, true)
	if len(on) != 8 || len(off) != len(on) {
		t.Fatalf("blink changed visible height: %d -> %d", len(on), len(off))
	}
	if !strings.Contains(on[7].PlainText(), "●") || !strings.Contains(off[7].PlainText(), "○") {
		t.Fatalf("last tool did not change activity phase: %q -> %q", on[7].PlainText(), off[7].PlainText())
	}
	for i := 1; i < 7; i++ {
		if !strings.Contains(off[i].PlainText(), "Read main.go") || on[i].Native != off[i].Native || on[i].PaneInfo.LineIdx != off[i].PaneInfo.LineIdx {
			t.Fatalf("blink rebuilt or moved settled row %d: %q -> %q", i, on[i].PlainText(), off[i].PlainText())
		}
	}
	pane.Messages.AppendMessage(conversation.Message{Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: `{"path":"next.go"}`, ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone})
	app.syncAgentMessages(pane.ID, false)
	next := reg.Render(pane.ID, pane, 80, 8, ui.ActivityBlinkPhaseMillis, true)
	if !strings.Contains(next[7].PlainText(), "Read next.go") {
		t.Fatalf("updated source not visible at the bottom: %q", next[7].PlainText())
	}
}
