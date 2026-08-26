package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

// TestComposerCtrlVPastesFromSystemClipboard guards the composer paste fix:
// Ctrl+V and Ctrl+Shift+V must enqueue an image-first system-clipboard read.
// Bracketed paste cannot carry images (terminals only wrap text), which is why
// the editor always had its own explicit ctrl+v path and the composer did not.
func TestComposerCtrlVPastesFromSystemClipboard(t *testing.T) {
	app := newFullTestApp(t)
	app.ActiveSession = workspace.NewSession("paste-session", t.TempDir(), nil, stubLogger{})
	app.Input.SetReferenceStore(app.ActiveSession.RefStore)

	for _, key := range []string{"ctrl+v", "ctrl+shift+v"} {
		var mods tea.KeyMod
		switch key {
		case "ctrl+v":
			mods = tea.ModCtrl
		case "ctrl+shift+v":
			mods = tea.ModCtrl | tea.ModShift
		}
		msg := tea.KeyPressMsg{Code: 'v', Mod: mods}
		if got := msg.String(); got != key {
			t.Fatalf("key normalization changed: %q != %q", got, key)
		}
		if _, cmd := app.dispatchChatKey(msg, nil); cmd == nil {
			t.Fatalf("%s must enqueue a clipboard read command", key)
		}
	}

	// No active session: nothing to attach into, the chord must stay inert.
	app.ActiveSession = nil
	if _, cmd := app.dispatchChatKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, nil); cmd != nil {
		t.Fatal("ctrl+v without an active session must not enqueue work")
	}
}

// TestFilePickerKeepsComposerMounted guards the Ctrl+O visibility fix: the
// picker is a modal FloatingWindow over the chat, so the composer must stay
// laid out while it is open (outside clicks are consumed by the overlay
// policy). Every other overlay mode still owns the whole frame.
func TestFilePickerKeepsComposerMounted(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 100
	app.height = 30
	app.launcherScreen = nil

	app.OverlayState.Set(workspace.ModeFilePicker)
	if lay := app.computeContentLayout(); !lay.ShowMainInput {
		t.Fatal("file picker overlay must not hide the composer")
	}
	if lay := app.computeContentLayout(); lay.InputLineCount < 1 {
		t.Fatalf("composer rows must be reserved during picker mode, got %d", lay.InputLineCount)
	}

	app.OverlayState.Set(workspace.ModeTextOverlay)
	if lay := app.computeContentLayout(); lay.ShowMainInput {
		t.Fatal("text overlay mode must keep owning the frame (input hidden)")
	}
}
