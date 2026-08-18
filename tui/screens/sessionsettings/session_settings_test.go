package sessionsettings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestScreenUsesNativeNestedRoleForm(t *testing.T) {
	base := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "workspace-main", ThinkingEffort: "low"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "default", types.CategorySettings{Provider: "openai", Model: "session-main", ThinkingEffort: "high"})
	screen := New(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "session-main", ThinkingEffort: "high"},
	})
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}}, map[string]types.ProviderConfig{
		"openrouter": {Model: "workspace-main"}, "openai": {Model: "session-main"},
	})
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 12})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 13)
	host.Layout()
	host.Focus(FormID)

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action != nil || screen.page != rolePage || screen.role != "default" {
		t.Fatalf("role open result=%#v page=%d role=%q", result, screen.page, screen.role)
	}
	if screen.formModel.FieldCount() != 4 {
		t.Fatalf("role form fields=%d, want provider/model/protocol/thinking", screen.formModel.FieldCount())
	}

	buf := surface.NewCellBuffer(80, 13)
	host.Paint(buf)
	if !bufferContains(buf, "Main Agent") || !bufferContains(buf, "Protocol") {
		t.Fatal("native role form did not paint expected content")
	}
}

func TestRootIncludesMultiAgentToggle(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{
		Enabled: false,
		Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "workspace-main"},
		},
	}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	if field := screen.formModel.Field(fieldMultiAgent); field == nil || ui.BoolFieldValue(field) {
		t.Fatalf("root form missing inherited multi-agent toggle: %#v", field)
	}
	if field := screen.formModel.Field(fieldMultiAgent); !field.Inherited || field.InheritedFrom != "workspace" {
		t.Fatalf("multi-agent should show workspace inheritance: %#v", field)
	}
	screen.formModel.SetValue(fieldMultiAgent, ui.StringValue("on"))
	got := screen.ToSettings()
	if !got.Agents.Enabled || got.SessionMultiAgent == nil || !*got.SessionMultiAgent {
		t.Fatalf("saving multi-agent on did not persist: %#v", got.Agents)
	}
}

func TestRoleResetRestoresInheritanceAndRemovesOverride(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"crafter": {Provider: "openrouter", Model: "workspace-crafter", ThinkingEffort: "medium"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "crafter", types.CategorySettings{Provider: "openai", Model: "session-crafter", ThinkingEffort: "high"})
	screen := New(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"crafter": {Provider: "openai", Model: "session-crafter", ThinkingEffort: "high"},
	})

	screen.resetRole("crafter")
	settings := screen.ToSettings()
	if _, ok := settings.SessionOverrides["crafter"]; ok {
		t.Fatal("reset role still has an explicit session override")
	}
	if got := settings.Agents.Categories["crafter"].Model; got != "workspace-crafter" {
		t.Fatalf("model after reset=%q, want workspace-crafter", got)
	}
}

func TestRoleProtocolOverrideIsSessionScoped(t *testing.T) {
	base := types.Settings{
		ProviderID: "openai",
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openai", Model: "gpt-5.6"},
		}},
	}
	screen := New(theme.DefaultDark(), base, base, nil)
	screen.ConfigureProviders(
		[]types.ProviderPreset{{ID: "openai", Name: "OpenAI", StandardID: "openai-responses"}},
		map[string]types.ProviderConfig{
			"openai": {APIStandard: "openai-responses", Model: "gpt-5.6"},
		},
	)
	screen.openRole("default")
	screen.formModel.SetValue(fieldStandard, ui.StringValue("openai"))
	screen.applyRoleForm()

	settings := screen.ToSettings()
	if got := settings.SessionOverrides["default"].APIStandard; got != "openai" {
		t.Fatalf("session protocol override = %q", got)
	}
	if got := screen.providers["openai"].APIStandard; got != "openai-responses" {
		t.Fatalf("provider default mutated = %q", got)
	}
}

