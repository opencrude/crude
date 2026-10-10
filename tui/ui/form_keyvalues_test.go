package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// TestFormKeyValuesFieldCommitsThroughModel drives the field the way a host
// does: the retained grid edits rows, and the FormModel is the single source of
// truth for the value, dirty state, and change notifications.
func TestFormKeyValuesFieldCommitsThroughModel(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	initial := StringValue("X-Tenant: acme")
	changed := ""
	model.OnChanged(func(id string) { changed = id })
	model.AddField(&FieldState{ID: "headers", Label: "Extra headers", Type: FieldKeyValues, Value: initial, Initial: initial, Description: "Sent with every request.", Validator: func(v FieldValue) []string {
		for _, line := range strings.Split(v.String(), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if name, value, ok := strings.Cut(line, ":"); !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
				return []string{"Use one non-empty Name: value header per line."}
			}
		}
		return nil
	}})
	model.AddField(&FieldState{ID: "after", Label: "Next", Type: FieldText, Value: StringValue(""), Initial: StringValue("")})
	form := NewForm("form", model, "Template", "Editing", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(60, 16)
	host.Layout()
	host.Focus(form.ID())
	if !form.FocusField("headers") {
		t.Fatal("form could not focus the key/value field")
	}
	grid := form.Field("headers").Grid()
	if grid == nil {
		t.Fatal("form field retained no grid")
	}

	// Up/Down reach the grid instead of leaving the field.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "X-New" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "yes" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := model.Field("headers").Value.String(); got != "X-Tenant: acme\nX-New: yes" {
		t.Fatalf("model value=%q", got)
	}
	if !model.Field("headers").Dirty {
		t.Fatal("edited header set must mark the field dirty")
	}
	if changed != "headers" {
		t.Fatalf("change notification=%q want headers", changed)
	}

	// Tab still leaves the table for the next field.
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if !res.Handled {
		t.Fatal("Tab out of the table was not handled")
	}
	if grid.Editing() {
		t.Fatal("Tab must close the open cell editor")
	}
	if focused := form.Field("after").FieldState().ID; form.focused < 0 || form.fields[form.focused].FieldState().ID != focused {
		t.Fatalf("focus did not move to the next field: %d", form.focused)
	}

	// Validation errors paint under the table, not inside it. A host that
	// replaces the model rebinds the form, which syncs retained fields.
	model.SetValue("headers", StringValue("X-Broken"))
	model.ValidateAll()
	form.SetForm(model, "Template", "Editing")
	if !model.Field("headers").HasErrors() {
		t.Fatal("malformed header line must fail validation")
	}
	buf := surface.NewCellBuffer(60, 16)
	host.MarkLayout()
	host.Layout()
	host.Paint(buf)
	plain := plainGrid(buf, 60, 16)
	if !strings.Contains(plain, "Use one non-empty Name: value header") {
		t.Fatalf("validation message missing from the painted field:\n%s", plain)
	}
	if !strings.Contains(plain, "Sent with every request.") {
		t.Fatalf("description missing from the painted field:\n%s", plain)
	}
	if !strings.Contains(plain, "X-Broken") {
		t.Fatalf("malformed row missing from the painted table:\n%s", plain)
	}
}

func plainGrid(buf *surface.CellBuffer, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)
			if c.Char == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(c.Char)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
