package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestFindReplace_PopupOwnedKeysThroughDispatch drives the popup's keyboard
// shortcuts through the REAL dispatch chain (dispatchKeyPress), the way a
// user's keys arrive. Regression guard for the two defects that made the
// popup feel dead:
//
//  1. alt+c was swallowed by the global composer.toggle binding BEFORE focus
//     routing — opening the composer, stealing focus, and killing every
//     subsequent key. The popup must own its filter chords.
//  2. ctrl+h/ctrl+f were not handled while the popup held focus, so the
//     find ↔ replace transform only worked from the editor.
func TestFindReplace_PopupOwnedKeysThroughDispatch(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.openFindReplace(false)
	if app.FindReplace == nil {
		t.Fatal("find popup did not open")
	}
	if fw := app.windowManager.Focused(); fw == nil || fw.ID != "find-replace" {
		t.Fatalf("find-replace window not focused: %v", fw)
	}

	// Alt+C toggles case sensitivity IN THE POPUP — and must not open the
	// composer or steal focus.
	if cs := app.FindReplace.CaseSensitive; cs {
		t.Fatal("precondition: CaseSensitive should start false")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !app.FindReplace.CaseSensitive {
		t.Fatal("alt+c did not toggle CaseSensitive through dispatch")
	}
	if fw := app.windowManager.Focused(); fw == nil || fw.ID != "find-replace" {
		t.Fatalf("alt+c stole focus from the popup (focused=%v)", fw)
	}
	if app.windowManager.Window("composer") != nil {
		t.Fatal("alt+c opened the composer from the find popup")
	}

	// Alt+W / Alt+R toggle the other filters.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
	if !app.FindReplace.WholeWord {
		t.Fatal("alt+w did not toggle WholeWord through dispatch")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	if !app.FindReplace.Regex {
		t.Fatal("alt+r did not toggle Regex through dispatch")
	}

	// Ctrl+H transforms the open Find popup into the Replace form.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if app.FindReplace == nil {
		t.Fatal("ctrl+h closed the popup")
	}
	if !app.FindReplace.ReplaceMode {
		t.Fatal("ctrl+h did not transform the popup into replace mode")
	}
	if fw := app.windowManager.Window("find-replace"); fw == nil {
		t.Fatal("transform lost the window")
	}

	// Ctrl+F transforms it back to Find.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if app.FindReplace == nil {
		t.Fatal("ctrl+f closed the popup")
	}
	if app.FindReplace.ReplaceMode {
		t.Fatal("ctrl+f did not transform the popup back to find mode")
	}

	// Esc closes the popup.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.FindReplace != nil {
		t.Fatal("esc did not close the popup")
	}
	if app.windowManager.Window("find-replace") != nil {
		t.Fatal("esc left the window mounted")
	}
}

// TestFindReplace_TabSwitchesFields pins field-to-field Tab movement while
// the popup owns focus (Tab/Shift+Tab through the real dispatch chain).
func TestFindReplace_TabSwitchesFields(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.openFindReplace(true)
	if app.FindReplace == nil || !app.FindReplace.ReplaceMode {
		t.Fatal("replace popup did not open")
	}

	if app.FindReplace.FocusedField != 0 {
		t.Fatalf("precondition: FocusedField=%d want 0", app.FindReplace.FocusedField)
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab})
	if app.FindReplace.FocusedField != 1 {
		t.Fatalf("tab: FocusedField=%d want 1", app.FindReplace.FocusedField)
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if app.FindReplace.FocusedField != 0 {
		t.Fatalf("shift+tab: FocusedField=%d want 0", app.FindReplace.FocusedField)
	}
}
