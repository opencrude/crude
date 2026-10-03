package settings

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestSettingsScrollbarReachesBottomAcrossResize(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{ActiveProvider: "custom", Providers: map[string]types.ProviderConfig{"custom": provider.DefaultTemplates()["custom"]}}, nil)
	if err := model.beginProviderEdit(false, false); err != nil {
		t.Fatal(err)
	}
	s := NewSurface(th, model)
	host := ui.NewHost(th, s)
	paint := func() string {
		host.Layout()
		buf := newTestBuffer(s.Bounds().W, s.Bounds().H)
		host.Paint(buf)
		return bufferPlain(buf)
	}
	for _, size := range [][2]int{{100, 20}, {78, 22}, {120, 26}, {72, 18}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			host.Resize(size[0], size[1])
			paint()
			if !s.formScroll.Needed() {
				t.Fatal("fixture does not overflow")
			}
			r := s.formScroll.Bounds()
			top, _ := s.form.Scroll().ThumbBounds(r.H)
			host.Handle(tea.MouseClickMsg{X: r.X, Y: r.Y + top, Button: tea.MouseLeft})
			host.Handle(tea.MouseMotionMsg{X: r.X, Y: r.Y + r.H + 3, Button: tea.MouseLeft})
			host.Handle(tea.MouseReleaseMsg{X: r.X, Y: r.Y + r.H + 3, Button: tea.MouseLeft})
			immediate := paint()
			if s.form.Scroll().Offset() != s.form.Scroll().MaxOffset() {
				t.Fatalf("drag clamped before bottom: %d/%d", s.form.Scroll().Offset(), s.form.Scroll().MaxOffset())
			}
			if !strings.Contains(immediate, "Extra JSON") {
				t.Fatalf("final field not painted:\n%s", immediate)
			}
			host.MarkLayout()
			if settled := paint(); settled != immediate {
				t.Fatal("settled frame moved after scrollbar release")
			}
			if s.scope.Bounds().Y+s.scope.Bounds().H != s.Bounds().H {
				t.Fatal("scope toggle is not aligned with footer buttons")
			}
		})
	}
}

func TestSettingsPasteIntoAPIKey_UpdatesFieldAndScrollsToCursor(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": provider.DefaultTemplates()["openrouter"],
		},
		Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "openrouter", Label: "Personal"}},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	model.SelectCategory(catAccounts)
	surfaceRoot.refreshForm()
	if !surfaceRoot.form.FocusField(fieldAPIKey) {
		t.Fatal("API key field is not focusable")
	}
	surfaceRoot.FocusForm()
	host.MarkLayout()
	host.Layout()

	// Focus the masked account credential field.
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
			"openrouter": provider.DefaultTemplates()["openrouter"],
		},
		Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "openrouter", Label: "Personal"}},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 80, 22)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(80, 22)
	host.Layout()

	model.SelectCategory(catAccounts)
	surfaceRoot.refreshForm()
	if !surfaceRoot.form.FocusField(fieldAPIKey) {
		t.Fatal("API key field is not focusable")
	}
	surfaceRoot.FocusForm()
	host.MarkLayout()
	host.Layout()

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
		if category.Name == "Accounts" || category.Name == "Providers" || category.Name == "Agents" || category.Name == "Skills" || category.Name == "MCP" || category.Name == "Sources" {
			want := "Add account"
			if category.Name == "Providers" {
				want = "Add template"
			}
			if category.Name == "Agents" {
				want = "Main Agent"
			}
			if category.Name == "Skills" || category.Name == "MCP" || category.Name == "Sources" {
				want = "Installed"
			}
			if painted := bufferPlain(paintAccountSurface(t, surface, 90, 24)); !strings.Contains(painted, want) {
				t.Fatalf("%s overview table is not painted", category.Name)
			}
			continue
		}
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
