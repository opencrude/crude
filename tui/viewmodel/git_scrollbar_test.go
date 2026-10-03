package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/activitybar"
	"crude/tui/screens/sourcecontrol"
	"crude/workspace"
)

// TestGitPanelWheelScrollingSurvivesShellFrame guards against scroll
// snap-back through the full shell: a wheel gesture on the git list must move
// the offset and a subsequent shell frame must not reset it to the selection.
func TestGitPanelWheelScrollingSurvivesShellFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree = NewTreeState(nil, 30)
	app.Tree.Visible = true
	app.width = 100
	app.height = 30
	app.setActivity(activitybar.ViewGit, true)

	changes := make([]workspace.GitChange, 0, 60)
	for i := 0; i < 60; i++ {
		changes = append(changes, workspace.GitChange{Path: "f.go", Area: workspace.GitAreaWorking, Code: "M"})
	}
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{Branch: "main", Changes: changes})

	app.paintShellFrame()

	listRect, ok := app.rootHost.ScreenRect(sourcecontrol.ListID)
	if !ok {
		t.Fatal("git list not registered in root host")
	}
	offset := gitListScrollOffset(app)

	before := offset()
	app.rootHost.Handle(tea.MouseWheelMsg{X: listRect.X + 1, Y: listRect.Y + 1, Button: tea.MouseWheelDown})
	afterWheel := offset()
	if afterWheel <= before {
		t.Fatalf("wheel did not scroll: offset before=%d after=%d", before, afterWheel)
	}

	// A fresh shell frame must keep the user's offset.
	app.paintShellFrame()
	afterFrame := offset()
	if afterFrame != afterWheel {
		t.Fatalf("SNAP-BACK: offset %d after wheel became %d after shell frame", afterWheel, afterFrame)
	}
}

// gitListScrollOffset returns a reader for the git list scroll offset.
func gitListScrollOffset(app *App) func() int {
	return func() int { return app.sourceControl.ScrollModel().Offset() }
}
