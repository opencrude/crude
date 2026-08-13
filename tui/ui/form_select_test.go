package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// newSelectForm builds a form with one FieldSelect over the supplied options
// and returns the form and the bound field. Used by the dropdown filter /
// hover-preview / commit tests so each starts from the same shape.
func newSelectForm(t *testing.T, options []FormOption) (*Form, *FormField) {
	t.Helper()
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	value := StringValue(options[0].ID)
	model.AddField(&FieldState{
		ID: "model", Label: "Model", Type: FieldSelect,
		Value: value, Initial: value, Options: options,
	})
	form := NewForm("form", model, "Form", "Pick a model", tokens)
	form.Layout(LayoutContext{}, Rect{W: 60, H: 12})
	return form, form.Field("model")
}

func TestSelectFilterNarrowsByIDLabelDetail(t *testing.T) {
	options := []FormOption{
		{ID: "anthropic/claude-opus-4-7", Label: "Claude Opus 4.7", Detail: "200K"},
		{ID: "openai/gpt-5", Label: "GPT-5", Detail: "128K"},
		{ID: "google/gemini-2.5-pro", Label: "Gemini 2.5 Pro", Detail: "1M"},
	}
	_, field := newSelectForm(t, options)

	field.selectOpen = true
	field.selectQuery = "claude"
	field.syncSelectItems(false)

	if items := field.selectList.Items(); len(items) != 1 || items[0].ID != "anthropic/claude-opus-4-7" {
		t.Fatalf("filter by ID substring: items=%#v", field.selectList.Items())
	}

	field.selectQuery = "1m"
	field.syncSelectItems(false)
	if items := field.selectList.Items(); len(items) != 1 || items[0].ID != "google/gemini-2.5-pro" {
		t.Fatalf("filter by Detail substring: items=%#v", field.selectList.Items())
	}

	field.selectQuery = "gemini"
	field.syncSelectItems(false)
	if items := field.selectList.Items(); len(items) != 1 || items[0].ID != "google/gemini-2.5-pro" {
		t.Fatalf("filter by Label substring: items=%#v", field.selectList.Items())
	}
}

func TestSelectOpenAndFilterKeepCommittedValue(t *testing.T) {
	options := []FormOption{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	}
	form, field := newSelectForm(t, options)
	model := form.model

	field.selectOpen = true
	field.syncSelectItems(false)
	field.applySelectFilter("ga")
	field.selectList.SelectIndex(0)

	if got := model.Field("model").Value.String(); got != "a" {
		t.Fatalf("opening or filtering changed value=%q want a", got)
	}
}

func TestSelectKeyboardArrowThenEnterCommits(t *testing.T) {
	options := []FormOption{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	}
	form, field := newSelectForm(t, options)
	model := form.model

	// Open the dropdown.
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))

	// Arrow down once moves the list cursor; Enter commits Beta.
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if got := model.Field("model").Value.String(); got != "a" {
		t.Fatalf("arrow navigation changed value=%q want a before commit", got)
	}
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))

	if got := model.Field("model").Value.String(); got != "b" {
		t.Fatalf("committed value=%q want b", got)
	}
	if field.selectOpen {
		t.Fatal("dropdown should close on commit")
	}
	if field.selectQuery != "" {
		t.Fatalf("query should clear on commit: query=%q", field.selectQuery)
	}
}

func TestSelectFilterThenEnterCommitsFilteredSelection(t *testing.T) {
	options := []FormOption{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	}
	form, field := newSelectForm(t, options)
	model := form.model

	// Open, then type a query that narrows to one row, then commit it.
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'g', Text: "g"}))
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))

	if field.selectQuery != "ga" {
		t.Fatalf("selectQuery=%q want ga", field.selectQuery)
	}
	if items := field.selectList.Items(); len(items) != 1 || items[0].ID != "c" {
		t.Fatalf("filtered items=%#v", field.selectList.Items())
	}

	// Enter in the filter emits form.select.commit; commit applies the
	// (only visible) selection.
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))

	if got := model.Field("model").Value.String(); got != "c" {
		t.Fatalf("committed value=%q want c", got)
	}
	if field.selectOpen || field.selectQuery != "" {
		t.Fatalf("dropdown/filter should reset on commit: open=%v query=%q", field.selectOpen, field.selectQuery)
	}
}

