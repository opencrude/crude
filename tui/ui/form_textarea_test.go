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
	// Label row + 4 editor rows + description row.
	if got := field.height(); got != 6 {
		t.Fatalf("height = %d, want 6", got)
	}
	field.field.Rows = 6
	if got := field.height(); got != 8 {
		t.Fatalf("height with Rows=6 = %d, want 8", got)
	}
}

func TestTextAreaFieldPaintsBelowLabel(t *testing.T) {
	form, _ := newTextAreaForm(t)
	buf := surface.NewCellBuffer(60, 20)
	form.Paint(buf, 0, 0, PaintContext{Focused: true})
	// The label paints on the field's first row and the editor occupies the
	// rows below it; nothing else needs to hold for the height contract.
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
