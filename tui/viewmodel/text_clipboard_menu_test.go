package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/ui"
	wm "crude/tui/wm"
)

// recordingClipboard installs a backend that captures clipboard writes instead
// of touching the system clipboard, and returns a waiter for the next write.
func recordingClipboard(t *testing.T, app *App) func() string {
	t.Helper()
	writes := make(chan string, 8)
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(clipboard.Format) []byte { return nil },
		writeText: func(text string) error {
			writes <- text
			return nil
		},
	})
	t.Cleanup(restore)
	return func() string {
		t.Helper()
		select {
		case text := <-writes:
			return text
		case <-time.After(2 * time.Second):
			t.Fatal("no clipboard write reached the backend")
			return ""
		}
	}
}

func menuItem(t *testing.T, st *ui.ContextMenuState, label string) ui.ContextMenuItem {
	t.Helper()
	for _, section := range st.Sections {
		for _, item := range section.Items {
			if item.Label == label {
				return item
			}
		}
	}
	t.Fatalf("menu item %q not found in %#v", label, st.Sections)
	return ui.ContextMenuItem{}
}

func activateMenuItem(t *testing.T, st *ui.ContextMenuState, label string) {
	t.Helper()
	item := menuItem(t, st, label)
	if item.Disabled {
		t.Fatalf("menu item %q is disabled", label)
	}
	if item.Action == nil {
		t.Fatalf("menu item %q has no action", label)
	}
	item.Action()
}

// The real product path for a composer drag: the Host owns pointer capture for
// the click, so the release arrives through the root Host. It must still copy
// and drop the selection, and must leave no pointer-owned state behind (the
// composer used to keep claiming pointer events after the release).
func TestComposerDragReleaseThroughHostCopiesAndClears(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	app.Input.SetInput("hello world")
	rect, ok := app.rootHost.ScreenRect(app.inputScreen.ID())
	if !ok || rect.Empty() {
		t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, rect)
	}
	padLR := app.Theme.Spacing.PadLR
	y := rect.Y + 1

	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: rect.X + padLR, Y: y, Button: tea.MouseLeft})
	if !app.rootHost.HasPointerCapture() {
		t.Fatal("click did not capture the pointer through the Host")
	}
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: rect.X + padLR + 5, Y: y, Button: tea.MouseLeft})
	selected := app.Input.SelectedText()
	if selected == "" {
		t.Fatal("drag produced no live selection")
	}

	_, cmd := app.dispatchMouseRelease(tea.MouseReleaseMsg{X: rect.X + padLR + 5, Y: y, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("host-path release emitted no clipboard command")
	}
	if app.Input.HasInputSelection() {
		t.Fatalf("release kept selection %q", app.Input.SelectedText())
	}
	if app.Input.InputSelecting() {
		t.Fatal("composer still claims the pointer after release")
	}

	// Hovering afterwards must not resurrect the selection.
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: rect.X + padLR + 9, Y: y})
	if app.Input.HasInputSelection() {
		t.Fatalf("hover re-created a selection: %q", app.Input.SelectedText())
	}
}

// The chat input's context menu acts on the whole draft: Copy always copies it,
// Cut copies and clears it, and both disable while the draft is empty.
func TestInputContextMenuOffersWholeDraftCopyCut(t *testing.T) {
	app := newFullTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	nextWrite := recordingClipboard(t, app)
	app.AddOrchestrator("agent")
	app.Input.SetInput("hello draft")
	app.paintShellFrame()

	mouse := tea.Mouse{X: app.inputRect.X + 3, Y: app.inputRect.Y + 1}
	st := app.buildInputContextMenu(mouse)
	if st == nil {
		t.Fatal("input context menu was not built")
	}
	for _, label := range []string{"Paste", "Copy", "Cut", "Clear input"} {
		if item := menuItem(t, st, label); item.Disabled {
			t.Fatalf("%q should be enabled with a non-empty draft", label)
		}
	}

	activateMenuItem(t, st, "Copy")
	if got := nextWrite(); got != "hello draft" {
		t.Fatalf("Copy wrote %q want the whole draft", got)
	}
	if app.Input.Value() != "hello draft" {
		t.Fatalf("Copy changed the draft: %q", app.Input.Value())
	}

	activateMenuItem(t, st, "Cut")
	if got := nextWrite(); got != "hello draft" {
		t.Fatalf("Cut wrote %q want the whole draft", got)
	}
	if got := app.Input.Value(); got != "" {
		t.Fatalf("Cut left draft=%q want empty", got)
	}

	// With an empty draft the whole-text commands disable; Clear stays usable.
	empty := app.buildInputContextMenu(mouse)
	for _, label := range []string{"Copy", "Cut"} {
		if item := menuItem(t, empty, label); !item.Disabled {
			t.Fatalf("%q must disable on an empty draft", label)
		}
	}
}

