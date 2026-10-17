package viewmodel

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/ui"
)

// openFindForOverlayTextTest opens Ctrl+F over a document through the real
// key dispatch, as the user does.
func openFindForOverlayTextTest(t *testing.T, doc string) *App {
	t.Helper()
	app := newTestAppForPopupMouse(t)
	app.registerOverlayHandlers()
	app.width, app.height = 120, 40
	app.EditorState.ActiveView = "/tmp/foo.go"
	app.EditorState.Editor.ReplaceDocument(doc)
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if app.FindReplace == nil {
		t.Fatal("ctrl+f did not open find")
	}
	return app
}

// findFieldScreenRect is the find field's painted screen rectangle.
func findFieldScreenRect(t *testing.T, app *App) ui.Rect {
	t.Helper()
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	cw := fw.Content.(*ui.ComponentWidget)
	w, h := fw.Size()
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	r, ok := cw.Host().ScreenRect(ui.FRFindField)
	if !ok || r.Empty() {
		t.Fatal("find field has no geometry")
	}
	x, y := fw.Position()
	return ui.Rect{X: x + r.X, Y: y + r.Y, W: r.W, H: r.H}
}

// Windows Terminal (and every terminal paste shortcut) delivers bracketed
// paste. It used to be dropped by the find popup, whose ComponentWidget had no
// paste route; it must edit the query and re-derive matches.
func TestFindPopupBracketedPasteEditsQuery(t *testing.T) {
	app := openFindForOverlayTextTest(t, "alpha beta\ngamma alpha\n")
	app.Update(tea.PasteMsg{Content: "alpha"})
	if got := app.FindReplace.Query(); got != "alpha" {
		t.Fatalf("query after paste = %q, want alpha", got)
	}
	if app.FindReplace.MatchCount != 2 {
		t.Fatalf("matches after paste = %d, want 2", app.FindReplace.MatchCount)
	}
}

// Dragging in the find field selects through the shared TextField (captured
// across the popup), keeps the selection, and Ctrl+C / typing act on it.
func TestFindPopupDragSelectionKeepsAndCopies(t *testing.T) {
	app := openFindForOverlayTextTest(t, "alpha beta\n")
	for _, r := range "alphabet" {
		app.dispatchKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	field := findFieldScreenRect(t, app)
	app.dispatchMouseClick(tea.MouseClickMsg{X: field.X, Y: field.Y, Button: tea.MouseLeft})
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: field.X + 5, Y: field.Y, Button: tea.MouseLeft})
	app.dispatchMouseRelease(tea.MouseReleaseMsg{X: field.X + 5, Y: field.Y, Button: tea.MouseLeft})
	if got := app.FindReplace.FindEdit().SelectedText(); got != "alpha" {
		t.Fatalf("drag selection after release = %q, want alpha", got)
	}

	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	var copied []string
	ui.FindReplaceFrameFindField(fw.Content.(*ui.ComponentWidget)).SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg { copied = append(copied, text); return nil }
	})
	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C in the find field returned no clipboard command")
	}
	cmd()
	if len(copied) != 1 || copied[0] != "alpha" {
		t.Fatalf("copied = %q, want [alpha]", copied)
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	if got := app.FindReplace.Query(); got != "Zbet" {
		t.Fatalf("typing over the selection = %q, want Zbet", got)
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := app.FindReplace.Query(); got == "Zbet" {
		t.Fatal("Ctrl+Z did not undo in the find field")
	}
}

// Previous-match and replace-all chords were unreachable: the plain-Enter case
// swallowed every Enter variant, and Shift+F3 fell through to text editing.
func TestFindPopupPreviousAndReplaceAllChords(t *testing.T) {
	app := openFindForOverlayTextTest(t, "alpha one\nalpha two\nalpha three\n")
	for _, r := range "alpha" {
		app.dispatchKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	start := app.FindReplace.CurrentIdx
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyF3, Mod: tea.ModShift})
	if app.FindReplace.CurrentIdx == start {
		t.Fatalf("Shift+F3 did not move to the previous match (idx %d)", start)
	}
	afterPrev := app.FindReplace.CurrentIdx
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if app.FindReplace.CurrentIdx == afterPrev {
		t.Fatal("Shift+Enter did not move to the previous match")
	}
	if got := app.FindReplace.Query(); got != "alpha" {
		t.Fatalf("navigation chords edited the query: %q", got)
	}

	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab})
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'X', Text: "X"})
	// tmux delivers Ctrl+Alt+Enter as Alt+Enter.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	if got := app.EditorState.Editor.LineText(2); got != "X three" {
		t.Fatalf("Alt+Enter did not replace all: line 3 = %q", got)
	}
}

