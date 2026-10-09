package viewmodel

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/workspace"
)

func TestStaleTerminalMessagesCannotTargetReplacementManagerSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newTestApp(t)
	oldInfo, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    20,
		Rows:    8,
		Title:   "old",
	})
	if err != nil {
		t.Fatalf("create old terminal: %v", err)
	}
	oldService := wsOf(app).Terminals
	oldService.CloseAll()
	app.discardTerminalUI()
	app.workspaceSwitchGen.Add(1)

	newMgr := workspace.NewTerminalManager(t.TempDir())
	wsOf(app).TermMgr = newMgr
	wsOf(app).Terminals = nil
	newInfo, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    20,
		Rows:    8,
		Title:   "new",
	})
	if err != nil {
		t.Fatalf("create replacement terminal: %v", err)
	}
	t.Cleanup(func() {
		app.dropTermScreen(newInfo.ID)
		newMgr.CloseAll()
	})
	if oldInfo.ID == newInfo.ID {
		t.Fatalf("replacement manager reused terminal ID %q", oldInfo.ID)
	}

	newScreen := app.ensureTermScreen(newInfo.ID, newInfo.Title)

	// These represent messages already queued by the old manager when the
	// workspace replacement happened. None may materialize an old screen or
	// target the replacement manager's terminal.
	_ = app.applyTerminalProjection(terminalProjectionMsg{service: oldService, projection: workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: oldInfo.ID, Title: "stale-output"}}})
	app.applyTerminalTitle(oldInfo.ID, "stale-title")
	app.handleTerminalExited(workspace.TerminalExitedMsg{ID: oldInfo.ID, ExitCode: 9, Err: "stale-exit"})

	if app.termScreens[oldInfo.ID] != nil {
		t.Fatal("stale output materialized a screen from the replacement manager")
	}
	if got := newScreen.Title(); got != "new" {
		t.Fatalf("stale old message mutated replacement title to %q", got)
	}
	result := newScreen.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'x', Text: "x"}))
	runTerminalCommand(t, app, result.Cmd)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		projection, err := app.ws.Terminals().Rows(newInfo.ID, -1, 0)
		if err == nil && strings.Contains(terminalProjectionText(projection), "x") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("replacement terminal stopped accepting input after stale exit")
}
