package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	"crude/tui/widget"
)

func TestAppSettingsPreserveProvidersAndAgentDefaults(t *testing.T) {
	th := theme.DefaultDark()
	current := types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {APIKey: "key-a", Model: "model-a", ThinkingEffort: "low"},
			"openai":     {APIKey: "key-b", Model: "model-b", ThinkingEffort: "high"},
		},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "main-model", ThinkingEffort: "medium"},
			"crafter": {Provider: "openai", Model: "craft-model", ThinkingEffort: "high"},
		}},
	}
	m := New(th, current, []types.ProviderPreset{
		{ID: "openrouter", Name: "OpenRouter"},
		{ID: "openai", Name: "OpenAI"},
	})

	got := m.AppSettings()
	if got.Providers["openrouter"].Model != "model-a" || got.Providers["openai"].Model != "model-b" {
		t.Fatalf("provider defaults were lost: %#v", got.Providers)
	}
	if got.Agents.Categories["default"].Model != "main-model" || got.Agents.Categories["crafter"].Model != "craft-model" {
		t.Fatalf("agent defaults were lost: %#v", got.Agents.Categories)
	}
}

func TestSettingsSurfacePaintAndNavigation(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 90, 28)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surfaceRoot.FocusSearch()
	surf := surface.NewSurface("settings-test", 90, 28, cell.TransparentBlank())
	cw.Render(surf, &widget.RenderContext{Theme: th})

	for _, text := range []string{"Search settings", "AI", "IDE", "Save", "Cancel"} {
		if !bufferContains(surf.Buffer(), text) {
			t.Fatalf("settings surface is missing %q", text)
		}
	}

	cw.Host().Handle(tea.MouseClickMsg{X: 2, Y: 4, Button: tea.MouseLeft})
	if m.SelectedCategory() != catMain || m.Pane() != 0 {
		t.Fatalf("category click selected=%d pane=%d, want %d/0", m.SelectedCategory(), m.Pane(), catMain)
	}

	surfaceRoot.FocusForm()
	result := cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !result.Handled || !surfaceRoot.form.Field(fieldProvider).SelectOpen() {
		t.Fatal("native provider field should open its shared select control")
	}
}

func TestSettingsSearchFiltersGroupedNavigation(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 80, 24)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surfaceRoot.FocusSearch()
	for _, r := range "font" {
		cw.Host().Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !m.searchActive || m.SelectedCategory() != catFont {
		t.Fatalf("search active=%v selected=%d, want font", m.searchActive, m.SelectedCategory())
	}
	if items := m.CategoryItems(); len(items) != 2 || items[1].Label != "Font" {
		t.Fatalf("filtered category items = %#v, want only Font", items)
	}

	surf := surface.NewSurface("settings-search-test", 80, 24, cell.TransparentBlank())
	cw.Render(surf, &widget.RenderContext{Theme: th})
	if !bufferContains(surf.Buffer(), "Font") || bufferContains(surf.Buffer(), "Providers") {
		t.Fatal("filtered settings categories were not rendered")
	}
}

func TestSettingsSurfaceActionBarEmitsMessages(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 80, 24)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surf := surface.NewSurface("settings-action-bar", 80, 24, cell.TransparentBlank())
	cw.Render(surf, &widget.RenderContext{Theme: th})
	cw.Focus(WorkspaceSettingsActionID)
	result := cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("save button did not emit a command")
	}
	msg := result.Cmd()
	if _, ok := msg.(SaveMsg); !ok {
		t.Fatalf("save command message = %T, want SaveMsg", msg)
	}
}

func TestSettingsSurfaceNativeFormPersistsTextAndCategoryChanges(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 90, 28)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surf := surface.NewSurface("settings-native-form", 90, 28, cell.TransparentBlank())
	cw.Render(surf, &widget.RenderContext{Theme: th})

	// The Editor category follows the AI group and Appearance rows.
	cw.Host().Handle(tea.MouseClickMsg{X: 2, Y: 10, Button: tea.MouseLeft})
	if m.SelectedCategory() != catEditor {
		t.Fatalf("selected category=%d, want editor", m.SelectedCategory())
	}
	if got := surfaceRoot.form.Field("tab-size").FieldState().ID; got != "tab-size" {
		t.Fatalf("form was not rebuilt for Editor, first field=%q", got)
	}

	surfaceRoot.FocusForm()
	cw.Host().Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	cw.Host().Handle(tea.KeyPressMsg{Code: '8', Text: "8"})
	if got := m.AppSettings().Editor.TabSize; got != 8 {
		t.Fatalf("native text edit saved tab size %d, want 8", got)
	}

	surfaceRoot.FocusActions()
	result := cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("native action bar save did not emit a command")
	}
	msg, ok := result.Cmd().(SaveMsg)
	if !ok || msg.AppSettings.Editor.TabSize != 8 {
		t.Fatalf("save message=%#v, want tab size 8", msg)
	}
}

func TestSettingsSurfaceNativeSelectCommitsProvider(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{ActiveProvider: "openrouter", Providers: map[string]types.ProviderConfig{
		"openrouter": {ProviderURL: "https://router"},
		"openai":     {ProviderURL: "https://openai"},
	}}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}})
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 90, 28)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surf := surface.NewSurface("settings-native-select", 90, 28, cell.TransparentBlank())
	cw.Render(surf, &widget.RenderContext{Theme: th})

	surfaceRoot.FocusForm()
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.AppSettings().ActiveProvider; got != "openai" {
		t.Fatalf("native select saved provider %q, want openai", got)
	}

	// Provider selection updates retained text controls before the user edits them.
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	cw.Host().Handle(tea.KeyPressMsg{Code: '2', Text: "2"})
	if got := m.AppSettings().Providers["openai"].ProviderURL; got != "https://openai2" {
		t.Fatalf("programmatic provider synchronization saved base URL %q", got)
	}
}

func bufferContains(buf *surface.CellBuffer, want string) bool {
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
