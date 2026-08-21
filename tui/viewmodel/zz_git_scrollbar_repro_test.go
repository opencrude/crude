package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/activitybar"
	"crude/tui/ui"
	"crude/workspace"
)

func TestReproGitPanelScrollbarInShell(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree = NewTreeState(nil, 30)
	app.Tree.Visible = true
	app.width = 100
	app.height = 30

	// switch to git activity
	app.setActivity(activitybar.ViewGit, true)
	app.activityView = activityGit

	// snapshot with many changes
	changes := make([]workspace.GitChange, 0, 60)
	for i := 0; i < 60; i++ {
		changes = append(changes, workspace.GitChange{Path: "f.go", Area: workspace.GitAreaWorking, Code: "M"})
	}
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{Branch: "main", Changes: changes})

	app.paintShellFrame()

	// verify sidebar slot has sourcecontrol mounted
	if got := app.shell.slots[shellSidebarSlot].active; !got {
		t.Fatal("sidebar slot inactive")
	}
	rect, ok := app.rootHost.ScreenRect(sourcecontrolScrollbarIDForTest())
	if !ok {
		t.Fatal("git scrollbar not registered in root host")
	}
	t.Logf("git scrollbar screen rect=%v", rect)
	hit := app.rootHost.HitTest(rect.X, rect.Y+4)
	t.Logf("hit=%q", hit)

	// click on the scrollbar track
	res := app.rootHost.Handle(tea.MouseClickMsg{X: rect.X, Y: rect.Y + 4, Button: tea.MouseLeft})
	t.Logf("click handled=%v captured=%v", res.Handled, app.rootHost.HasPointerCapture())

	// wheel over the git list
	offBefore := app.sourceControl.Snapshot()
	_ = offBefore
	_ = ui.RootID
}

// sourcecontrolScrollbarIDForTest returns the scrollbar component id of the git screen.
func sourcecontrolScrollbarIDForTest() ui.ComponentID {
	return "sourcecontrol/scrollbar"
}
