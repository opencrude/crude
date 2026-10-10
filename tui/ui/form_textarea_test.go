package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// newTextAreaForm builds a form with a multi-line field followed by a plain
// text field, so focus-movement interactions have a neighbor to land on.
func newTextAreaForm(t *testing.T) (*Form, *FormField) {
	t.Helper()
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(TextAreaFieldState("body", "Extra JSON body", "", "Raw JSON object merged into every request."))
	model.AddField(TextFieldState("after", "Neighbor", "", "Plain single-line field."))
	form := NewForm("form", model, "Form", "Edit", tokens)
	form.Layout(LayoutContext{}, Rect{W: 60, H: 20})
	return form, form.Field("body")
}

func TestTextAreaFieldHeightSpansRows(t *testing.T) {
	_, field := newTextAreaForm(t)
	// 4 editor rows starting on the label row, then the description row.
	if got := field.height(); got != 5 {
		t.Fatalf("height = %d, want 5", got)
	}
	field.field.Rows = 6
	if got := field.height(); got != 7 {
		t.Fatalf("height with Rows=6 = %d, want 7", got)
	}
}

func TestTextAreaFieldPaintsInValueColumn(t *testing.T) {
	form, field := newTextAreaForm(t)
	field.Area().SetValue("x")
	buf := surface.NewCellBuffer(60, 20)
	form.Paint(buf, 0, 0, PaintContext{Focused: true})
	fr := field.Bounds()
	// The editor box starts in the value column on the label row.
	editorX := fr.X + field.valueX(fr.W) + 1
	editorY := fr.Y
	if got := buf.Get(editorX, editorY).Char; got != 'x' {
		t.Fatalf("textarea content row=%q want x at (%d,%d)", string(got), editorX, editorY)
	}
	descriptionY := fr.Y + field.areaRows()
	insideDescription := buf.Get(editorX, descriptionY)
	farDescription := buf.Get(fr.X+fr.W-1, descriptionY)
	if !insideDescription.BG.Equal(farDescription.BG) {
		t.Fatalf("textarea fill leaked into description row: inside=%v far=%v", insideDescription.BG, farDescription.BG)
	}
	if got := rowText(buf, descriptionY); !strings.Contains(got, "Raw JSON object") {
		t.Fatalf("description row was overwritten: %q", got)
	}
}

func TestTextAreaEnterInsertsNewlineInsteadOfAdvancing(t *testing.T) {
	form, field := newTextAreaForm(t)
	area := field.Area()
	if area == nil {
		t.Fatal("multi-line field has no area control")
	}
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'b', Text: "b"}))

	got := field.FieldState().Value.String()
	if got != "a\nb" {
		t.Fatalf("value = %q, want %q", got, "a\nb")
	}
	if form.fields[form.focused] != field {
		t.Fatal("Enter moved focus out of the multi-line field")
	}
}

func TestTextAreaArrowsMoveCaretNotFocus(t *testing.T) {
	form, field := newTextAreaForm(t)
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))

	if form.fields[form.focused] != field {
		t.Fatal("Up moved focus; it must reach the caret inside the area")
	}
	if field.Area().CurLine() != 0 {
		t.Fatalf("caret line = %d, want 0 after Up", field.Area().CurLine())
	}
}

func TestTextAreaTabLeavesField(t *testing.T) {
	form, field := newTextAreaForm(t)
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyTab}))
	if form.fields[form.focused] == field {
		t.Fatal("Tab did not leave the multi-line field")
	}
}

func TestTextAreaSyncReflectsProgrammaticValue(t *testing.T) {
	form, field := newTextAreaForm(t)
	form.model.SetValue("body", StringValue(`{"k": 1}`))
	// Surfaces push programmatic model changes through SetForm, which Syncs
	// the retained editors (same structure → no rebuild).
	form.SetForm(form.model, "Form", "Edit")
	got := field.Area().Value()
	if got != `{"k": 1}` {
		t.Fatalf("area value = %q, want programmatic value", got)
	}
	if !strings.Contains(field.FieldState().Value.String(), `"k"`) {
		t.Fatalf("field value = %q", field.FieldState().Value.String())
	}
}

func TestFormTextAreaDragUsesHostCaptureAndCopiesOnRelease(t *testing.T) {
	form, field := newTextAreaForm(t)
	form.model.SetValue("body", StringValue("alpha beta"))
	form.SetForm(form.model, "Form", "Edit")
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(60, 20)
	host.Layout()
	var copied []string
	field.Area().SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			copied = append(copied, text)
			return nil
		}
	})
	fr := field.Bounds()
	ar := field.Area().Bounds()
	x, y := fr.X+ar.X, fr.Y+ar.Y
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	if !host.HasPointerCapture() || !field.Area().HasActiveSelection() {
		owner, _, _ := host.CapturedPointer()
		t.Fatalf("drag owner=%q capture=%v selecting=%v range=%v selection=%q", owner, host.HasPointerCapture(), field.Area().IsSelecting(), func() [2]int { a, b := field.Area().SelectionRange(); return [2]int{a, b} }(), field.Area().SelectedText())
	}
	// The drag itself is highlighted before release.
	buf := surface.NewCellBuffer(60, 20)
	host.Paint(buf)
	selectedBG := NewTokenSet(theme.DefaultDark()).Color(ColorTextSelection)
	if got := buf.Get(x, y).BG; !got.Equal(selectedBG) {
		t.Fatalf("drag selection bg=%v want %v", got, selectedBG)
	}

	res := host.Handle(tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("release kept pointer capture")
	}
	if field.Area().HasActiveSelection() || field.Area().IsSelecting() {
		t.Fatalf("release kept selection=%q selecting=%v", field.Area().SelectedText(), field.Area().IsSelecting())
	}
	if res.Cmd == nil {
		t.Fatal("release returned no clipboard command")
	}
	if len(copied) != 0 {
		t.Fatalf("clipboard write ran synchronously: copied=%q", copied)
	}
	_ = res.Cmd()
	if len(copied) != 1 || copied[0] != "alpha" {
		t.Fatalf("copied=%q want [alpha]", copied)
	}
}

func TestFormTextAreaOwnsPageNavigation(t *testing.T) {
	form, field := newTextAreaForm(t)
	form.model.SetValue("body", StringValue("one\ntwo\nthree\nfour\nfive\nsix"))
	form.SetForm(form.model, "Form", "Edit")
	field.Area().SetCursorFlat(0)
	form.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyPgDown}))
	if field.Area().CurLine() == 0 {
		t.Fatal("PageDown scrolled the form instead of moving the textarea caret")
	}
	if form.fields[form.focused] != field {
		t.Fatal("PageDown moved focus out of textarea")
	}
}
