package viewmodel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
	"crude/workspace"
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

func TestCompactChildPaneUpdatesRetainUnchangedRows(t *testing.T) {
	pane := workspace.NewSubPane("child", workspace.PaneAgent, "Child")
	pane.Collapse() // a fixed header duration for comparison with a fresh render
	reg := newSubPaneProjectionRegistry(theme.DefaultDark()).ForOwner("parent")
	for _, name := range []string{"first", "second", "third"} {
		pane.Messages.AppendMessage(conversation.Message{
			InternalID: name, Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: name + ".go",
			ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone, Content: "package " + name,
		})
	}
	pane.Messages.AppendMessage(conversation.Message{InternalID: "text", Role: conversation.RoleAssistant, Content: "Reviewing"})
	width := 72
	render := func() []chatprojection.SubPaneLine {
		return reg.Render(pane.ID, pane, width, 20, 0, true)
	}
	apply := func(delta workspace.ConversationDelta) {
		delta.BaseRevision = pane.Messages.Version()
		delta.Revision = delta.BaseRevision + 1
		if err := pane.Messages.InstallDelta(delta); err != nil {
			t.Fatal(err)
		}
	}
	compareFresh := func(rows []chatprojection.SubPaneLine, expanded ...string) {
		t.Helper()
		fresh := newSubPaneProjectionRegistry(theme.DefaultDark()).ForOwner("fresh-parent")
		proj := fresh.Get(pane.ID, pane.Type)
		fresh.SyncFromWorkspace(pane.ID, pane.Messages, false)
		for _, id := range expanded {
			proj.renderer.ToggleDisclosure(id)
		}
		want := fresh.Render(pane.ID, pane, width, 20, 0, true)
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("live pane differs from fresh pane: got %v; want %v", rows, want)
		}
	}
	initial := render()
	if len(initial) != 4 {
		t.Fatalf("want header and three tools; got %d rows", len(initial))
	}
	msg, _ := pane.Messages.Message("second")
	msg.ToolStatus, msg.ToolPreviewPhase, msg.Content = conversation.ToolStatusError, conversation.ToolPreviewError, "File is missing."
	apply(workspace.ConversationDelta{Upserts: []conversation.Message{msg}})
	changed := render()
	compareFresh(changed)
	if initial[1].Native != changed[1].Native || initial[3].Native != changed[3].Native {
		t.Fatal("one changed result rebuilt unchanged compact tool rows")
	}
	if reflect.DeepEqual(initial[2].Native, changed[2].Native) {
		t.Fatal("changed result still paints the previous success row")
	}
	pane.Messages.AppendToken(" more")
	text := render()
	for i := 1; i < len(text); i++ {
		if text[i].Native != changed[i].Native {
			t.Fatalf("assistant text rebuilt compact tool row %d", i)
		}
	}

	reg.Get(pane.ID, pane.Type).renderer.ToggleDisclosure("second")
	opened := render()
	compareFresh(opened, "second")
	if len(opened) <= len(text) || opened[1].Native != text[1].Native || opened[len(opened)-1].Native != text[3].Native {
		t.Fatal("opening one tool must reveal its evidence and retain both neighbors")
	}
	reg.Get(pane.ID, pane.Type).renderer.ToggleDisclosure("second")
	closed := render()
	compareFresh(closed)

	width = 34
	narrow := render()
	compareFresh(narrow)
	if narrow[1].Native == closed[1].Native || narrow[1].Native.VisibleWidth() != width {
		t.Fatal("compact tool row did not reflow at the new width")
	}
	apply(workspace.ConversationDelta{Removed: []string{"second"}, Order: []string{"third", "first", "text"}})
	reordered := render()
	compareFresh(reordered)
	// The middle failure can wrap at width 34; the third tool is the
	// last painted row, not necessarily the third body line.
	if len(reordered) != 3 || reordered[1].Native != narrow[len(narrow)-1].Native || reordered[2].Native != narrow[1].Native {
		t.Fatalf("removing and reordering tools must keep only the two surviving rows in their new order: narrow=%v reordered=%v", narrow, reordered)
	}
}

func TestCompletedChildPaneRefreshesOnlyPendingDiffInk(t *testing.T) {
	pane := workspace.NewSubPane("child", workspace.PaneAgent, "Child")
	pane.Collapse()
	reg := newSubPaneProjectionRegistry(theme.DefaultDark()).ForOwner("parent")
	var patch strings.Builder
	patch.WriteString("```diff\n--- a/check.go\n+++ b/check.go\n@@ -0,0 +1,100 @@\n")
	seed := time.Now().UnixNano()
	for i := range 100 {
		fmt.Fprintf(&patch, "+func pendingHelper%d_%d(value int) int { return value + 1 }\n", seed, i)
	}
	patch.WriteString("```\n")
	for _, msg := range []conversation.Message{
		{Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: "neighbor.go", ToolStatus: conversation.ToolStatusSuccess},
		{Role: conversation.RoleTool, ToolName: "apply_patch", ToolArgs: "check.go", ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone, Content: patch.String()},
	} {
		pane.Messages.AppendMessage(msg)
	}
	render := func() []chatprojection.SubPaneLine { return reg.Render(pane.ID, pane, 110, 1024, 0, true) }
	colored := func(rows []chatprojection.SubPaneLine) bool {
		for _, row := range rows {
			for _, span := range row.Native.Spans {
				if span.FGSet && strings.Contains(span.Text, "pendingHelper") {
					return true
				}
			}
		}
		return false
	}
	first := render()
	if len(first) < 100 || !first[2].FrameSensitive || colored(first) {
		t.Fatal("completed child did not paint its pending diff immediately with fallback ink")
	}
	deadline := time.Now().Add(10 * time.Second)
	for chatprojection.DiffSyntaxBusy() {
		if time.Now().After(deadline) {
			t.Fatal("diff coloring did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	last := render()
	if len(last) != len(first) || last[1].Native != first[1].Native || last[2].FrameSensitive || !colored(last) {
		t.Fatal("finished lexer must color only the pending diff and preserve its geometry")
	}
	repeat := render()
	for i := 1; i < len(last); i++ {
		if first[i].Text != last[i].Text || repeat[i].Native != last[i].Native {
			t.Fatalf("colored diff changed text or did not stay cached at row %d", i)
		}
	}
}
