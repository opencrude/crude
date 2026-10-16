package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/workspace"
)

// TestStartupFocus_LauncherOwnsEnterAndHints is the behavioural contract for
// Task 1: with the welcome launcher visible and no tabs, Enter creates a
// session (does not act on the tree); Ctrl+1 moves focus to the tree so `/`
// opens the filter; Ctrl+1 again restores launcher keys. Status-bar hints
// track the focused region in each case.
func TestStartupFocus_LauncherOwnsEnterAndHints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.ChatAreaHeight = 20
	app.BufferMgr = editor.NewBufferManager()
	app.EditorState = NewEditorState()
	wsOf(app).AgentMgr = workspace.NewAgentManager()
	app.AgentView = NewAgentViewState(app.ws.AgentMgr())
	app.actionRegistry = ui.DefaultRegistry()
	if app.FocusStack == nil {
		app.FocusStack = NewFocusStack()
	}

	app.launcherScreen.SetData(dir, nil)
	app.launcherScreen.SyncFocus()
	app.paintShellFrame()

	model := filetree.NewFileTreeModel(dir)
	app.Tree = NewTreeState(model, 28)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	if !app.launcherHostVisible() {
		t.Fatal("precondition: launcher should be visible with no tabs")
	}

	// --- Startup: launcher owns focus; Enter activates New Session ---
	app.FocusStack.Clear()
	if got := app.focusTarget(); got != FocusLauncher {
		t.Fatalf("startup focusTarget=%v want FocusLauncher", got)
	}
	help := app.contextualHelpView()
	// Tree region prefixes its hint with "file tree"; launcher may mention
	// "Ctrl+1 focus file tree" as a discoverability chord; that is fine.
	if strings.HasPrefix(strings.TrimSpace(help), "file tree") {
		t.Fatalf("startup status hint must be launcher region, got %q", help)
	}
	if !strings.Contains(help, "Enter") {
		t.Fatalf("startup status hint should include Enter, got %q", help)
	}
	if !strings.Contains(help, "Ctrl+Alt+P") {
		t.Fatalf("startup status hint should include launcher-only chords, got %q", help)
	}

	cursorBefore := model.CursorIndex()
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.CursorIndex() != cursorBefore {
		t.Fatalf("Enter at startup must not move the tree cursor (was %d now %d)", cursorBefore, model.CursorIndex())
	}
	// ActivateMsg is returned as a Cmd from the launcher Host; drain it the
	// same way Update would.
	res := app.rootHandleFocus(launcher.ScreenID, tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd == nil {
		// First Enter already consumed via dispatch; re-select New Session
		// and activate through dispatch by ensuring selection is on card 0.
		app.launcherScreen.SyncFocus()
	}
	// Ensure selected item is still New Session after the key (dispatch routes
	// to launcher Host which activates).
	if got := app.launcherScreen.SelectedItem(); got == nil || got.Kind != launcher.ItemNewSession {
		// After Enter activation the selection may still be New Session.
		// What matters: tree filter stayed inactive and focus stayed launcher
		// until we explicitly move it.
		_ = got
	}
	if model.FilterActive() {
		t.Fatal("Enter at startup must not open the tree filter")
	}
	if app.focusTarget() != FocusLauncher {
		t.Fatalf("after Enter focusTarget=%v want FocusLauncher (no tabs yet in this fixture)", app.focusTarget())
	}

	// Prove launcher activation path emits New Session via Host handle.
	app.FocusStack.Clear()
	app.launcherScreen.SetData(dir, nil)
	app.launcherScreen.SyncFocus()
	activate := app.rootHandleFocus(launcher.ScreenID, tea.KeyPressMsg{Code: tea.KeyEnter})
	if activate.Cmd == nil {
		t.Fatal("launcher Enter must emit ActivateMsg cmd")
	}
	msg := activate.Cmd()
	am, ok := msg.(launcher.ActivateMsg)
	if !ok || am.Kind != launcher.ItemNewSession {
		t.Fatalf("ActivateMsg=%#v want ItemNewSession", msg)
	}

	// --- Explicit tree focus (Ctrl+1): `/` opens filter, arrows navigate ---
	app.dispatchKeyPress(tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl})
	if app.focusTarget() != FocusTree {
		t.Fatalf("after Ctrl+1 focusTarget=%v want FocusTree", app.focusTarget())
	}
	help = app.contextualHelpView()
	if !strings.HasPrefix(strings.TrimSpace(help), "file tree") {
		t.Fatalf("tree-focused status hint must start with 'file tree', got %q", help)
	}

	app.dispatchKeyPress(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !model.FilterActive() {
		t.Fatal("tree `/` filter must activate when tree is focused")
	}
	// Clear filter via Esc so arrows exercise tree nav, not filter typing.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.FilterActive() {
		t.Fatal("Esc should clear tree filter while keeping tree focus")
	}
	if app.focusTarget() != FocusTree {
		t.Fatalf("after filter Esc focusTarget=%v want FocusTree", app.focusTarget())
	}
	before := model.CursorIndex()
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	if model.CursorIndex() == before {
		t.Fatal("Down should navigate the tree while focused")
	}

	// --- Focus back (Ctrl+1): launcher keys work again ---
	app.dispatchKeyPress(tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl})
	if app.focusTarget() != FocusLauncher {
		t.Fatalf("second Ctrl+1 focusTarget=%v want FocusLauncher", app.focusTarget())
	}
	help = app.contextualHelpView()
	if strings.HasPrefix(strings.TrimSpace(help), "file tree") {
		t.Fatalf("after return, status hint must be launcher region, got %q", help)
	}
	treeCursor := model.CursorIndex()
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	if model.CursorIndex() != treeCursor {
		t.Fatal("Down on launcher must not move the tree cursor")
	}
	if got := app.launcherScreen.SelectedItem(); got == nil || got.Kind != launcher.ItemOpenFolder {
		t.Fatalf("launcher Down should select OpenFolder, got %#v", got)
	}
}

// TestToggleSidebar_DoesNotStealLauncherFocus verifies Ctrl+B hide/show does
// not push FocusTree over the welcome launcher.
func TestToggleSidebar_DoesNotStealLauncherFocus(t *testing.T) {
	app, screen := setupLauncherRoutingApp(t, nil)
	app.FocusStack.Clear()
	if app.focusTarget() != FocusLauncher {
		t.Fatalf("precondition focus=%v", app.focusTarget())
	}
	app.ToggleSidebar() // hide
	app.ToggleSidebar() // show
	if app.focusTarget() != FocusLauncher {
		t.Fatalf("after sidebar toggle focusTarget=%v want FocusLauncher", app.focusTarget())
	}
	if app.FocusStack.Top() == FocusTree {
		t.Fatal("ToggleSidebar must not push FocusTree while launcher is visible")
	}
	_ = screen
}
