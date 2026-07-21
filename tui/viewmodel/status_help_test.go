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
	app.launcher = nil

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
	for _, want := range []string{"handlers.go", "1:1", "4L", "Ctrl+S save", "esc close"} {
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
	app.launcher = nil
	app.ActiveSession = workspace.NewSession("", "", nil, nil)
	app.setMode(ModeSettings)

	overlayLines := app.renderOverlayArea()
	app.syncFormHint()
	help := app.contextualHelpView()
	if !strings.Contains(help, "provider, model, and thinking level") {
		t.Fatalf("session settings help should move to status: %q", help)
	}
	if strings.HasPrefix(help, "[") || strings.HasSuffix(help, "]") {
		t.Fatalf("status help should use compact status-bar chrome: %q", help)
	}

	view := app.buildMainView()
	if strings.Contains(view, "provider, model, and thinking level") {
		t.Fatalf("session settings help must not render inside the main pane:\n%s", view)
	}
	wantHeight := app.height - 2 - 3 - len(overlayLines)
	if wantHeight < 1 {
		wantHeight = 1
	}
	if app.ChatAreaHeight != wantHeight {
		t.Fatalf("inline mode should reclaim help separator and row: height=%d, want %d", app.ChatAreaHeight, wantHeight)
	}
}

func TestContextualHelpCoversInlineModesAndTabFocus(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
		tab  string
		want string
	}{
		{name: "confirm", mode: ModeConfirm, tab: "Confirm", want: "confirmation required"},
		{name: "yolo", mode: ModeYolo, tab: "YOLO", want: "yolo mode confirmation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newFullTestApp(t)
			app.launcher = nil
			app.Overlay.OpenTab(tt.tab, "", true)
			app.OverlayState.Set(tt.mode)
			app.syncFormHint()
			if got := app.contextualHelpView(); !strings.Contains(got, tt.want) {
				t.Fatalf("status help=%q, want %q", got, tt.want)
			}
		})
	}

	app := newFullTestApp(t)
	app.launcher = nil
	app.FocusStack.Push(FocusTabBar)
	app.syncFormHint()
	if got := app.contextualHelpView(); !strings.Contains(got, "tab bar focused") {
		t.Fatalf("tab-focus help should move to status: %q", got)
	}
}

func TestCompactStatusHintPreservesQuestionContext(t *testing.T) {
	hint := "[ enter: submit answer · esc: back ]  ❯ Q2 (Details)"
	got := compactStatusHint(hint)
	if got != "enter: submit answer · esc: back  ·  ❯ Q2 (Details)" {
		t.Fatalf("compactStatusHint()=%q", got)
	}
}

func TestComposerHelpMovesToStatusWithoutRenderingInInput(t *testing.T) {
	app := newFullTestApp(t)
	app.launcher = nil

	help := app.contextualHelpView()
	if !strings.Contains(help, "enter send") || !strings.Contains(help, "ctrl+o attach file") {
		t.Fatalf("composer guidance should appear in status context: %q", help)
	}
	inputView := app.Input.View(100, 20, false, false, 0)
	if strings.Contains(inputView, "enter send") || strings.Contains(inputView, "attach file") {
		t.Fatalf("composer guidance must not reserve an input row: %q", inputView)
	}

	app.Input.DismissHint()
	if got := app.contextualHelpView(); got != "" {
		t.Fatalf("dismissed composer guidance should leave status context, got %q", got)
	}
}

func TestEditorContextHelpDerivesShortcutsFromRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcher = nil
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
		"esc close",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("editor help missing %q in: %q", want, help)
		}
	}
}

func TestEditorContextHelpFallsBackEmptyRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcher = nil
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
	// Without registry, still shows position info and esc close.
	if !strings.Contains(help, "main.go") || !strings.Contains(help, "esc close") {
		t.Fatalf("editor help missing basics (nil registry): %q", help)
	}
	// Should NOT have the "  ·  " extra separator when no registry actions.
	if strings.Count(help, "  ·  esc close") < 1 {
		t.Fatalf("editor help missing '  ·  esc close' suffix: %q", help)
	}
}
