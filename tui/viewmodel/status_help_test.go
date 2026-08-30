package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/editor"
	"crude/workspace"
)

func TestEditorHelpMovesToStatusWithoutReservingRows(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcherScreen = nil

	path := filepath.Join(t.TempDir(), "handlers.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc handle() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = path

	help := app.contextualHelpView()
	for _, want := range []string{"handlers.go", "1:1", "4L", "Ctrl+S save", "Esc close"} {
		if !strings.Contains(help, want) {
			t.Fatalf("editor status help missing %q: %q", want, help)
		}
	}
	_ = app.computeContentLayout()
	// Editor reclaims the tab-bar rows; with no tab bar the chat area spans
	// from chromeTopRow down to the bottom chrome (30 - 2 - 3).
	if want := 30 - app.chromeBottomRows() - app.chromeTopRow(); app.ChatAreaHeight != want {
		t.Fatalf("editor should reclaim help separator and row: height=%d, want %d", app.ChatAreaHeight, want)
	}
}

func TestInlineModeHelpMovesToStatusWithoutReservingRows(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcherScreen = nil
	app.ActiveSession = workspace.NewSession("", "", nil, nil)
	app.openSessionSettings()

	_ = app.computeContentLayout()
	help := app.contextualHelpView()
	if !strings.Contains(help, "Shift+Tab previous") {
		t.Fatalf("session settings help should move to status: %q", help)
	}
	if strings.HasPrefix(help, "[") || strings.HasSuffix(help, "]") {
		t.Fatalf("status help should use compact status-bar chrome: %q", help)
	}

	inputLines := app.Input.ShellHeight(app.width)
	wantHeight := app.height - app.chromeBottomRows() - app.chromeTopRow() - app.sessionDeckRect.H - inputLines
	if wantHeight < 1 {
		wantHeight = 1
	}
	if app.ChatAreaHeight != wantHeight {
		t.Fatalf("inline mode should reclaim help separator and row: height=%d, want %d", app.ChatAreaHeight, wantHeight)
	}
}

func TestContextualHelpCoversTabFocus(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.FocusStack.Push(FocusTabBar)
	if got := app.contextualHelpView(); !strings.Contains(got, "tab bar focused") {
		t.Fatalf("tab-focus help should move to status: %q", got)
	}
}

func TestComposerHelpMovesToStatusWithoutRenderingInInput(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil

	help := app.contextualHelpView()
	if !strings.Contains(help, "Enter send") || !strings.Contains(help, "Ctrl+O attach file") {
		t.Fatalf("composer guidance should appear in status context: %q", help)
	}
	inputLines := app.Input.ShellHeight(100)
	if inputLines > 2 {
		t.Fatalf("composer guidance must not reserve an input row: shell=%d rows", inputLines)
	}

	app.Input.DismissHint()
	if got := app.contextualHelpView(); got != "" {
		t.Fatalf("dismissed composer guidance should leave status context, got %q", got)
	}
}

func TestEditorContextHelpDerivesShortcutsFromRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.width = 100
	app.height = 30

	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = path

	help := app.contextualHelpView()
	for _, want := range []string{
		"Ctrl+S save",
		"Ctrl+Z undo",
		"Ctrl+Y redo",
		"Esc close",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("editor help missing %q in: %q", want, help)
		}
	}
}

func TestEditorContextHelpFallsBackEmptyRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.actionRegistry = nil
	app.width = 100
	app.height = 30

	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = path

	help := app.contextualHelpView()
	// Without registry, still shows position info and Esc close via chord fallbacks.
	if !strings.Contains(help, "main.go") || !strings.Contains(help, "Esc close") {
		t.Fatalf("editor help missing basics (nil registry): %q", help)
	}
	if !strings.Contains(help, "Ctrl+S save") {
		t.Fatalf("editor help missing chord fallbacks (nil registry): %q", help)
	}
}
