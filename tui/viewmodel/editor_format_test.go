package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFormatDocumentFormatsBufferWithoutSavingAndOneUndoRestores(t *testing.T) {
	app := newFullTestApp(t)
	before := "package main\n\nfunc  f( )  int { return 1 }\n"
	ed := newSaveTestEditorAt(t, app, filepath.Join(t.TempDir(), "format.go"), before)
	runAppCommand(t, app, app.requestActiveEditorFormat())
	if got := ed.Value(); got != "package main\n\nfunc f() int { return 1 }\n" {
		t.Fatalf("formatted text = %q", got)
	}
	if !ed.Modified() {
		t.Fatal("formatting did not mark the unsaved buffer dirty")
	}
	disk, err := os.ReadFile(ed.FilePath())
	if err != nil || string(disk) != before {
		t.Fatalf("format wrote disk bytes = %q, %v", disk, err)
	}
	ed.Undo()
	if got := ed.Value(); got != before {
		t.Fatalf("one undo restored %q, want %q", got, before)
	}
}

// Shift+Alt+F is the advertised editor binding; it must reach the same format
// command the menu and palette use.
func TestFormatDocumentKeyBindingFormatsAndMenuOffersIt(t *testing.T) {
	app := newFullTestApp(t)
	before := "package main\n\nfunc  f( )  int { return 1 }\n"
	ed := newSaveTestEditorAt(t, app, filepath.Join(t.TempDir(), "format.go"), before)
	pressKeys(t, app, tea.KeyPressMsg{Mod: tea.ModShift | tea.ModAlt, Code: 'f', Text: "F"})
	runAppCommand(t, app, app.drainLiveEditorCommands())
	if got := ed.Value(); got != "package main\n\nfunc f() int { return 1 }\n" {
		t.Fatalf("binding formatted %q", got)
	}

	menu := app.buildEditorContextMenu(tea.Mouse{})
	if _, found := contextMenuItem(menu, "Format document"); !found {
		t.Fatal("editor menu does not offer document formatting")
	}
	if _, found := contextMenuItem(menu, "Format project"); !found {
		t.Fatal("editor menu does not offer project formatting")
	}
}