func TestModelSelectEmitsFetchAndAppliesAsyncResult(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}}, map[string]types.ProviderConfig{
		"openrouter": {ProviderURL: "https://router", APIKey: "secret"},
	})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.model.open"})
	if result.Cmd == nil {
		t.Fatal("model picker did not request models")
	}
	request, ok := result.Cmd().(FetchModelsMsg)
	if !ok || request.ProviderID != "openrouter" || request.APIKey != "secret" {
		t.Fatalf("fetch request=%#v", request)
	}
	if screen.page != modelPage {
		t.Fatalf("page=%d, want model picker", screen.page)
	}
	screen.ApplyModels("openrouter", []types.ModelEntry{{ID: "model-a", Name: "Model A"}})
	if item, ok := screen.modelList.Selected(); !ok || item.ID != "model-a" {
		t.Fatalf("picker after result=%#v ok=%v", item, ok)
	}
}

func TestRootArrowsDoNotCloseOrTrap(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 16})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 18)
	host.Layout()
	host.Focus(FormID)

	screen.form.FocusFirst()
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if !deck.IsOpen() {
		t.Fatal("Up on the first agent row closed the dock")
	}
	if result.Action != nil && (result.Action.ID == "panel.close" || result.Action.ID == "paneldeck.empty") {
		t.Fatalf("Up on the first agent row emitted %s", result.Action.ID)
	}
	if host.Focused() != ActionsID {
		t.Fatalf("Up on the first agent row should cycle to buttons, focused=%q", host.Focused())
	}

	host.Focus(FormID)
	screen.form.FocusLast()
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if host.Focused() != ActionsID {
		t.Fatalf("Down past the last field should reach buttons, focused=%q", host.Focused())
	}
	if !deck.IsOpen() {
		t.Fatal("Down onto buttons closed the dock")
	}
	_ = host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if host.Focused() != FormID {
		t.Fatalf("Up from buttons should return to the form, focused=%q", host.Focused())
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action != nil && result.Action.ID == "panel.close" {
		t.Fatal("Enter on multi-agent closed the dock")
	}
}

func TestNavRowsPaintChevronNotPrompt(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 72, H: 16})
	buf := surface.NewCellBuffer(72, 16)
	screen.form.Paint(buf, 0, 0, ui.PaintContext{Focused: true})
	plain := bufferString(buf)
	if strings.Contains(plain, "❯") {
		t.Fatalf("session settings must not paint the chat prompt glyph:\n%s", plain)
	}
	if strings.Contains(plain, ") )") || strings.Contains(plain, "(inherited") {
		t.Fatalf("inherited detail must not use wrapping parentheses:\n%s", plain)
	}
	if !strings.Contains(plain, theme.DefaultDark().Glyphs.TreeCollapsed) {
		t.Fatalf("nav rows missing TreeCollapsed chevron:\n%s", plain)
	}
	if !strings.Contains(plain, "inherited from workspace") {
		t.Fatalf("nav rows missing inherited detail:\n%s", plain)
	}
}