// The right-click path (not just the builder) reaches the same menu for the
// chat input region.
func TestRightClickOnInputOpensWholeDraftMenu(t *testing.T) {
	app := newFullTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	app.AddOrchestrator("agent")
	app.Input.SetInput("draft")
	app.paintShellFrame()

	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{
		X: app.inputRect.X + 3, Y: app.inputRect.Y + 1, Button: tea.MouseRight,
	})
	st := app.LastContextMenu
	if st == nil {
		t.Fatal("right-click on the composer opened no menu")
	}
	if item := menuItem(t, st, "Copy"); item.Disabled {
		t.Fatal("Copy should be enabled for a non-empty draft")
	}
	if item := menuItem(t, st, "Cut"); item.Disabled {
		t.Fatal("Cut should be enabled for a non-empty draft")
	}
}

// Esc walks the composer ladder: selection first, then the draft, and only
// then does it cancel the running turn.
func TestEscLadderSelectionDraftThenTurn(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	app.Input.SetInput("hello world")
	node := app.AgentMgr.Get("agent")
	if node == nil || node.State == nil {
		t.Fatal("test setup: no agent state")
	}
	node.State.Running = true

	// 1) An active selection is dropped first; the draft and the turn survive.
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift, Text: "shift+home"})
	if !app.Input.HasInputSelection() {
		t.Fatal("setup: shift+home should select the draft prefix")
	}
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.Input.HasInputSelection() {
		t.Fatal("esc must drop the selection before anything else")
	}
	if got := app.Input.Value(); got != "hello world" {
		t.Fatalf("esc cleared the draft while a selection existed: %q", got)
	}
	if node.State.Cancelling {
		t.Fatal("esc cancelled the turn while the draft was still there")
	}

	// 2) The draft is cleared next; the turn still survives.
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if got := app.Input.Value(); got != "" {
		t.Fatalf("esc=%q want the draft cleared", got)
	}
	if node.State.Cancelling {
		t.Fatal("esc cancelled the turn in the same press that cleared the draft")
	}

	// 3) With an empty composer Esc cancels the turn.
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !node.State.Cancelling {
		t.Fatal("esc with an empty composer must cancel the running turn")
	}
}

// Ctrl+C is owned by the composer: it copies a live selection, and with nothing
// selected it clears the draft (no clipboard write).
func TestChatKeyCtrlCCopiesOrClearsRatherThanShadowingTheComponent(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	app.Input.SetInput("hello world")

	// Selection: the component copies, the viewmodel keeps its hands off.
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift, Text: "shift+home"})
	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c with a selection returned no clipboard command")
	}
	if got := app.Input.Value(); got != "hello world" {
		t.Fatalf("ctrl+c with a selection changed the draft: %q", got)
	}
	app.Input.ClearInputSelection()

	// No selection: the draft clears instead of copying.
	_, cmd = app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+c without a selection must not emit a clipboard command")
	}
	if got := app.Input.Value(); got != "" {
		t.Fatalf("ctrl+c=%q want the draft cleared", got)
	}
}

// Ctrl+Shift+C is the explicit copy chord: it copies a selection and never
// clears the draft, so users coming from terminal copy chords get a pure copy.
func TestChatKeyCtrlShiftCIsPureCopy(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	app.Input.SetInput("hello world")
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift, Text: "shift+home"})
	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift})
	if cmd == nil {
		t.Fatal("ctrl+shift+c with a selection returned no clipboard command")
	}
	app.Input.ClearInputSelection()
	_, cmd = app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift})
	if cmd != nil {
		t.Fatal("ctrl+shift+c without a selection must not write the clipboard")
	}
	if got := app.Input.Value(); got != "hello world" {
		t.Fatalf("ctrl+shift+c cleared the draft: %q", got)
	}
}