func TestSelectEscClearsFilterThenCloses(t *testing.T) {
	options := []FormOption{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
	}
	_, field := newSelectForm(t, options)

	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	if field.selectQuery != "a" {
		t.Fatalf("selectQuery=%q want a", field.selectQuery)
	}

	// First Esc clears the filter (FilterField swallows Esc when non-empty).
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if field.selectQuery != "" {
		t.Fatalf("Esc should clear filter first: query=%q", field.selectQuery)
	}
	if !field.selectOpen {
		t.Fatal("dropdown should still be open after Esc cleared the filter")
	}

	// Second Esc closes the dropdown.
	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if field.selectOpen {
		t.Fatal("second Esc should close the dropdown")
	}
}

func TestSelectSelectionPreservedAcrossFilter(t *testing.T) {
	options := []FormOption{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	}
	_, field := newSelectForm(t, options)

	field.selectOpen = true
	field.selectList.SelectIndex(2) // Gamma
	field.selectQuery = "a"
	field.syncSelectItems(true)

	// "a" matches both Alpha and Gamma. Gamma is the previously selected
	// row; syncSelectItems must preserve it so the cursor doesn't snap back
	// to the first match every time the user types.
	if selected, ok := field.selectList.Selected(); !ok || selected.ID != "c" {
		t.Fatalf("selection should be preserved across filter: %#v", selected)
	}
}

func TestSelectClickOutsidePanelCancelsWithoutCommit(t *testing.T) {
	options := []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "c", Label: "Gamma"}}
	form, field := newSelectForm(t, options)
	model := form.model

	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !field.SelectOpen() {
		t.Fatal("dropdown did not open")
	}
	// Field is at Y=3 (form header), control column starts at controlX. A
	// click in the label column on an option row is outside the panel.
	form.Handle(EventContext{}, MouseClickEvent(tea.MouseClickMsg{X: 2, Y: 6, Button: tea.MouseLeft}))
	if field.SelectOpen() {
		t.Fatal("click outside the dropdown panel must cancel the dropdown")
	}
	if got := model.Field("model").Value.String(); got != "a" {
		t.Fatalf("cancel must not commit: value=%q want a", got)
	}
}

func TestSelectClickControlRowTogglesClosed(t *testing.T) {
	options := []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}
	form, field := newSelectForm(t, options)

	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	form.Handle(EventContext{}, MouseClickEvent(tea.MouseClickMsg{X: 24, Y: field.Bounds().Y, Button: tea.MouseLeft}))
	if field.SelectOpen() {
		t.Fatal("clicking the control row of an open dropdown must close it")
	}
}

func TestFormClickOtherFieldCancelsOpenSelect(t *testing.T) {
	model := NewFormModel()
	value := StringValue("a")
	model.AddField(&FieldState{ID: "model", Label: "Model", Type: FieldSelect, Value: value, Initial: value, Options: []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}})
	text := StringValue("")
	model.AddField(&FieldState{ID: "note", Label: "Note", Type: FieldText, Value: text, Initial: text})
	form := NewForm("form", model, "Form", "Pick", NewTokenSet(theme.DefaultDark()))
	form.Layout(LayoutContext{}, Rect{W: 60, H: 12})
	field := form.Field("model")

	field.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !field.SelectOpen() {
		t.Fatal("dropdown did not open")
	}
	// Click the other field's row while the dropdown is open: the dropdown
	// cancels and focus does NOT move (the click is swallowed).
	other := form.Field("note")
	form.Handle(EventContext{}, MouseClickEvent(tea.MouseClickMsg{X: 24, Y: other.Bounds().Y, Button: tea.MouseLeft}))
	if field.SelectOpen() {
		t.Fatal("click outside the open dropdown must cancel it")
	}
	if form.fields[form.focused].FieldState().ID != "model" {
		t.Fatalf("focus moved on cancel click: %q", form.fields[form.focused].FieldState().ID)
	}
}

