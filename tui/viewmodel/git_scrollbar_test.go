package viewmodel

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitpanel"
	"crude/tui/screens/termdock"
	"crude/workspace"
)

// TestGitPanelWheelScrollingSurvivesShellFrame guards against scroll
// snap-back through the full shell: a wheel gesture on the docked change list
// must move the offset and a subsequent shell frame must not reset it.
func TestGitPanelWheelScrollingSurvivesShellFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	changes := make([]workspace.GitChange, 0, 60)
	for i := 0; i < 60; i++ {
		changes = append(changes, workspace.GitChange{Path: fmt.Sprintf("f%02d.go", i), Area: workspace.GitAreaWorking, Code: "M"})
	}
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{Branch: "main", Changes: changes})
	app.toggleWorkspacePanel(termdock.PanelGit)
	app.paintShellFrame()

	listRect, ok := app.rootHost.ScreenRect(gitpanel.ListID)
	if !ok || listRect.Empty() {
		t.Fatal("docked git change list not registered in root host")
	}
	before := app.gitPanel.ChangeListOffset()
	app.rootHost.Handle(tea.MouseWheelMsg{X: listRect.X + 1, Y: listRect.Y + 1, Button: tea.MouseWheelDown})
	afterWheel := app.gitPanel.ChangeListOffset()
	if afterWheel <= before {
		t.Fatalf("wheel did not scroll: offset before=%d after=%d", before, afterWheel)
	}
	app.paintShellFrame()
	if afterFrame := app.gitPanel.ChangeListOffset(); afterFrame != afterWheel {
		t.Fatalf("SNAP-BACK: offset %d after wheel became %d after shell frame", afterWheel, afterFrame)
	}
}