// The question deck's write-in is an inline text control too: a right-click
// inside the deck resolves the editor the focus gate wraps, so the panel offers
// whole-text Copy/Cut instead of falling through to the message rows behind it.
func TestDeckQuestionWriteInOffersClipboardMenu(t *testing.T) {
	app, projection := setupSessionDeckApp(t)
	openTestAgentPrompt(t, app, projection, agentPromptRequest{kind: agentPromptQuestions, questionsJSON: `[{"question":"Continue?","options":["yes","no"]}]`})
	if !projection.deck.IsOpen() {
		t.Fatal("question panel did not open")
	}
	app.paintShellFrame()
	deckRect := app.sessionDeckRect
	if deckRect.Empty() {
		t.Fatal("deck rect unavailable after layout")
	}

	// The write-in gate sits in the deck's right pane; scan that column for it.
	x := deckRect.X + deckRect.W/2
	var hit textClipboardHit
	found := false
	hitY := 0
	for y := deckRect.Y; y < deckRect.Y+deckRect.H && !found; y++ {
		hit, found = app.deckTextClipboardHit(tea.Mouse{X: x, Y: y})
		hitY = y
	}
	if !found {
		t.Fatalf("no text control resolved inside the deck at column %d (rect=%+v)", x, deckRect)
	}
	if got := hit.target.TextValue(); got != "" {
		t.Fatalf("fresh write-in value=%q want empty", got)
	}

	// Typing into the write-in then makes the whole-text commands available.
	hit.target.ReplaceTextValue("typed answer")
	if got := hit.target.TextValue(); got != "typed answer" {
		t.Fatalf("write-in value=%q want 'typed answer'", got)
	}
	section := app.textClipboardMenuSection(hit)
	for _, item := range section.Items {
		if item.Disabled {
			t.Fatalf("%q must be enabled for a non-empty write-in", item.Label)
		}
	}

	// The right-click path opens that menu for the deck instead of the chat
	// message menu behind the panel.
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: x, Y: hitY, Button: tea.MouseRight})
	if app.LastContextMenu == nil {
		t.Fatal("right-click inside the deck opened no menu")
	}
}

// Popup text controls (settings-style forms) expose the same whole-text
// Copy/Cut, and Cut flows through the owning component so the form model stays
// in sync with the editor.
func TestPopupFormFieldMenuCutsThroughOwningField(t *testing.T) {
	app := newFullTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	nextWrite := recordingClipboard(t, app)

	tokens := ui.NewTokenSet(app.Theme)
	model := ui.NewFormModel()
	model.AddField(ui.TextFieldState("name", "Name", "", "Display name."))
	form := ui.NewForm("probe-form", model, "Form", "Edit", tokens)
	form.SetForm(model, "Form", "Edit")
	field := form.Field("name")
	if field == nil || field.Control() == nil {
		t.Fatal("probe form has no text field")
	}
	field.Control().SetValue("hello")
	model.SetValue("name", ui.StringValue("hello"))

	frame := ui.NewWindowFrameWidget(app.Theme, ui.NewID(ui.RootID, "probe-frame"),
		ui.FrameWindow, "Probe", form, nil, true, 40, 12)
	fw := wm.NewFloatingWindow("probe", "Probe", frame, 40, 12)
	fw.SetPosition(5, 3)

	// A real right-click lands in screen coordinates: the window origin plus
	// the body rect (the frame's content padding) plus the field's own row.
	winX, winY := fw.Position()
	bodyRect, ok := frame.Host().ScreenRect(form.ID())
	if !ok {
		t.Fatal("form is not mounted in the popup host")
	}
	mouse := tea.Mouse{
		X: winX + bodyRect.X + field.Bounds().X + 1,
		Y: winY + bodyRect.Y + field.Bounds().Y + 1,
	}
	hit, ok := windowTextClipboardHit(fw, mouse)
	if !ok {
		t.Fatalf("no text control resolved inside the popup at %+v (field bounds=%+v)", mouse, field.Bounds())
	}
	if got := hit.target.TextValue(); got != "hello" {
		t.Fatalf("resolved control value=%q want 'hello'", got)
	}

	st := app.textClipboardMenu("Probe", hit)
	activateMenuItem(t, st, "Copy")
	if got := nextWrite(); got != "hello" {
		t.Fatalf("Copy wrote %q want 'hello'", got)
	}

	activateMenuItem(t, st, "Cut")
	if got := nextWrite(); got != "hello" {
		t.Fatalf("Cut wrote %q want 'hello'", got)
	}
	if got := field.Control().Value(); got != "" {
		t.Fatalf("Cut left the editor=%q want empty", got)
	}
	if got := form.Field("name").FieldState().Value.String(); got != "" {
		t.Fatalf("Cut left the form model=%q — the edit must flow through the field", got)
	}
}

// A masked field never offers clipboard commands.
func TestPopupPasswordFieldMenuDisablesClipboard(t *testing.T) {
	app := newFullTestApp(t)
	model := ui.NewFormModel()
	model.AddField(ui.PasswordFieldState("key", "API key", "", "Secret."))
	form := ui.NewForm("probe-form", model, "Form", "Edit", ui.NewTokenSet(app.Theme))
	form.SetForm(model, "Form", "Edit")
	field := form.Field("key")
	if field == nil || field.Control() == nil {
		t.Fatal("probe form has no password field")
	}
	field.Control().SetValue("sk-secret")

	target := ui.TextClipboardTargetIn(field)
	if target == nil {
		t.Fatal("password field exposes no clipboard target")
	}
	section := app.textClipboardMenuSection(textClipboardHit{owner: field, target: target})
	for _, item := range section.Items {
		if !item.Disabled {
			t.Fatalf("%q must be disabled for a masked field", item.Label)
		}
	}
}
