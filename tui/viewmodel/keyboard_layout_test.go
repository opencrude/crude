package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Shortcuts must work with Num Lock on and with a non-Latin keyboard layout.
// The keys below are exactly what Ghostty sends with the kitty keyboard
// protocol: the Persian letter, the US-layout key and the lock modifier.
func TestEditorShortcutsWorkOnANonLatinLayoutWithNumLock(t *testing.T) {
	app := askCrudeApp(t, 160, 40, map[string]string{"layout.go": "package layout\n\nfunc A() {}\n"})
	openEditorTabs(t, app, "layout.go")
	if !app.View().KeyboardEnhancements.ReportAlternateKeys {
		t.Fatal("Crude does not ask the terminal for US-layout keys")
	}
	ed := app.EditorState.Editor

	// Ctrl+A on a Persian layout is Ctrl+ش.
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'ش', BaseCode: 'a', Mod: tea.ModCtrl | tea.ModNumLock})
	runAppCommand(t, app, cmd)
	if got := ed.SelectedText(); got != "package layout\n\nfunc A() {}\n" && got != "package layout\n\nfunc A() {}" {
		t.Fatalf("Ctrl+A on a Persian layout selected %q, want the whole file", got)
	}
	if ed.LineText(0) != "package layout" || ed.Modified() {
		t.Fatal("Ctrl+A on a Persian layout typed into the file")
	}

	// Ctrl+K on a Persian layout is Ctrl+ن: Ask Crude opens the chat.
	ed.ClearSelection()
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'ن', BaseCode: 'k', Mod: tea.ModCtrl | tea.ModNumLock})
	runAppCommand(t, app, cmd)
	_ = app.View()
	chat := askChat(t, app)
	if app.Win.ActiveGroup() != chat.Group || app.focusTarget() != FocusInput {
		t.Fatal("Ctrl+K on a Persian layout did not open Ask Crude")
	}
}
