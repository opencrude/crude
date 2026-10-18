package viewmodel

import (
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"crude/workspace"
)

func TestTerminalSummonShortcutsRestoreLastUsedAcrossDockAndWindow(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	runTerminalCommand(t, app, func() tea.Msg { return tea.WindowSizeMsg{Width: 120, Height: 40} })
	opts := workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"300"}, CWD: t.TempDir(), Cols: 60, Rows: 8}
	if runtime.GOOS == "windows" {
		opts.Command = "powershell.exe"
		opts.Args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 300"}
	}
	first, err := createAppTerminal(t, app, opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := createAppTerminal(t, app, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.discardTerminalUI)
	app.dockTerminal(second.ID)
	app.dockTerminal(first.ID)
	app.paintShellFrame()
	for _, floating := range []bool{false, true} {
		if floating {
			app.promoteTerminal(first.ID)
			app.paintShellFrame()
		}
		for _, key := range []tea.KeyPressMsg{{Code: '`', Mod: tea.ModCtrl}, {Code: 't', Mod: tea.ModCtrl | tea.ModAlt}, {Code: tea.KeyF12, Mod: tea.ModAlt}} {
			_, cmd := app.Update(key)
			runTerminalCommand(t, app, cmd)
			app.paintShellFrame()
			if app.terminalOwnsKeys() {
				t.Fatal("summon did not release terminal focus")
			}
			_, cmd = app.Update(key)
			runTerminalCommand(t, app, cmd)
			app.paintShellFrame()
			if !app.terminalOwnsKeys() || app.lastTerminalID != first.ID {
				t.Fatal("summon selected newest-created instead of last-used PTY")
			}
			if len(app.termMgr().List()) != 2 {
				t.Fatal("summon created duplicate PTY")
			}
		}
	}
	// Physical Shift+backtick often arrives as '~'; registry must accept both.
	for _, key := range []tea.KeyPressMsg{{Code: '`', Mod: tea.ModCtrl | tea.ModShift}, {Code: '~', Mod: tea.ModCtrl | tea.ModShift}} {
		if action := app.actionRegistry.MatchGlobal(key); action == nil || action.ID != "terminal.new" {
			t.Fatalf("new-terminal chord not recognized: %#v", key)
		}
	}
}

func TestTerminalSummonCreatesOnceAndNeverBypassesModal(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.paintShellFrame()
	_, cmd := app.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl | tea.ModAlt})
	runTerminalCommand(t, app, cmd)
	app.paintShellFrame()
	if !app.terminalOwnsKeys() || len(app.termMgr().List()) != 1 {
		t.Fatal("fast summon did not create/focus first terminal")
	}
	id := app.lastTerminalID
	t.Cleanup(func() { app.closeTerminal(id) })
	hints := ""
	for _, seg := range app.compactContextHelpSegments() {
		hints += app.hintSegmentText(seg) + " "
	}
	if !strings.Contains(hints, "Alt+F12") || !strings.Contains(hints, "scrollback") {
		t.Fatalf("missing terminal escape/scroll hints: %q", hints)
	}
	app.openFolderPicker()
	app.paintShellFrame()
	modal := app.overlayHost.Window(ui.OverlayFilePicker)
	if modal == nil {
		t.Fatal("test modal absent")
	}
	for _, key := range []tea.KeyPressMsg{{Code: '`', Mod: tea.ModCtrl}, {Code: 't', Mod: tea.ModCtrl | tea.ModAlt}, {Code: '`', Mod: tea.ModCtrl | tea.ModShift}} {
		_, cmd = app.Update(key)
		runTerminalCommand(t, app, cmd)
		if app.windowManager.Focused() != modal || len(app.termMgr().List()) != 1 {
			t.Fatal("terminal shortcut crossed modal ownership")
		}
	}
}
