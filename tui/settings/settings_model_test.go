package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	"crude/tui/wm"
	modeltypes "crude/types"
)

func TestAppSettingsPreserveProvidersAndAgentDefaults(t *testing.T) {
	th := theme.DefaultDark()
	current := types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": provider.DefaultTemplates()["openrouter"],
			"openai":     provider.DefaultTemplates()["openai"],
		},
		Accounts: map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal"}, "work": {ID: "work", ProviderID: "openai", Label: "Work"}},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {AccountID: "router", Model: "main-model", ThinkingEffort: "medium"},
			"crafter": {AccountID: "work", Model: "craft-model", ThinkingEffort: "high"},
		}},
	}
	m := New(th, current, []types.ProviderPreset{
		{ID: "openrouter", Name: "OpenRouter"},
		{ID: "openai", Name: "OpenAI"},
	})

	got := m.AppSettings()
	if got.Providers["openrouter"].ProviderURL != current.Providers["openrouter"].ProviderURL || got.Providers["openai"].APIStandard != current.Providers["openai"].APIStandard {
		t.Fatalf("provider templates were lost: %#v", got.Providers)
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
	cw.Render(surf, &wm.RenderContext{Theme: th})

	for _, text := range []string{"Search settings", "Application", "AI", "IDE", "Save", "Cancel"} {
		if !bufferContains(surf.Buffer(), text) {
			t.Fatalf("settings surface is missing %q", text)
		}
	}

	// Roles live in the Agents catalog instead of four separate rail pages.
	cw.Host().Handle(tea.MouseClickMsg{X: 2, Y: 6, Button: tea.MouseLeft})
	if m.SelectedCategory() != catAgents || m.Pane() != 0 {
		t.Fatalf("category click selected=%d pane=%d, want %d/0", m.SelectedCategory(), m.Pane(), catAgents)
	}
	cw.Render(surf, &wm.RenderContext{Theme: th})
	cw.Focus(surfaceRoot.agents.table.ID())
	result := cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !result.Handled || surfaceRoot.agents.draft == nil || surfaceRoot.agents.draft.Field(fieldConnection) == nil {
		t.Fatal("agent row should open its native model form")
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
	cw.Render(surf, &wm.RenderContext{Theme: th})
	if !bufferContains(surf.Buffer(), "Font") || bufferContains(surf.Buffer(), "Providers") {
		t.Fatal("filtered settings categories were not rendered")
	}
}

func TestSettingsSurfaceRailUsesConsistentInset(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	surfaceRoot.Layout(ui.LayoutContext{Theme: th}, ui.Rect{W: 90, H: 24})

	if got := surfaceRoot.search.Bounds().X; got != workspaceSettingsInset {
		t.Fatalf("search X=%d want shared inset %d", got, workspaceSettingsInset)
	}
	if got := surfaceRoot.categories.Bounds().X; got != workspaceSettingsInset {
		t.Fatalf("category rail X=%d want shared inset %d", got, workspaceSettingsInset)
	}
	if got := surfaceRoot.contentRect.X; got <= surfaceRoot.categories.Bounds().X {
		t.Fatalf("settings content X=%d must follow category rail X=%d", got, surfaceRoot.categories.Bounds().X)
	}
}

func TestSettingsSurfaceSplitDividerDragResizesRail(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 90, 28)
	surfaceRoot.SetFocusHandler(cw.Focus)
	host := cw.Host()
	host.Resize(90, 28)
	host.Layout()

	startW := surfaceRoot.search.Bounds().W
	div := surfaceRoot.divider.Bounds()
	if div.Empty() {
		t.Fatal("settings divider bounds empty")
	}
	clickX := div.X + div.W/2
	res := host.Handle(tea.MouseClickMsg{X: clickX, Y: 8, Button: tea.MouseLeft})
	if !res.Handled || !host.HasPointerCapture() {
		t.Fatalf("click divider: handled=%v capture=%v hit=%q", res.Handled, host.HasPointerCapture(), host.HitTest(clickX, 8))
	}
	host.Handle(tea.MouseMotionMsg{X: clickX + 6, Y: 8, Button: tea.MouseLeft})
	host.Layout()
	if surfaceRoot.search.Bounds().W != startW+6 {
		t.Fatalf("after +6 drag rail W=%d want %d", surfaceRoot.search.Bounds().W, startW+6)
	}
}