// Ctrl+V in the find field reads the clipboard off Update and the text lands
// in the field that asked for it, through the same path as typing (matches
// re-derive), even if focus moved to the replace field meanwhile.
func TestFindPopupCtrlVLandsInInitiatingField(t *testing.T) {
	app := openFindForOverlayTextTest(t, "needle\n")
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			<-release
			return []byte("needle")
		},
		writeText: func(string) error { return nil },
	})
	defer restore()
	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+V started no clipboard read")
	}
	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab}) // focus the replace field
	once.Do(func() { close(release) })
	select {
	case msg := <-results:
		app.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("clipboard read did not complete")
	}
	if got := app.FindReplace.Query(); got != "needle" {
		t.Fatalf("query = %q, want the pasted needle", got)
	}
	if got := app.FindReplace.Replace(); got != "" {
		t.Fatalf("replace field was mutated: %q", got)
	}
	if app.FindReplace.MatchCount != 1 {
		t.Fatalf("matches after Ctrl+V = %d, want 1", app.FindReplace.MatchCount)
	}
}

// The command palette dropped bracketed paste and discarded the clipboard
// command of Ctrl+X (cutting the query without copying it).
func TestCommandPalettePasteAndCut(t *testing.T) {
	app, cw := openCommandPaletteForInteractionTest(t, nil)
	all := len(app.commandPaletteModel.filtered)
	app.Update(tea.PasteMsg{Content: "settings"})
	if got := app.commandPaletteModel.filterText(); got != "settings" {
		t.Fatalf("palette query after paste = %q", got)
	}
	if len(app.commandPaletteModel.filtered) >= all {
		t.Fatal("paste did not filter the palette")
	}

	var copied []string
	ui.CommandPaletteSearch(cw).SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg { copied = append(copied, text); return nil }
	})
	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+X in the palette returned no clipboard command")
	}
	cmd()
	if len(copied) != 1 || copied[0] != "settings" {
		t.Fatalf("cut copied %q, want [settings]", copied)
	}
	if got := app.commandPaletteModel.filterText(); got != "" || len(app.commandPaletteModel.filtered) != all {
		t.Fatalf("cut left query %q with %d rows", got, len(app.commandPaletteModel.filtered))
	}
}

// The tree prompt (new file / rename) takes bracketed paste and keyboard
// selection through the shared field; its painted field is the edited value.
func TestTreePromptPasteAndSelection(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.registerOverlayHandlers()
	app.width, app.height = 100, 30
	app.openTreeTextPrompt(ui.TextPromptRename, "Rename", "old.go", "", "old.go", func(string) error { return nil })
	app.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift})
	app.Update(tea.PasteMsg{Content: "new"})
	if got := app.TreePrompt.ValueText(); got != "new" {
		t.Fatalf("paste over the Shift+Home selection = %q, want new", got)
	}
	cw := app.windowManager.Window(string(ui.OverlayTreePrompt)).Content.(*ui.ComponentWidget)
	if got := ui.TextPromptField(cw).Value(); got != "new" {
		t.Fatalf("painted field = %q", got)
	}
}

// Ctrl+V's clipboard text re-enters through the composite that asked for it,
// so a debounced search field schedules its search exactly as for a bracketed
// paste (inserting into the inner field directly skipped the search).
func TestNativePasteDeliveryRunsThroughCompositeOwner(t *testing.T) {
	field := ui.NewSearchField("search", nil)
	host := ui.NewHost(nil, field)
	host.Resize(30, 1)
	host.Layout()
	host.Focus(field.ID())
	res := host.Handle(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if res.Action == nil || res.Action.ID != ui.ActionTextPaste {
		t.Fatalf("Ctrl+V action = %+v", res.Action)
	}
	target := res.Action.Payload.(ui.ClipboardPasteTarget)
	deliver := nativePasteDelivery(target, nil, host)
	if deliver == nil {
		t.Fatal("no owner found for the search field paste target")
	}
	if cmd := deliver("query"); cmd == nil {
		t.Fatal("delivered paste did not schedule the debounced search")
	}
	if field.Value() != "query" {
		t.Fatalf("value = %q", field.Value())
	}
}
