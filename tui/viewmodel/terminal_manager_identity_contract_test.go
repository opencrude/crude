package viewmodel

import (
	"runtime"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/workspace"
)

func TestStaleTerminalMessagesCannotTargetReplacementManagerSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newTestApp(t)
	oldMgr := app.termMgr()
	oldInfo, err := oldMgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    20,
		Rows:    8,
		Title:   "old",
	})
	if err != nil {
		t.Fatalf("create old terminal: %v", err)
	}
	oldMgr.CloseAll()

	newMgr := workspace.NewTerminalManager(t.TempDir())
	app.Workspace.TermMgr = newMgr
	newInfo, err := newMgr.Create(workspace.TerminalCreateOpts{
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
	wrote := ""
	newScreen.SetWrite(func(data []byte) { wrote += string(data) })

	// These represent messages already queued by the old manager when the
	// workspace replacement happened. None may materialize an old screen or
	// target the replacement manager's terminal.
	_ = app.handleTerminalOutput(workspace.TerminalOutputMsg{
		ID:   oldInfo.ID,
		Data: []byte("\x1b]2;stale-output\x07"),
	})
	app.applyTerminalTitle(oldInfo.ID, "stale-title")
	app.handleTerminalExited(workspace.TerminalExitedMsg{ID: oldInfo.ID, ExitCode: 9, Err: "stale-exit"})

	if app.termScreens[oldInfo.ID] != nil {
		t.Fatal("stale output materialized a screen from the replacement manager")
	}
	if got := newScreen.Title(); got != "new" {
		t.Fatalf("stale old message mutated replacement title to %q", got)
	}
	newScreen.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'x', Text: "x"}))
	if wrote != "x" {
		t.Fatalf("replacement terminal stopped accepting input after stale exit: wrote=%q", wrote)
	}
}
