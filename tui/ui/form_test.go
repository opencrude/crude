package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
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
	if got, want := buf.Get(10, 0).BG, tokens.Color(ColorAccent); got != want {
		t.Fatalf("primary action background=%v, want accent %v", got, want)
	}
	if got, want := buf.Get(20, 0).BG, tokens.Color(ColorError); got != want {
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

func TestFormSubsectionLabelsIndentFromSectionTitles(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	on := BoolValue(true)
	model.AddField(&FieldState{ID: "features", Label: "Features", Type: FieldSeparator})
	model.AddField(&FieldState{ID: "diagnostics", Label: "Diagnostics", Type: FieldToggle, Value: on, Initial: on})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	buf := surface.NewCellBuffer(40, 6)
	form.Paint(buf, 0, 0, PaintContext{})

	titleX, fieldX := -1, -1
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if titleX < 0 && buf.Get(x, y).Char == 'F' && x+7 < buf.Width() && buf.Get(x+1, y).Char == 'e' {
				titleX = x
			}
			if fieldX < 0 && buf.Get(x, y).Char == 'D' && x+10 < buf.Width() && buf.Get(x+1, y).Char == 'i' {
				fieldX = x
			}
		}
	}
	if titleX != 0 {
		t.Fatalf("section title x=%d want 0", titleX)
	}
	if fieldX != tokens.FormIndent() {
		t.Fatalf("field label x=%d want FormIndent %d", fieldX, tokens.FormIndent())
	}
}

func TestFormPaintClipsFieldsToViewport(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	on := BoolValue(true)
	model := NewFormModel()
	for i := 0; i < 8; i++ {
		model.AddField(&FieldState{
			ID:          fmt.Sprintf("f%d", i),
			Label:       fmt.Sprintf("Field %d", i),
			Description: "description that must not leak",
			Type:        FieldToggle,
			Value:       on,
			Initial:     on,
		})
	}
	form := NewForm("form", model, "", "", tokens)
	const formX, formY, formW, formH = 2, 2, 40, 5
	form.Layout(LayoutContext{}, Rect{X: formX, Y: formY, W: formW, H: formH})

	assertClipped := func(t *testing.T, buf *surface.CellBuffer) {
		t.Helper()
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				inside := x >= formX && x < formX+formW && y >= formY && y < formY+formH
				if inside {
					continue
				}
				if buf.Get(x, y).Char != '#' {
					t.Fatalf("form leaked %q at (%d,%d) outside viewport", string(buf.Get(x, y).Char), x, y)
				}
			}
		}
		found := false
		for y := formY; y < formY+formH; y++ {
			for x := formX; x < formX+formW; x++ {
				if buf.Get(x, y).Char != '#' && buf.Get(x, y).Char != 0 && buf.Get(x, y).Char != ' ' {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("viewport is empty after paint")
		}
	}

	paint := func() *surface.CellBuffer {
		buf := surface.NewCellBuffer(50, 16)
		sentinel := cell.Cell{Char: '#', Width: 1}
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				buf.Set(x, y, sentinel)
			}
		}
		form.Paint(buf, 0, 0, PaintContext{})
		return buf
	}

	t.Run("bottom straddle", func(t *testing.T) {
		assertClipped(t, paint())
	})
	t.Run("top straddle", func(t *testing.T) {
		form.Scroll().ScrollTo(1)
		form.Layout(LayoutContext{}, Rect{X: formX, Y: formY, W: formW, H: formH})
		assertClipped(t, paint())
	})
	t.Run("mid scroll", func(t *testing.T) {
		form.Scroll().ScrollTo(3)
		form.Layout(LayoutContext{}, Rect{X: formX, Y: formY, W: formW, H: formH})
		assertClipped(t, paint())
	})
}

func TestFormArrowsClampAtEdgesByDefault(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	empty := StringValue("")
	model.AddField(&FieldState{ID: "a", Label: "A", Type: FieldText, Value: empty, Initial: empty})
	model.AddField(&FieldState{ID: "b", Label: "B", Type: FieldText, Value: empty, Initial: empty})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	form.FocusFirst()

	res := form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if res.Action != nil {
		t.Fatalf("Up at first field emitted %v", res.Action)
	}

	form.FocusLast()
	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if res.Action != nil {
		t.Fatalf("Down at last field emitted %v", res.Action)
	}

	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyTab}))
	if res.Action == nil || res.Action.ID != "form.exit.forward" {
		t.Fatalf("Tab at last field result=%#v", res)
	}

	form.FocusFirst()
	form.SetArrowExitsForm(true)
	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if res.Action == nil || res.Action.ID != "form.exit.backward" {
		t.Fatalf("SetArrowExitsForm Up result=%#v", res)
	}
	form.FocusLast()
	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if res.Action == nil || res.Action.ID != "form.exit.forward" {
		t.Fatalf("SetArrowExitsForm Down result=%#v", res)
	}
}

func TestFormNavRowPaintsChevronNotPrompt(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{
		ID: "default", Label: "Main Agent", Description: "openrouter  ·  gpt-5",
		Type: FieldNav, Inherited: true, InheritedFrom: "workspace",
	})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 60, H: 4})
	buf := surface.NewCellBuffer(60, 4)
	form.Paint(buf, 0, 0, PaintContext{Focused: true})
	var b strings.Builder
	for y := 0; y < 4; y++ {
		for x := 0; x < 60; x++ {
			if c := buf.Get(x, y); c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	plain := b.String()
	if strings.Contains(plain, theme.DefaultGlyphs().Prompt) {
		t.Fatalf("nav row must not use the chat prompt glyph:\n%s", plain)
	}
	if strings.Contains(plain, ") )") || strings.Contains(plain, "(inherited") {
		t.Fatalf("nav row must not wrap inherited text in parentheses:\n%s", plain)
	}
	if !strings.Contains(plain, theme.DefaultGlyphs().TreeCollapsed) {
		t.Fatalf("nav row missing TreeCollapsed chevron:\n%s", plain)
	}
	if !strings.Contains(plain, "inherited from workspace") {
		t.Fatalf("nav row missing inherited detail:\n%s", plain)
	}
}
