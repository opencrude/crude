package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// paintedForm lays out and paints a spacious settings-style form.
func paintedForm(t *testing.T, fields ...*FieldState) (*Form, *FormModel, *surface.CellBuffer) {
	t.Helper()
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	for _, field := range fields {
		model.AddField(field)
	}
	form := NewForm("form", model, "", "", tokens)
	form.SetHideDescriptions(true)
	form.Layout(LayoutContext{}, Rect{W: 90, H: 10})
	buf := surface.NewCellBuffer(90, 10)
	form.Paint(buf, 0, 0, PaintContext{})
	return form, model, buf
}

func clickField(form *Form, id string, x int) {
	field := form.Field(id)
	field.Handle(EventContext{LocalX: x, LocalY: 0}, Event{Kind: EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}})
}

// The stepper's ‹ must step to the previous value. It used to advance on
// every click, so "previous" went forward.
func TestCycleStepperClickPreviousGoesBack(t *testing.T) {
	cycle := CycleFieldState("preset", "Theme", []string{"one", "two", "three"}, "two", "")
	form, model, buf := paintedForm(t, cycle)
	left := formColOf(buf, 0, "‹")
	right := formColOf(buf, 0, "›")
	if left < 0 || right < 0 {
		t.Fatalf("stepper not painted:\n%s", formBufferPlain(buf))
	}
	clickField(form, "preset", left)
	if got := model.Field("preset").Value.String(); got != "one" {
		t.Fatalf("‹ selected %q, want previous one", got)
	}
	clickField(form, "preset", right)
	if got := model.Field("preset").Value.String(); got != "two" {
		t.Fatalf("› selected %q, want next two", got)
	}
}

func TestSegmentedClickSelectsThatSegment(t *testing.T) {
	segments := SegmentedFieldState("severity", "Show", []FormOption{{ID: "error", Label: "Errors"}, {ID: "warning", Label: "Warnings"}, {ID: "hint", Label: "Hints"}}, "error", "")
	form, model, buf := paintedForm(t, segments)
	x := formColOf(buf, 0, "Hints")
	if x < 0 || formColOf(buf, 0, "Warnings") < 0 {
		t.Fatalf("segments not painted inline:\n%s", formBufferPlain(buf))
	}
	clickField(form, "severity", x)
	if got := model.Field("severity").Value.String(); got != "hint" {
		t.Fatalf("clicked Hints, selected %q", got)
	}
}

// Spacious switches are a compact ‹ On › box in the stepper's language,
// sized to the widest state so both rows' arrows line up. Never circles.
func TestSwitchIsACompactArrowBox(t *testing.T) {
	form, model, buf := paintedForm(t, BoolFieldState("on", "Enabled", true, ""), BoolFieldState("off", "Hidden", false, ""))
	plain := formBufferPlain(buf)
	if strings.ContainsAny(plain, "●○") {
		t.Fatalf("switches must not paint circle markers:\n%s", plain)
	}
	on, off := form.Field("on"), form.Field("off")
	onRow, offRow := formRowText(buf, on.Bounds().Y), formRowText(buf, off.Bounds().Y)
	if !strings.Contains(onRow, "On") || !strings.Contains(offRow, "Off") {
		t.Fatalf("switch states not painted:\n%s", plain)
	}
	if strings.Index(onRow, "‹") != strings.Index(offRow, "‹") || strings.Index(onRow, "›") != strings.Index(offRow, "›") {
		t.Fatalf("switch arrows do not line up:\n%s", plain)
	}
	if w := off.boxWidth(off.Bounds().W); w > 12 {
		t.Fatalf("switch box width %d, want compact", w)
	}
	// Any click on the row flips it, including the label.
	clickField(form, "off", 1)
	if got := model.Field("off").Value.String(); got != "on" {
		t.Fatalf("label click left the switch %q", got)
	}
}

// Number inputs are short boxes with their unit beside them, never a slab
// spanning the pane.
func TestUnitFieldIsContentSized(t *testing.T) {
	form, _, buf := paintedForm(t, NumberFieldState("delay", "Update delay", 300, "ms", ""))
	if formColOf(buf, 0, "ms") < 0 {
		t.Fatalf("unit not painted:\n%s", formBufferPlain(buf))
	}
	if w := form.Field("delay").Control().Bounds().W; w > 20 {
		t.Fatalf("number input width %d, want a short box", w)
	}
}

// A stepper is an ordered scale: ‹ at the first value and › at the last hold
// still (and their arrows dim) instead of wrapping to the other end, for
// clicks and keys alike. Clicking the label column never steps.
func TestStepperHoldsAtItsEnds(t *testing.T) {
	cycle := CycleFieldState("tries", "Attempts", []string{"1", "2", "3"}, "1", "")
	form, model, buf := paintedForm(t, cycle)
	left, right := formColOf(buf, 0, "‹"), formColOf(buf, 0, "›")
	if left < 0 || right < 0 {
		t.Fatalf("stepper not painted:\n%s", formBufferPlain(buf))
	}
	tokens := NewTokenSet(theme.DefaultDark())
	if got := buf.Get(left, 0).FG; !got.Equal(tokens.Color(ColorMuted)) {
		t.Fatalf("‹ at the first value should dim, got %v", got)
	}
	clickField(form, "tries", left)
	if got := model.Field("tries").Value.String(); got != "1" {
		t.Fatalf("‹ at the first value wrapped to %q", got)
	}
	clickField(form, "tries", 1)
	if got := model.Field("tries").Value.String(); got != "1" {
		t.Fatalf("label click stepped to %q", got)
	}
	field := form.Field("tries")
	key := func(code rune) {
		field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: code}))
	}
	key(tea.KeyLeft)
	if got := model.Field("tries").Value.String(); got != "1" {
		t.Fatalf("Left at the first value wrapped to %q", got)
	}
	key(tea.KeyRight)
	key(tea.KeyRight)
	key(tea.KeyRight)
	if got := model.Field("tries").Value.String(); got != "3" {
		t.Fatalf("Right past the last value = %q, want it held at 3", got)
	}
	clickField(form, "tries", right)
	if got := model.Field("tries").Value.String(); got != "3" {
		t.Fatalf("› at the last value wrapped to %q", got)
	}
}

func TestHelpFieldFollowsHoverThenFocus(t *testing.T) {
	form, _, _ := paintedForm(t, BoolFieldState("a", "Alpha", true, "alpha help"), BoolFieldState("b", "Beta", true, "beta help"))
	if got := form.HelpField(); got == nil || got.ID != "a" {
		t.Fatalf("help = %+v, want focused alpha", got)
	}
	form.hovered = 1
	if got := form.HelpField(); got == nil || got.ID != "b" {
		t.Fatalf("help = %+v, want hovered beta", got)
	}
}
