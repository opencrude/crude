package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestSettingsPasteIntoAPIKey_UpdatesFieldAndScrollsToCursor(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {},
		},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	model.SelectCategory(catProviders)
	surfaceRoot.refreshForm()
	surfaceRoot.form.FocusFirst()
	surfaceRoot.FocusForm()
	host.Layout()

	// Focus moves to the API key field (provider is a select; second focusable).
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	field := surfaceRoot.form.Field(fieldAPIKey)
	if field == nil {
		t.Fatal("providers form missing api-key field")
	}
	control := field.Control()
	if control == nil {
		t.Fatal("api-key field has no text control")
	}
	control.SetValue("")

	key := "sk-ant-abcdefghijklmnopqrstuvwxyz1234567890"
	res := host.Handle(tea.PasteMsg{Content: key})
	if !res.Handled {
		t.Fatalf("paste into settings not handled (handled=%v)", res.Handled)
	}
	if got := control.Value(); got != key {
		t.Fatalf("api-key value after paste=%q, want %q", got, key)
	}
}

func TestSettingsPasteIntoAPIKey_MovesCursorIntoView(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {},
		},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	model.SelectCategory(catProviders)
	surfaceRoot.refreshForm()
	surfaceRoot.form.FocusFirst()
	surfaceRoot.FocusForm()
	host.Layout()

	// The api-key field is the second focusable field (after the provider
	// select), so one Down lands on it.
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	field := surfaceRoot.form.Field(fieldAPIKey)
	if field == nil {
		t.Fatal("providers form missing api-key field")
	}
	control := field.Control()
	if control == nil {
		t.Fatal("api-key field has no text control")
	}
	control.SetValue("")

	// A key longer than the field width must scroll the viewport so the
	// cursor (at end) is visible immediately after the paste.
	key := "sk-ant-abcdefghijklmnopqrstuvwxyz1234567890"
	_ = host.Handle(tea.PasteMsg{Content: key})
	host.MarkLayout()
	host.Layout()
	if !control.CursorVisible() {
		t.Fatalf("after paste cursor not visible: cursor=%d viewStart=%d width=%d",
			control.Edit().Cursor(), control.ViewStart(), control.Edit().Len())
	}
}

func TestSettingsCategories_AllFormsPopulateFields(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surface := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surface, nil, true, 90, 24)
	surface.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	if len(model.categories) == 0 {
		t.Fatal("no categories")
	}

	for i, category := range model.categories {
		model.SelectCategory(i)
		surface.refreshForm()
		host.MarkLayout()
		host.Layout()

		wantFields := category.Form.Fields()
		if len(wantFields) == 0 {
			t.Fatalf("category %q model has zero fields — fixture broken", category.Name)
		}
		for _, field := range wantFields {
			ff := surface.form.Field(field.ID)
			if ff == nil {
				t.Fatalf("category %q: form missing field %q after SelectCategory", category.Name, field.ID)
			}
			if ff.Bounds().Empty() {
				t.Fatalf("category %q: field %q has empty bounds (not laid out) — form rebuild without layout regress",
					category.Name, field.ID)
			}
		}

		// Paint and require at least one field label to appear in the buffer.
		buf := newTestBuffer(90, 24)
		host.Paint(buf)
		painted := bufferPlain(buf)
		foundLabel := false
		for _, field := range wantFields {
			if field.Label != "" && strings.Contains(painted, field.Label) {
				foundLabel = true
				break
			}
		}
		if !foundLabel {
			t.Fatalf("category %q: painted form contains none of the field labels %v\nplain=%q",
				category.Name, fieldLabels(wantFields), painted)
		}
	}
}

func TestSettingsCategories_SearchCategoryNameKeepsFields(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}}, nil)
	surface := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surface, nil, true, 90, 24)
	surface.SetFocusHandler(frame.Focus)
	surface.FocusSearch()
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	// Type a query that matches the Appearance category name only.
	for _, r := range "appear" {
		_ = host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	host.MarkLayout()
	host.Layout()

	if model.SelectedCategory() != catAppearance {
		t.Fatalf("selected=%d want Appearance (%d) after search appear", model.SelectedCategory(), catAppearance)
	}
	want := model.categories[catAppearance].Form.Fields()
	if len(want) == 0 {
		t.Fatal("Appearance model fields empty")
	}
	for _, field := range want {
		ff := surface.form.Field(field.ID)
		if ff == nil || ff.Bounds().Empty() {
			t.Fatalf("search 'appear' emptied Appearance field %q (nil=%v empty=%v)",
				field.ID, ff == nil, ff != nil && ff.Bounds().Empty())
		}
	}
	buf := newTestBuffer(90, 24)
	host.Paint(buf)
	painted := bufferPlain(buf)
	if !strings.Contains(painted, "preset") {
		t.Fatalf("search 'appear' left Appearance form without preset label; plain=%q", painted)
	}
}

func fieldLabels(fields []*ui.FieldState) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Label)
	}
	return out
}

func newTestBuffer(w, h int) *surface.CellBuffer {
	return surface.NewCellBuffer(w, h)
}

func bufferPlain(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
