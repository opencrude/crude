package viewmodel

import (
	"runtime"
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

func TestTerminalLateReadCannotReopenClosedScreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY workload")
	}
	app := newFullTestApp(t)
	info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{Command: "/bin/cat", Cols: 20, Rows: 4})
	if err != nil {
		t.Fatal(err)
	}
	app.dockTerminal(info.ID)
	msg := app.terminalCommand(shellterm.Command{ID: info.ID, Kind: shellterm.CommandReadRows, Start: -1})()
	app.closeTerminal(info.ID)
	_, next := app.Update(msg)
	runTerminalCommand(t, app, next)
	if app.termScreens[info.ID] != nil || app.termDock.Contains(info.ID) || app.terminalLeases[info.ID].Generation != 0 {
		t.Fatal("late successful projection resurrected closed terminal")
	}
}