func TestSettingsSurfaceActionBarEmitsMessages(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	surfaceRoot := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surfaceRoot, 80, 24)
	surfaceRoot.SetFocusHandler(cw.Focus)
	surf := surface.NewSurface("settings-action-bar", 80, 24, cell.TransparentBlank())
	cw.Render(surf, &wm.RenderContext{Theme: th})
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
	cw.Render(surf, &wm.RenderContext{Theme: th})

	// Rail: scope/search, AI roles, Security/Safety, then IDE/Appearance/Editor.
	for row, item := range m.CategoryItems() {
		if item.Payload == catEditor {
			cw.Host().Handle(tea.MouseClickMsg{X: 2, Y: surfaceRoot.categories.Bounds().Y + row, Button: tea.MouseLeft})
			break
		}
	}
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

func TestSettingsSurfaceTemplateBrowsingDoesNotSwitchMainAgentAccount(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{ActiveProvider: "openrouter", Providers: map[string]types.ProviderConfig{"openrouter": provider.DefaultTemplates()["openrouter"], "openai": provider.DefaultTemplates()["openai"]}, Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "openrouter", Label: "Personal"}, "work": {ID: "work", ProviderID: "openai", Label: "Work"}}, Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "personal", Model: "m"}}}}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}})
	surf := NewSurface(th, m)
	host := ui.NewHost(th, surf)
	host.Resize(90, 28)
	host.Layout()
	m.providerID = "openai"
	surf.templates.syncRows()
	if m.providerID != "openai" {
		t.Fatalf("browsing provider = %q", m.providerID)
	}
	if saved := m.AppSettings(); saved.Agents.Categories["default"].AccountID != "personal" {
		t.Fatalf("browsing changed the Main Agent account: %+v", saved.Agents.Categories["default"])
	}
}

func TestSafetyAutoPermissionDefaultsAndRoundTrip(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{Accounts: map[string]provider.Account{"review": {ID: "review", ProviderID: "openai", Label: "Review"}}}, []types.ProviderPreset{{ID: "openai", Name: "OpenAI"}})
	if got := m.ValueForTest(catSafety, fieldAutoPermission); got != "off" {
		t.Fatalf("default enabled = %q, want off", got)
	}
	if got := m.ValueForTest(catSafety, fieldAutoPermAccount); got != "" {
		t.Fatalf("default provider = %q, want unconfigured", got)
	}
	if got := m.ValueForTest(catSafety, fieldAutoPermModel); got != "" {
		t.Fatalf("default model = %q, want unconfigured", got)
	}

	m.SetValueForTest(catSafety, fieldAutoPermission, "on")
	m.SetValueForTest(catSafety, fieldAutoPermAccount, "review")
	m.SetValueForTest(catSafety, fieldAutoPermModel, "gpt-review")
	m.SetValueForTest(catSafety, fieldAutoPermDenyMode, "deny")
	m.SetValueForTest(catSafety, fieldAutoPermTimeout, "13")
	m.SetValueForTest(catSafety, fieldAutoPermOutputTokens, "72")
	m.SetValueForTest(catSafety, fieldAutoPermInputBytes, "3072")
	m.SetValueForTest(catSafety, fieldAutoPermReviews, "9")
	m.SetValueForTest(catSafety, fieldAutoPermInstructions, " No deployments. ")
	auto := m.AppSettings().Safety.AutoPermission
	if auto.Enabled != "on" || auto.Provider != "openai" || auto.Model != "gpt-review" || auto.DenyMode != "deny" ||
		auto.TimeoutSeconds != 13 || auto.MaxOutputTokens != 72 || auto.MaxInputBytes != 3072 ||
		auto.MaxReviewsPerTurn != 9 || auto.Instructions != "No deployments." {
		t.Fatalf("auto permission settings = %+v", auto)
	}
}

