package viewmodel

import (
	"path/filepath"
	"testing"
)

func TestTerminalIdentityUsesWorkspaceNameForTitleAndIcon(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).RootPath = filepath.Join(t.TempDir(), "project-alpha")

	view := app.newTerminalView("")
	const want = "project-alpha - Crude"
	if view.WindowTitle != want || view.IconName != want {
		t.Fatalf("title=%q icon=%q, want both %q", view.WindowTitle, view.IconName, want)
	}
}

func TestTerminalIdentityStripsControlCharacters(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).RootPath = filepath.Join(t.TempDir(), "project\x1b]2;injected\x07")

	if got, want := app.terminalIdentity(), "project]2;injected - Crude"; got != want {
		t.Fatalf("terminal identity=%q, want %q", got, want)
	}
}
