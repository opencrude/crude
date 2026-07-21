package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestFormRoutesPasteAndMouseToRetainedFields(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	textValue := StringValue("before")
	cycleValue := StringValue("one")
	model := NewFormModel()
	model.AddField(&FieldState{ID: "text", Label: "Text", Type: FieldText, Value: textValue, Initial: textValue})
	model.AddField(&FieldState{ID: "cycle", Label: "Cycle", Type: FieldCycle, Value: cycleValue, Initial: cycleValue, Options: []FormOption{{ID: "one", Label: "one"}, {ID: "two", Label: "two"}}})
	form := NewForm("form", model, "Form", "Shared field interactions", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.RegisterTree(form)
	host.Resize(60, 10)
	host.Layout()
	host.Focus(form.ID())

	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.PasteMsg{Content: "after"})
	if got := model.Field("text").Value.String(); got != "after" {
		t.Fatalf("pasted text=%q, want after", got)
	}

	// The second field starts after the title, detail, divider, and first row.
	host.Handle(tea.MouseClickMsg{X: 20, Y: 4, Button: tea.MouseLeft})
	if got := model.Field("cycle").Value.String(); got != "two" {
		t.Fatalf("mouse cycle value=%q, want two", got)
	}
}

func TestActionBarPaintsSemanticVariants(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	bar := NewActionBar("actions", tokens,
		ActionBarButton{Label: "Save", Action: "save", Variant: VariantPrimary, Enabled: true},
		ActionBarButton{Label: "Delete", Action: "delete", Variant: VariantDanger, Enabled: true},
	)
	bar.Layout(LayoutContext{Theme: theme.DefaultDark()}, Rect{W: 30, H: 1})
	buf := surface.NewCellBuffer(30, 1)
	bar.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark()})
	if got, want := buf.Get(0, 0).BG, tokens.Color(ColorAccent); got != want {
		t.Fatalf("primary action background=%v, want accent %v", got, want)
	}
	if got, want := buf.Get(10, 0).BG, tokens.Color(ColorError); got != want {
		t.Fatalf("danger action background=%v, want error %v", got, want)
	}
}
