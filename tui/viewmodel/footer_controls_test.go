package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	ttypes "crude/types"
)

func TestFooterControlsContainOnlyPanelToggles(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 28
	app.AddOrchestrator("root")

	segments := app.footerStatusSegments()
	wantActions := []string{"problems", "notices", "terminals", "git", "board", "memory", "workspace"}
	if len(segments) != len(wantActions) {
		t.Fatalf("footer status group has %d segments, want %d: %+v", len(segments), len(wantActions), segments)
	}
	for index, segment := range segments {
		if !segment.Pill || segment.Action != wantActions[index] {
			t.Fatalf("footer control %d=%+v want action %q", index, segment, wantActions[index])
		}
	}
	wantFills := []ui.SemanticColor{ui.ColorAccent2, ui.ColorAccent, ui.ColorDiffHunk, ui.ColorDiffFile, ui.ColorAccent2, ui.ColorMuted, ui.ColorAccent}
	for index, segment := range segments {
		if segment.FillRole != wantFills[index] {
			t.Fatalf("control %s lost its vivid hue: %v", segment.Text, segment.FillRole)
		}
	}
	if segments[len(segments)-1].Priority >= segments[0].Priority {
		t.Fatal("Workspace should survive before status tools on narrow screens")
	}
}

func TestFooterControlsMarkOnlyTheOpenView(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 28
	app.AddOrchestrator("root")
	app.toggleWorkspacePanel(termdock.PanelProblems)
	active := ""
	for _, segment := range app.footerStatusSegments() {
		if segment.Active {
			if active != "" {
				t.Fatal("multiple footer controls marked active")
			}
			active = segment.Action
		}
	}
	if active != "problems" {
		t.Fatalf("active footer control=%q want problems", active)
	}
}

func TestFooterControlsHavePersistentFillsAndDistinctActiveState(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 28
	app.AddOrchestrator("root")
	app.toggleWorkspacePanel(termdock.PanelProblems)
	app.paintShellFrame()

	y := app.height - 1
	buf := app.mainSurf.Buffer()
	row := rowText(app, y)
	tokens := ui.NewTokenSet(app.Theme)
	for _, segment := range app.footerStatusSegments() {
		x := strings.Index(row, segment.Text)
		if x < 0 {
			t.Fatalf("missing footer control %q in %q", segment.Text, row)
		}
		x = rendering.VisibleWidth(row[:x])
		fg, bg := tokens.StatusStripColors(segment.FillRole, segment.Active, segment.Attention)
		painted := buf.Get(x, y)
		if !painted.BG.Equal(bg) || !painted.FG.Equal(fg) {
			t.Fatalf("%s has incorrect fill/ink: %+v", segment.Text, painted)
		}
		if segment.Active && painted.Attr&cell.AttrBold == 0 {
			t.Fatalf("active control needs bold ink: %+v", painted)
		}
	}
	if strings.Contains(row, "Previous Sessions") {
		t.Fatalf("session command leaked back into footer: %q", row)
	}
}

func TestFooterColorsFollowAttentionInsteadOfPermanentAlarms(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	idle := app.footerStatusSegments()
	if idle[0].FillRole == ui.ColorError || idle[0].FillRole == ui.ColorWarning || idle[0].Attention {
		t.Fatal("healthy Problems must not paint an alarm")
	}
	tokens := ui.NewTokenSet(app.Theme)
	_, quiet := tokens.StatusStripColors(idle[1].FillRole, idle[1].Active, idle[1].Attention)
	noticeID := app.Status.Notify(ttypes.NotificationRecord{Summary: "Build finished", Source: "test"})
	unread := app.footerStatusSegments()[1]
	_, alert := tokens.StatusStripColors(unread.FillRole, unread.Active, unread.Attention)
	if !unread.Attention || quiet.Equal(alert) {
		t.Fatal("unread notice did not strengthen its fill")
	}
	app.Status.MarkRead(noticeID, true)
	if app.footerStatusSegments()[1].Attention {
		t.Fatal("read notice retained attention fill")
	}
	app.Status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "background"}, Phase: ttypes.AgentTool})
	working := app.footerStatusSegments()[6]
	if !working.Attention || working.FillRole != ui.ColorAccent2 {
		t.Fatal("background work did not change Workspace color")
	}
	app.Status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "background"}, Phase: ttypes.AgentQuestion, NeedsAction: true})
	if needsInput := app.footerStatusSegments()[6]; !needsInput.Attention || needsInput.FillRole != ui.ColorWarning {
		t.Fatal("genuine input request did not change Workspace color")
	}
}

func TestBoardMovedFromAgentsSidebarToFooter(t *testing.T) {
	app := newFullTestApp(t)
	for _, item := range app.agentSidebarItems() {
		if item.ID == "board.open" || item.Label == "Board" {
			t.Fatalf("Board remained in Agents sidebar: %#v", item)
		}
	}

	var boardFound bool
	for _, segment := range app.footerStatusSegments() {
		if segment.Action == "board" {
			boardFound = segment.Text == "Board" && segment.Pill
		}
	}
	if !boardFound {
		t.Fatal("footer has no Board panel control")
	}

	_, command := app.update(shellfooter.ActionMsg{Action: "board"})
	if command == nil || app.termDock == nil || !app.termDock.IsOpen() || app.termDock.ActivePanel() != termdock.PanelBoard {
		t.Fatal("footer Board control did not open the retained Board panel")
	}
	_, command = app.update(shellfooter.ActionMsg{Action: "board"})
	if command != nil || app.termDock.IsOpen() {
		t.Fatal("active footer Board control did not hide the panel")
	}
}