func TestRootPaintsAlignedModelThenProvider(t *testing.T) {
	settings := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default":    {Provider: "opencode-go", Model: "deepseek-v4-flash"},
		"researcher": {Provider: "opencode-go", Model: "deepseek-v4-flash"},
		"crafter":    {Provider: "crof", Model: "glm-5.1-precision"},
		"aeron":      {Provider: "opencode-go", Model: "deepseek-v4-flash"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 80, H: 14})
	buf := surface.NewCellBuffer(80, 14)
	screen.form.Paint(buf, 0, 0, ui.PaintContext{})
	plain := bufferString(buf)
	if strings.Contains(plain, "Agents ─") || strings.Contains(plain, "Agents─") {
		t.Fatalf("root must not paint an Agents rule title:\n%s", plain)
	}
	if !strings.Contains(plain, "Session Settings") || strings.Contains(plain, "Session AI Settings") {
		t.Fatalf("root title must be Session Settings:\n%s", plain)
	}
	main := rowContaining(buf, "Main Agent")
	craft := rowContaining(buf, "Crafter")
	multi := rowContaining(buf, "Multi Agent")
	if main == "" || craft == "" || multi == "" {
		t.Fatalf("missing rows:\n%s", plain)
	}
	if strings.Contains(plain, "Multi-agent") {
		t.Fatalf("label must be Multi Agent, not hyphenated:\n%s", plain)
	}
	if strings.Index(plain, "Crafter") > strings.Index(plain, "Multi Agent") {
		t.Fatalf("multi agent must follow the agent list:\n%s", plain)
	}
	if colOf(main, "Main Agent") != colOf(multi, "Multi Agent") {
		t.Fatalf("multi agent label col=%d, main agent col=%d\n%s", colOf(multi, "Multi Agent"), colOf(main, "Main Agent"), plain)
	}
	if colOf(main, "deepseek-v4-flash") != colOf(craft, "glm-5.1-precision") {
		t.Fatalf("model columns misaligned: main=%d crafter=%d\n%s\n%s", colOf(main, "deepseek-v4-flash"), colOf(craft, "glm-5.1-precision"), main, craft)
	}
	if colOf(main, "deepseek-v4-flash") > colOf(main, "opencode-go") {
		t.Fatalf("model should precede provider: %q", main)
	}
	if colOf(main, "·") != colOf(craft, "·") {
		t.Fatalf("first table dots misaligned: main=%d crafter=%d\n%s\n%s", colOf(main, "·"), colOf(craft, "·"), main, craft)
	}
	if lastColOf(main, "·") != lastColOf(multi, "·") {
		t.Fatalf("inherited dots misaligned: main=%d multi=%d\n%s\n%s", lastColOf(main, "·"), lastColOf(multi, "·"), main, multi)
	}
}

func TestRolePageUsesBreadcrumbAndAlignedValues(t *testing.T) {
	settings := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "opencode-go", Model: "deepseek-v4-flash"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "opencode-go", Name: "OpenCode Go"}}, map[string]types.ProviderConfig{
		"opencode-go": {Model: "deepseek-v4-flash"},
	})
	screen.openRole("default")
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 72, H: 12})
	buf := surface.NewCellBuffer(72, 12)
	screen.form.Paint(buf, 0, 0, ui.PaintContext{})
	plain := bufferString(buf)
	if !strings.Contains(plain, "Session Settings · Main Agent") {
		t.Fatalf("role title must be a breadcrumb:\n%s", plain)
	}
	provider := rowContaining(buf, "Provider")
	model := rowContaining(buf, "Model")
	if provider == "" || model == "" {
		t.Fatalf("missing role rows:\n%s", plain)
	}
	if colOf(provider, "OpenCode Go") != colOf(model, "deepseek-v4-flash") {
		t.Fatalf("role values misaligned:\n%s\n%s", provider, model)
	}
}

func TestProviderPickerHasOwnPage(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureProviders([]types.ProviderPreset{
		{ID: "openrouter", Name: "OpenRouter"},
		{ID: "openai", Name: "OpenAI"},
	}, map[string]types.ProviderConfig{"openrouter": {Model: "a"}, "openai": {Model: "b"}})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.provider.open"})
	if screen.page != providerPage {
		t.Fatalf("page=%d, want provider picker", screen.page)
	}
	if result.Cmd != nil {
		t.Fatal("provider picker should not fetch models")
	}
	if item, ok := screen.modelList.Selected(); !ok || item.ID != "openrouter" {
		t.Fatalf("provider picker selection=%#v ok=%v", item, ok)
	}
}

func rowContaining(buf *surface.CellBuffer, want string) string {
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			if c := buf.Get(x, y); c.Char != 0 {
				b.WriteRune(c.Char)
			} else {
				b.WriteByte(' ')
			}
		}
		row := b.String()
		if strings.Contains(row, want) {
			return row
		}
	}
	return ""
}

func colOf(row, needle string) int {
	i := strings.Index(row, needle)
	if i < 0 {
		return -1
	}
	return len([]rune(row[:i]))
}

func lastColOf(row, needle string) int {
	i := strings.LastIndex(row, needle)
	if i < 0 {
		return -1
	}
	return len([]rune(row[:i]))
}

func bufferString(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if c := buf.Get(x, y); c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
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
