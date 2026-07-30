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
	app.launcherHost = nil

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
	view := app.buildMainView()
	if strings.Contains(view, "Ctrl+S save") {
		t.Fatalf("editor help must not render inside the main pane:\n%s", view)
	}
	if app.ChatAreaHeight != 25 {
		t.Fatalf("editor should reclaim help separator and row: height=%d, want 25", app.ChatAreaHeight)
	}
}

func TestInlineModeHelpMovesToStatusWithoutReservingRows(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcherScreen = nil
	app.launcherHost = nil
	app.ActiveSession = workspace.NewSession("", "", nil, nil)
	app.openSessionSettings()

	view := app.buildMainView()
	help := app.contextualHelpView()
	if !strings.Contains(help, "Shift+Tab previous") {
		t.Fatalf("session settings help should move to status: %q", help)
	}
	if strings.HasPrefix(help, "[") || strings.HasSuffix(help, "]") {
		t.Fatalf("status help should use compact status-bar chrome: %q", help)
	}

	if strings.Contains(view, "Shift+Tab previous") {
		t.Fatalf("session settings help must not render inside the main pane:\n%s", view)
	}
	inputLines := len(strings.Split(app.Input.View(app.width, app.height, false, false, app.SpinFrame), "\n"))
	wantHeight := app.height - 2 - 3 - app.sessionDeckRect.H - inputLines - 1
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
	app.launcherHost = nil
	app.FocusStack.Push(FocusTabBar)
	if got := app.contextualHelpView(); !strings.Contains(got, "tab bar focused") {
		t.Fatalf("tab-focus help should move to status: %q", got)
	}
}

func TestComposerHelpMovesToStatusWithoutRenderingInInput(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.launcherHost = nil

	help := app.contextualHelpView()
	if !strings.Contains(help, "Enter send") || !strings.Contains(help, "Ctrl+O attach file") {
		t.Fatalf("composer guidance should appear in status context: %q", help)
	}
	inputView := app.Input.View(100, 20, false, false, 0)
	if strings.Contains(inputView, "Enter send") || strings.Contains(inputView, "attach file") {
		t.Fatalf("composer guidance must not reserve an input row: %q", inputView)
	}

	app.Input.DismissHint()
	if got := app.contextualHelpView(); got != "" {
		t.Fatalf("dismissed composer guidance should leave status context, got %q", got)
	}
}

func TestEditorContextHelpDerivesShortcutsFromRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.launcherHost = nil
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
	app.launcherHost = nil
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
