package ui

import (
	"strings"
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

func TestFormFieldPaintsDirtyMarkerOnModifiedLabel(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	value := StringValue("4")
	model.AddField(&FieldState{ID: "tab-size", Label: "Tab size", Type: FieldText, Value: value, Initial: value})
	form := NewForm("form", model, "Form", "Dirty tracking", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(60, 8)
	host.Layout()

	paint := func() string {
		buf := surface.NewCellBuffer(60, 8)
		host.Paint(buf)
		var b strings.Builder
		for y := 0; y < 8; y++ {
			for x := 0; x < 60; x++ {
				if c := buf.Get(x, y); c.Char != 0 {
					b.WriteRune(c.Char)
				}
			}
			b.WriteByte('\n')
		}
		return b.String()
	}

	if plain := paint(); strings.Contains(plain, "Tab size •") {
		t.Fatalf("unmodified field must not paint dirty marker:\n%s", plain)
	}
	model.SetValue("tab-size", StringValue("8"))
	if !model.Field("tab-size").Dirty {
		t.Fatal("field must be dirty after SetValue diverges from Initial")
	}
	if plain := paint(); !strings.Contains(plain, "Tab size •") {
		t.Fatalf("dirty field must paint • marker on the label:\n%s", plain)
	}
	model.Commit()
	if plain := paint(); strings.Contains(plain, "Tab size •") {
		t.Fatalf("committed field must drop the dirty marker:\n%s", plain)
	}
}

func TestFormMultiSelectTogglesChoicesWithoutLosingCursor(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	value := StringsValue(nil)
	model.AddField(&FieldState{
		ID: "choices", Label: "Choices", Type: FieldMultiSelect, Value: value, Initial: value, Required: true,
		Options: []FormOption{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}},
	})
	form := NewForm("form", model, "Form", "Multiple answers", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(60, 10)
	host.Layout()
	host.Focus(form.ID())

	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	values := model.Field("choices").Value.Strings()
	if len(values) != 2 || values[0] != "one" || values[1] != "two" {
		t.Fatalf("multi-select values=%v", values)
	}
	if selected, ok := form.Field("choices").selectList.Selected(); !ok || selected.ID != "two" {
		t.Fatalf("multi-select cursor=%#v", selected)
	}
}
