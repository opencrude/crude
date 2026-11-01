package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestFormFocusUsesCurrentValidationGeometry(t *testing.T) {
	model := NewFormModel()
	before := TextFieldState("before", "Before", "", "")
	target := TextFieldState("target", "Cap", "4200", "")
	model.AddField(before)
	model.AddField(target)
	model.AddField(TextFieldState("tail", "Tail", "untouched", ""))
	form := NewForm("form", model, "", "", NewTokenSet(theme.DefaultDark()))
	form.SetFieldGap(1)
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(38, 4)
	host.Layout()
	host.Focus(form.ID())
	// Save/combined validation can change preceding row heights before a new
	// host layout. Native Tab must reveal its target on the first frame.
	before.Errors = []string{"first error", "second error", "third error"}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	host.Layout()
	buf := surface.NewCellBuffer(38, 4)
	host.Paint(buf)
	var plain strings.Builder
	for y := 0; y < 4; y++ {
		for x := 0; x < 38; x++ {
			plain.WriteRune(buf.Get(x, y).Char)
		}
	}
	if !strings.Contains(plain.String(), "4200") {
		t.Fatalf("native focus left its current validated row offscreen: %s", plain.String())
	}
}

func TestFormFocusBeforeFirstLayoutRevealsTargetOnce(t *testing.T) {
	model := NewFormModel()
	for _, id := range []string{"one", "two", "three", "four", "target"} {
		model.AddField(TextFieldState(id, id, id, ""))
	}
	form := NewForm("form", model, "", "", NewTokenSet(theme.DefaultDark()))
	form.SetFieldGap(1)
	if !form.FocusField("target") {
		t.Fatal("focus target was not retained")
	}
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(38, 3)
	host.Layout()
	buf := surface.NewCellBuffer(38, 3)
	host.Paint(buf)
	var plain strings.Builder
	for y := range 3 {
		for x := range 38 {
			plain.WriteRune(buf.Get(x, y).Char)
		}
	}
	if !strings.Contains(plain.String(), "target") {
		t.Fatalf("first mounted frame lost the prepared focus target: %s", plain.String())
	}
	// Once the explicit reveal was consumed, layout must not steal the viewport
	// back from the reader's scroll model while the same field retains focus.
	form.Scroll().ScrollTo(0)
	host.MarkLayout()
	host.Layout()
	if form.Scroll().Offset() != 0 {
		t.Fatal("ordinary layout repeated a consumed focus reveal over reader scrolling")
	}
}
