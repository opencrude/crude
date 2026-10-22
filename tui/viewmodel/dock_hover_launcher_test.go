package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/termdock"
	"crude/workspace"
)

// Pointer motion over the dock reaches its controls in every shell state.
// The launcher used to drop motion outside its own body and the tree, so dock
// header buttons and panel rows never showed hover there.
func TestDockReceivesPointerMotionOverLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 110, 34
	app.ChatAreaHeight = 20
	app.BufferMgr = editor.NewBufferManager()
	app.EditorState = NewEditorState()
	wsOf(app).AgentMgr = workspace.NewAgentManager()
	app.AgentView = NewAgentViewState(app.ws.AgentMgr())
	app.launcherScreen.SetData(t.TempDir(), nil)
	app.toggleWorkspacePanel(termdock.PanelNotices)
	app.paintShellFrame()
	if !app.launcherHostVisible() || !app.termDock.IsOpen() {
		t.Fatal("precondition: launcher and dock visible")
	}
	dock, ok := app.rootHost.ScreenRect(termdock.ScreenID)
	if !ok || dock.Empty() {
		t.Fatal("dock not mounted")
	}
	_, _ = app.Update(tea.MouseMotionMsg{X: dock.X + dock.W/2, Y: dock.Y + dock.H - 2})
	if hovered := string(app.rootHost.Hovered()); !strings.HasPrefix(hovered, string(termdock.ScreenID)) && !strings.HasPrefix(hovered, "status-overview") {
		t.Fatalf("motion over the dock hovered %q", hovered)
	}
}
