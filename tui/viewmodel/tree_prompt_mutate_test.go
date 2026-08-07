package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// TestTreePrompt_FramePersistsAcrossKeystrokes proves the tree create/rename
// prompt frame is built once on open and subsequent keystrokes mutate the
// retained TextField instead of rebuilding the WindowFrame (Phase 9 #14).
func TestTreePrompt_FramePersistsAcrossKeystrokes(t *testing.T) {
	app := newTestApp(t)
	app.width = 100
	app.height = 30

	app.openTreeTextPrompt(ui.TextPromptNewFile, "New file", "Create in /tmp", "", "", func(value string) error {
		return nil
	})
	fw := app.windowManager.Window("tree-prompt")
	if fw == nil {
		t.Fatal("tree prompt window should be open")
	}
	contentBefore := fw.Content
	if contentBefore == nil {
		t.Fatal("tree prompt content is nil")
	}

	for _, r := range "main.go" {
		app.handleTreePromptKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		if fw.Content != contentBefore {
			t.Fatalf("tree prompt frame rebuilt on keystroke %q; frame must persist (mutate field, not rebuild)", string(r))
		}
	}

	if got := app.TreePrompt.ValueText(); got != "main.go" {
		t.Fatalf("typed value: got %q, want %q", got, "main.go")
	}

	// The retained frame's TextField must mirror the typed value: read it
	// back through the existing sync helper.
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		t.Fatal("tree prompt content is not a ComponentWidget")
	}
	ui.SyncTextPromptField(cw, app.TreePrompt)
	if got := app.TreePrompt.ValueText(); got != "main.go" {
		t.Fatalf("retained frame TextField did not reflect typed value; got %q, want %q", got, "main.go")
	}
}

// TestTreePrompt_RenameKeystrokesPreserveFrame exercises the same no-rebuild
// guarantee through the rename flavor of the prompt.
func TestTreePrompt_RenameKeystrokesPreserveFrame(t *testing.T) {
	app := newTestApp(t)
	app.width = 100
	app.height = 30

	app.openTreeTextPrompt(ui.TextPromptRename, "Rename", "old.go", "", "old.go", func(value string) error {
		return nil
	})
	fw := app.windowManager.Window("tree-prompt")
	if fw == nil {
		t.Fatal("tree prompt window should be open")
	}
	contentBefore := fw.Content

	app.handleTreePromptKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if fw.Content != contentBefore {
		t.Fatal("tree prompt frame rebuilt on backspace; frame must persist")
	}
	app.handleTreePromptKey(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if fw.Content != contentBefore {
		t.Fatal("tree prompt frame rebuilt on rune key; frame must persist")
	}

	if got := app.TreePrompt.ValueText(); got != "old.gw" {
		t.Fatalf("rename value after edit: got %q, want %q", got, "old.gw")
	}
}
