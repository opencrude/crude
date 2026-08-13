package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/workspace"
)

func TestDiscardGitChangeUsesSafeConfirmation(t *testing.T) {
	app := newFullTestApp(t)
	change := workspace.GitChange{
		Path: "main.go", AbsolutePath: app.RootPath + "/main.go",
		Area: workspace.GitAreaWorking, Code: "M", Description: "Modified",
	}
	app.startDiscardGitChange(change)
	if !app.overlayHost.IsOpen(ui.OverlayTreeDelete) {
		t.Fatal("discard confirmation did not open")
	}
	if app.GitDiscardPending == nil || app.GitDiscardPending.Path != change.Path {
		t.Fatalf("pending=%#v", app.GitDiscardPending)
	}
	if app.TreeDeleteChoice != 1 {
		t.Fatalf("default choice=%d want Cancel", app.TreeDeleteChoice)
	}

	// Enter on the safe default cancels; no Git mutation command is started.
	app.handleTreeDeleteConfirmKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.overlayHost.IsOpen(ui.OverlayTreeDelete) || app.GitDiscardPending != nil {
		t.Fatal("cancel did not close and clear discard confirmation")
	}
}
