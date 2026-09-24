package viewmodel

import (
	"testing"

	"crude/tui/screens/shellterm"
	"crude/workspace"
)

func TestTerminalExitRemovesInstanceFromManagerAndDock(t *testing.T) {
	app := newFullTestApp(t)
	app.termScreens["dead"] = shellterm.New(app.Theme, "dead", "dead")
	app.termDock.Mount("dead", app.termScreens["dead"])
	app.handleTerminalExited(workspace.TerminalExitedMsg{ID: "dead", ExitCode: 0})
	if app.termDock.Contains("dead") || app.termScreens["dead"] != nil {
		t.Fatal("natural terminal exit left a stale dock instance")
	}
}