func TestSafetyAutoPermissionProviderAndModelPickerAreExplicit(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": provider.DefaultTemplates()["openrouter"],
			"openai":     {Name: "OpenAI", ProviderURL: "https://api.example/v1", APIStandard: "openai-responses", AuthType: provider.AuthProtocol},
		},
		Accounts: map[string]provider.Account{"review": {ID: "review", ProviderID: "openai", Label: "Review", APIKey: "secret"}},
		Safety:   types.SafetySettings{AutoPermission: modeltypes.AutoPermissionSettings{Provider: "openrouter", Model: "reviewer-old"}},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}})

	m.SetValueForTest(catSafety, fieldAutoPermAccount, "review")
	if got := m.ValueForTest(catSafety, fieldAutoPermModel); got != "" {
		t.Fatalf("provider switch selected model %q; reviewer must stay explicitly unconfigured", got)
	}
	m.SelectCategory(catSafety)
	cmd := m.OpenSelect(fieldAutoPermModel)
	if cmd == nil {
		t.Fatal("opening auto-permission model did not use the standard fetch workflow")
	}
	request, ok := cmd().(FetchModelsMsg)
	if !ok || request.ProviderID != "openai" || request.BaseURL != "https://api.example/v1" || request.APIKey != "secret" {
		t.Fatalf("model fetch request = %#v", request)
	}
	field := m.currentForm().Field(fieldAutoPermModel)
	if !field.Loading {
		t.Fatal("auto-permission model picker did not enter loading state")
	}
	updated, _ := m.Update(ModelsMsg{ProviderID: "openai", AccountID: "review", Generation: request.Generation, Models: []types.ModelEntry{{ID: "gpt-5-mini", Name: "GPT-5 mini"}}})
	m = &updated
	field = m.currentForm().Field(fieldAutoPermModel)
	if field.Loading || len(field.Options) != 1 || field.Options[0].ID != "gpt-5-mini" {
		t.Fatalf("auto-permission model picker after fetch = %+v", field)
	}
	m.SetValueForTest(catSafety, fieldAutoPermModel, "gpt-5-mini")
	if got := m.AppSettings().Safety.AutoPermission.Model; got != "gpt-5-mini" {
		t.Fatalf("picked reviewer model = %q", got)
	}
}

func TestSettingsModelSelectFetchStaysOpenWhenModelsArrive(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{
		ActiveProvider: "openai",
		Providers: map[string]types.ProviderConfig{
			"openai": {Name: "OpenAI", ProviderURL: "https://api.example/v1", APIStandard: "openai-responses", AuthType: provider.AuthProtocol},
		},
		Accounts: map[string]provider.Account{"review": {ID: "review", ProviderID: "openai", Label: "Review", APIKey: "secret"}},
		Agents:   types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Provider: "openai", AccountID: "review"}}},
	}, []types.ProviderPreset{{ID: "openai", Name: "OpenAI"}})
	surfaceRoot := NewSurface(th, m)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 90, 28)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 28)
	host.Layout()
	surfaceRoot.FocusForm()
	m.SelectCategory(catMain)
	surfaceRoot.FocusForm()

	result := m.OpenSelect(fieldModel)
	if result == nil {
		t.Fatal("opening an empty model select did not request models")
	}
	request, ok := result().(FetchModelsMsg)
	if !ok {
		t.Fatalf("model request message=%T", result())
	}
	if request.ProviderID != "openai" || request.BaseURL != "https://api.example/v1" || request.APIKey != "secret" {
		t.Fatalf("model request=%#v", request)
	}
	if !m.currentForm().Field(fieldModel).Loading {
		t.Fatal("model select closed before asynchronous result")
	}

	updated, _ := m.Update(ModelsMsg{ProviderID: "openai", AccountID: "review", Generation: request.Generation, Models: []types.ModelEntry{{ID: "gpt-5", Name: "GPT-5"}}})
	m = &updated
	surfaceRoot.SetModel(m)
	if m.currentForm().Field(fieldModel).Loading {
		t.Fatal("model field still loading after async models arrived")
	}
	if options := m.currentForm().Field(fieldModel).Options; len(options) != 1 || options[0].Label != "GPT-5" {
		t.Fatalf("model options=%#v", options)
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
