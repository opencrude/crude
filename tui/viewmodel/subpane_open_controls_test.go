package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/types"
)

// findOnScreen returns the screen cell where text starts in the painted frame.
func findOnScreen(t *testing.T, app *App, text string) (int, int, bool) {
	t.Helper()
	buf := capturePaintedShell(t, app)
	needle := []rune(text)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x+len(needle) <= buf.Width(); x++ {
			match := true
			for i, r := range needle {
				if buf.Get(x+i, y).Char != r {
					match = false
					break
				}
			}
			if match {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func clickAt(app *App, x, y int) {
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	_ = app.View()
}

// A sub-agent starts minimized; its pane in the parent transcript carries the
// explicit controls that open it as a main tab or as its own window, live and
// after it finishes.
func TestSubAgentPaneOpensAsTabOrWindow(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Tree.Visible = false
	_, _ = app.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	app.AddOrchestrator("root")
	root := app.ws.AgentMgr().Get("root").State.Messages
	root.UpdateToolCall("spawn-1", "tool-1", "agent_start", "category: researcher")
	app.handleAgentEvent(types.AgentEventMsg{Type: "sub_agent_started", AgentID: "child", Data: map[string]string{
		"agent_id": "child", "agent_name": "Lily", "category": "researcher", "task": "Inspect recovery", "parent_id": "root", "parent_tool_id": "spawn-1",
	}})
	_ = app.View()
	if app.Win.TabByTitle(app.agentTabTitle("child")) != nil {
		t.Fatal("a started sub-agent opened a tab by itself")
	}

	x, y, ok := findOnScreen(t, app, "⇱ window")
	if !ok {
		t.Fatalf("running sub-agent pane has no window control:\n%s", bufferPlain(capturePaintedShell(t, app)))
	}
	clickAt(app, x+2, y)
	tab := app.Win.TabByTitle(app.agentTabTitle("child"))
	if tab == nil || !app.Win.IsFloating(tab.Group) || !app.overlayHost.IsOpen(paneWindowID(tab.Group)) {
		t.Fatalf("window control did not open the child in a window: %+v", tab)
	}
	app.dockPaneWindow(tab.Group)
	app.minimizeAgent("child")
	_ = app.View()
	if app.Win.TabByTitle(app.agentTabTitle("child")) != nil {
		t.Fatal("minimize kept the child tab")
	}

	app.handleAgentEvent(types.AgentEventMsg{Type: "sub_agent_completed", AgentID: "child", Data: map[string]string{
		"agent_id": "child", "agent_name": "Lily", "status": "completed",
	}})
	_ = app.View()
	x, y, ok = findOnScreen(t, app, "⧉ tab")
	if !ok {
		t.Fatalf("finished sub-agent summary has no tab control:\n%s", bufferPlain(capturePaintedShell(t, app)))
	}
	clickAt(app, x+1, y)
	tab = app.Win.TabByTitle(app.agentTabTitle("child"))
	if tab == nil || app.Win.IsFloating(tab.Group) {
		t.Fatalf("tab control did not open the child as a main tab: %+v", tab)
	}
}