func TestMultiSelectFilterPreservedAfterToggle(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	value := StringsValue(nil)
	options := []FormOption{
		{ID: "foo-1", Label: "Foo One"},
		{ID: "foo-2", Label: "Foo Two"},
		{ID: "bar-1", Label: "Bar One"},
	}
	model.AddField(&FieldState{
		ID: "choices", Label: "Choices", Type: FieldMultiSelect,
		Value: value, Initial: value, Options: options,
	})
	form := NewForm("form", model, "Form", "Pick many", tokens)
	form.Layout(LayoutContext{}, Rect{W: 60, H: 12})
	field := form.Field("choices")

	field.selectOpen = true
	field.applySelectFilter("foo")
	if items := field.selectList.Items(); len(items) != 2 {
		t.Fatalf("filter should narrow to two foo items: %#v", items)
	}

	// Toggle the first foo item via the existing commit path.
	field.selectList.SelectIndex(0)
	field.commitSelect()

	if got := model.Field("choices").Value.Strings(); len(got) != 1 || got[0] != "foo-1" {
		t.Fatalf("toggled values=%v want [foo-1]", got)
	}
	if !field.selectOpen {
		t.Fatal("multi-select should stay open after toggle")
	}
	if field.selectQuery != "foo" {
		t.Fatalf("filter should be preserved after multi-select toggle: %q", field.selectQuery)
	}
}

func TestSelectOverlayDoesNotGrowFormHeight(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	selectValue := StringValue("a")
	nextValue := StringValue("next")
	model.AddField(&FieldState{
		ID: "model", Label: "Model", Type: FieldSelect,
		Value: selectValue, Initial: selectValue,
		Options: []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "c", Label: "Gamma"}},
	})
	model.AddField(&FieldState{ID: "next", Label: "Next", Type: FieldText, Value: nextValue, Initial: nextValue})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 8})
	closed := form.Scroll().Content()

	field := form.Field("model")
	field.selectOpen = true
	field.syncSelectItems(true)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 8})
	if got := form.Scroll().Content(); got != closed {
		t.Fatalf("opening a select grew form content %d → %d", closed, got)
	}
}

func TestSelectOverlayPaintsOverLaterFields(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	selectValue := StringValue("a")
	nextValue := StringValue("next")
	model.AddField(&FieldState{
		ID: "model", Label: "Model", Type: FieldSelect,
		Value: selectValue, Initial: selectValue,
		Options: []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "c", Label: "Gamma"}},
	})
	model.AddField(&FieldState{ID: "next", Label: "Next", Type: FieldText, Value: nextValue, Initial: nextValue})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 8})

	buf := surface.NewCellBuffer(40, 8)
	form.Paint(buf, 0, 0, PaintContext{})
	if !bufferHasRunes(buf, "next") {
		t.Fatal("closed form should show the following field value")
	}

	field := form.Field("model")
	field.selectOpen = true
	field.syncSelectItems(true)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 8})
	buf = surface.NewCellBuffer(40, 8)
	form.Paint(buf, 0, 0, PaintContext{})
	if !bufferHasRunes(buf, "Alpha") {
		t.Fatal("open dropdown should paint its options over later fields")
	}
	if bufferHasRunes(buf, "next") {
		t.Fatal("open dropdown must overlay later fields instead of pushing them down")
	}
	if !bufferHasRunes(buf, "╭") || !bufferHasRunes(buf, "╰") {
		t.Fatal("open dropdown should paint rounded box chrome")
	}
}

func TestSelectOverlayClickDoesNotActivateCoveredField(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	selectValue := StringValue("a")
	nextValue := StringValue("keep")
	model.AddField(&FieldState{
		ID: "model", Label: "Model", Type: FieldSelect,
		Value: selectValue, Initial: selectValue,
		Options: []FormOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "c", Label: "Gamma"}},
	})
	model.AddField(&FieldState{ID: "next", Label: "Next", Type: FieldText, Value: nextValue, Initial: nextValue})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 8})
	field := form.Field("model")
	field.selectOpen = true
	field.syncSelectItems(true)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 8})
	form.focused = 0

	overlay := field.Bounds()
	clickX := overlay.X + field.controlX(overlay.W) + field.controlWidth(overlay.W)/2
	clickY := overlay.Y + 2
	result := form.Handle(EventContext{LocalX: clickX, LocalY: clickY}, MouseClickEvent(tea.MouseClickMsg{X: clickX, Y: clickY, Button: tea.MouseLeft}))
	if !result.Handled {
		t.Fatal("click on overlay should be handled by the open select")
	}
	if form.focused != 0 {
		t.Fatalf("overlay click moved focus to %d", form.focused)
	}
}

func bufferHasRunes(buf *surface.CellBuffer, want string) bool {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for i, r := range runes {
				if buf.Get(x+i, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}
