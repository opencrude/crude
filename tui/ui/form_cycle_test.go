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

func TestSwitchPaintsStateMarkerNotArrows(t *testing.T) {
	_, _, buf := paintedForm(t, BoolFieldState("on", "Enabled", true, ""), BoolFieldState("off", "Hidden", false, ""))
	plain := formBufferPlain(buf)
	if !strings.Contains(plain, "● On") || !strings.Contains(plain, "○ Off") || strings.Contains(plain, "←") {
		t.Fatalf("switches must read ● On / ○ Off:\n%s", plain)
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
