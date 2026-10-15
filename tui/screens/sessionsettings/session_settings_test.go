package sessionsettings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestScreenUsesNativeNestedRoleForm(t *testing.T) {
	base := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main", ThinkingEffort: "low"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "default", types.CategorySettings{Provider: "openai", Model: "session-main", ThinkingEffort: "high"})
	screen := New(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "session-main", ThinkingEffort: "high"},
	})
	screen.ConfigureAccounts(map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", APIKey: "secret", Connection: provider.DefaultTemplates()["openrouter"]}})

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
	if screen.formModel.FieldCount() != 6 {
		t.Fatalf("role form fields=%d, want provider/model/thinking/compaction/retention/capabilities", screen.formModel.FieldCount())
	}

	buf := surface.NewCellBuffer(80, 13)
	host.Paint(buf)
	if !bufferContains(buf, "Main Agent") {
		t.Fatal("native role form did not paint expected content")
	}
	if bufferContains(buf, "Protocol") {
		t.Fatal("role form should not expose a communication-protocol field")
	}
}

func TestRoleFormPersistsExplicitCompactionDisable(t *testing.T) {
	base := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "model"},
	}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, map[string]types.CategorySettings{})
	screen.openRole("default")
	screen.formModel.SetValue(fieldContextCompaction, ui.StringValue("0"))
	screen.applyRoleForm()
	settings := screen.ToSettings()
	category := settings.SessionOverrides["default"]
	if category.ContextCompactionPercent == nil || *category.ContextCompactionPercent != 0 {
		t.Fatalf("session compaction override = %#v", category.ContextCompactionPercent)
	}
}

func TestRootActionOrderPutsSaveAsWorkspaceLast(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)
	labels := screen.actions.ButtonLabelsForTest()
	want := []string{"Save", "Cancel", "Reset", "Save as workspace"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i, w := range want {
		if labels[i] != w {
			t.Errorf("label[%d] = %q, want %q", i, labels[i], w)
		}
	}
}

func TestRootIncludesMultiAgentToggle(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{
		Enabled: false,
		Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
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

func TestSessionDefaultAgentSwitchKeepsOtherRolesAndAccountPin(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default":    {AccountID: "home", Model: "main-model"},
		"researcher": {AccountID: "work", Model: "research-model"},
	}, AgentProfiles: map[string]types.AgentProfileSettings{
		"reviewer": {Category: "researcher", Model: "review-model", AccountID: "work"},
	}}}
	s := New(theme.DefaultDark(), base, base, map[string]types.CategorySettings{"default": {AccountID: "home"}})
	s.ConfigureAccounts(map[string]provider.Account{"home": {ID: "home", Label: "Home"}, "work": {ID: "work", Label: "Work"}})
	s.formModel.SetValue(fieldMainAgent, ui.StringValue("profile/reviewer"))
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "form.changed"})
	if row := s.formModel.Field("default"); row.Columns[0] != "review-model" || row.Columns[1] != "Work" {
		t.Fatalf("root row did not react to selection: %+v", row)
	}
	settings := s.ToSettings()
	if settings.SessionMainAgent != "profile/reviewer" || settings.Model != "review-model" || settings.AccountID != "work" || settings.SessionOverrides["root/profile/reviewer"].AccountID != "work" || settings.SessionOverrides["default"].AccountID != "home" {
		t.Fatalf("selected root rewrote another role or failed to pin account: %+v", settings)
	}
	s.formModel.SetValue(fieldMainAgent, ui.StringValue(""))
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "form.changed"})
	if got := s.ToSettings(); got.Model != "main-model" || got.AccountID != "home" {
		t.Fatalf("returning to Main Agent lost its session settings: %+v", got)
	}
}

func TestDefaultAgentUsesTheExistingNavigationPicker(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{
		Categories: map[string]types.CategorySettings{"default": {Model: "main-model"}, "researcher": {AccountID: "work"}},
		AgentProfiles: map[string]types.AgentProfileSettings{
			"reviewer":          {Category: "researcher", Model: "review-model", Description: "Review changes"},
			"background-memory": {Category: memoryRole},
		},
	}}
	s := New(theme.DefaultDark(), base, base, nil)
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(90, 17)
	host.Layout()
	if row := s.formModel.Field(fieldMainAgent); row.Type != ui.FieldNav || row.Target != "session-settings.agent.open" {
		t.Fatal("Default agent is not the same navigation-row control as Account and Model")
	}
	buf := surface.NewCellBuffer(90, 17)
	host.Paint(buf)
	row := rowContaining(buf, "Default agent")
	if !strings.HasSuffix(strings.TrimSpace(row), s.tokens.Glyphs().TreeCollapsed) || strings.Contains(row, "▾") {
		t.Fatalf("Default agent did not use the shared navigation chevron: %q", row)
	}
	s.form.FocusField(fieldMainAgent)
	host.Focus(FormID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.page != agentPage || !s.pickerPage() {
		t.Fatal("Default agent opened an inline dropdown instead of the existing picker page")
	}
	host.Paint(buf)
	if !bufferContains(buf, "Session Settings · Default agent") || !bufferContains(buf, "Filter agents") {
		t.Fatal("Default agent picker lost the shared breadcrumb/filter presentation")
	}
	for _, item := range s.modelList.Items() {
		if strings.Contains(item.ID, "memory") {
			t.Fatal("background-only Memory Agent appeared in the root picker")
		}
	}
	for _, letter := range "review" {
		host.Handle(tea.KeyPressMsg{Code: letter, Text: string(letter)})
	}
	if items := s.modelList.Items(); len(items) != 1 || items[0].ID != "profile/reviewer" {
		t.Fatalf("agent picker filtering did not use the existing list path: %+v", items)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.page != rootPage || s.ToSettings().SessionMainAgent != "profile/reviewer" || s.ToSettings().Model != "review-model" {
		t.Fatal("picker selection did not update the session agent and return to its parent")
	}
	host.Paint(buf)
	if row := rowContaining(buf, "Default agent"); !strings.Contains(row, "reviewer") {
		t.Fatalf("parent row did not repaint the selected agent: %q", row)
	}
	s.form.FocusField(fieldMainAgent)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.page != agentPage {
		t.Fatalf("reopening the default agent picker stayed on page %d", s.page)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if s.page != rootPage || s.effective.SessionMainAgent != "profile/reviewer" {
		t.Fatalf("picker Back changed the selection or closed the entire settings deck: page=%d agent=%q", s.page, s.effective.SessionMainAgent)
	}
	s.form.FocusField(fieldMainAgent)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if s.page != rootPage {
		t.Fatal("agent-picker root breadcrumb did not navigate back")
	}
}

func TestRoleResetRestoresInheritanceAndRemovesOverride(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
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
	// A sub-agent reset returns to inheriting the session main agent live —
	// no stale copy, no explicit provider/model.
	got := settings.Agents.Categories["crafter"]
	if got.Inherit != "main" || got.Model != "" || got.Provider != "" {
		t.Fatalf("crafter after reset = %+v, want inherit-main reference", got)
	}
}

func TestInheritMainIsLiveReference(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "gpt-5.6", ThinkingEffort: "high"},
		"crafter": {Provider: "openrouter", Model: "workspace-crafter"},
	}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)

	screen.inheritMain("crafter")
	settings := screen.ToSettings()
	got := settings.Agents.Categories["crafter"]
	// A live reference carries the marker and no copied values.
	if got.Inherit != "main" {
		t.Fatalf("crafter inherit marker = %q, want main", got.Inherit)
	}
	if got.Model != "" || got.Provider != "" {
		t.Fatalf("crafter should not copy main's values, got %+v", got)
	}
	over, ok := settings.SessionOverrides["crafter"]
	if !ok || over.Inherit != "main" {
		t.Fatalf("inherit main should record a live main-agent override, got %+v", over)
	}
}

func TestInheritWorkspaceIsLiveReference(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "gpt-5.6"},
	}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)
	screen.inheritWorkspace("default")
	settings := screen.ToSettings()
	if got := settings.Agents.Categories["default"].Inherit; got != "workspace" {
		t.Fatalf("default inherit marker = %q, want workspace", got)
	}
}

func TestInheritMainNoopForDefaultRole(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "gpt-5.6"},
	}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)
	screen.inheritMain("default")
	if _, ok := screen.overrides["default"]; ok {
		t.Fatal("default role should not get an inherit-main override")
	}
}

func TestRoleResetPointsAtWorkspace(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "default", types.CategorySettings{Provider: "openai", Model: "session-main"})
	screen := New(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "session-main"},
	})

	screen.resetRole("default")
	settings := screen.ToSettings()
	if _, ok := settings.SessionOverrides["default"]; ok {
		t.Fatal("reset main should remove the session override")
	}
	// The main agent's reset points back at the live workspace main.
	if got := settings.Agents.Categories["default"].Inherit; got != "workspace" {
		t.Fatalf("default after reset inherit = %q, want workspace", got)
	}
}

func TestModelSelectEmitsFetchAndAppliesAsyncResult(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureAccounts(map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", APIKey: "secret", Connection: provider.DefaultTemplates()["openrouter"]}})

	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.model.open"})
	if result.Cmd == nil {
		t.Fatal("model picker did not request models")
	}
	request, ok := result.Cmd().(FetchModelsMsg)
	if !ok || request.ProviderID != "openrouter" || request.AccountID != "router" || request.Target != screen {
		t.Fatalf("fetch request=%#v", request)
	}
	if screen.page != modelPage {
		t.Fatalf("page=%d, want model picker", screen.page)
	}
	screen.ApplyModelsForAccount("openrouter", "router", screen.fetchGeneration[sessionModelKey("openrouter", "router")], []types.ModelEntry{{ID: "model-a", Name: "Model A"}})
	if item, ok := screen.modelList.Selected(); !ok || item.ID != "model-a" {
		t.Fatalf("picker after result=%#v ok=%v", item, ok)
	}
}

func TestModelPickerEnterCommitsToRolePage(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureAccounts(map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", APIKey: "secret", Connection: provider.DefaultTemplates()["openrouter"]}})

	screen.openRole("default")
	screen.OpenModelPicker("default")
	screen.ApplyModelsForAccount("openrouter", "router", screen.fetchGeneration[sessionModelKey("openrouter", "router")], []types.ModelEntry{{ID: "model-b", Name: "Model B"}})

	// Enter on the selected model row commits the pick.
	res := screen.Handle(ui.EventContext{}, ui.NormalizeEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if res.Action != nil && res.Action.ID == "session-settings.model.select" {
		t.Fatalf("Enter leaked the select action: %#v", res.Action)
	}
	if screen.page != rolePage {
		t.Fatalf("page=%d, want role page after Enter commit", screen.page)
	}
	field := screen.formModel.Field(fieldModel)
	if field == nil || field.Value.String() != "model-b" {
		t.Fatalf("role page model=%#v, want model-b", field)
	}
	if cfg := categoryFor(screen.effective, screen.base, "default"); cfg.Model != "model-b" {
		t.Fatalf("effective model=%q, want model-b", cfg.Model)
	}
}

func TestModelPickerClickCommitsToRolePage(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureAccounts(map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", APIKey: "secret", Connection: provider.DefaultTemplates()["openrouter"]}})

	screen.openRole("default")
	screen.OpenModelPicker("default")
	screen.ApplyModelsForAccount("openrouter", "router", screen.fetchGeneration[sessionModelKey("openrouter", "router")], []types.ModelEntry{{ID: "model-c", Name: "Model C"}})
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 72, H: 16})
	screen.modelList.SelectIndex(0)

	// Click on the selected row commits the pick and returns to the role page.
	lr := screen.modelList.Bounds()
	res := screen.Handle(ui.EventContext{LocalX: lr.X + 2, LocalY: lr.Y}, ui.NormalizeEvent(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}))
	if res.Action != nil && res.Action.ID == "session-settings.model.select" {
		t.Fatalf("click leaked the select action: %#v", res.Action)
	}
	if screen.page != rolePage {
		t.Fatalf("page=%d, want role page after click commit", screen.page)
	}
	if cfg := categoryFor(screen.effective, screen.base, "default"); cfg.Model != "model-c" {
		t.Fatalf("effective model=%q, want model-c", cfg.Model)
	}
}

func TestRootArrowsDoNotCloseOrTrap(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
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
		"default": {Provider: "openrouter", AccountID: "router", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureAccounts(map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", Connection: provider.DefaultTemplates()["openrouter"]}})
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
	if !strings.Contains(plain, "inherited from") {
		t.Fatalf("nav rows missing inherited detail:\n%s", plain)
	}
}

func TestRootPaintsAlignedModelThenProvider(t *testing.T) {
	settings := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default":    {Provider: "opencode-go", AccountID: "work", Model: "deepseek-v4-flash"},
		"researcher": {Provider: "opencode-go", AccountID: "work", Model: "deepseek-v4-flash"},
		"crafter":    {Provider: "crof", AccountID: "craft", Model: "glm-5.1-precision"},
		"aeron":      {Provider: "opencode-go", AccountID: "work", Model: "deepseek-v4-flash"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureAccounts(map[string]provider.Account{"work": {ID: "work", ProviderID: "opencode-go", Label: "Work", Connection: provider.DefaultTemplates()["opencode-go"]}, "craft": {ID: "craft", ProviderID: "crof", Label: "Craft"}})
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
	if colOf(main, "deepseek-v4-flash") > colOf(main, "Work") {
		t.Fatalf("model should precede account: %q", main)
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
		"default": {Provider: "opencode-go", AccountID: "work", Model: "deepseek-v4-flash"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)

	screen.ConfigureAccounts(map[string]provider.Account{"work": {ID: "work", ProviderID: "opencode-go", Label: "Work", Connection: provider.DefaultTemplates()["opencode-go"]}})
	screen.openRole("default")
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 72, H: 12})
	buf := surface.NewCellBuffer(72, 12)
	screen.form.Paint(buf, 0, 0, ui.PaintContext{})
	plain := bufferString(buf)
	if !strings.Contains(plain, "Session Settings · Main Agent") {
		t.Fatalf("role title must be a breadcrumb:\n%s", plain)
	}
	provider := rowContaining(buf, "Account")
	model := rowContaining(buf, "Model")
	if provider == "" || model == "" {
		t.Fatalf("missing role rows:\n%s", plain)
	}
	if colOf(provider, "Work") != colOf(model, "deepseek-v4-flash") {
		t.Fatalf("role values misaligned:\n%s\n%s", provider, model)
	}
}

func TestSessionBreadcrumbSegmentsNavigateThroughHost(t *testing.T) {
	for _, width := range []int{28, 90} {
		base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Model: "model"}}}}
		s := New(theme.DefaultDark(), base, base, nil)
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(width, 16)
		for _, picker := range []page{modelPage, providerPage} {
			s.openRole("default")
			if picker == modelPage {
				s.openModelPicker()
			} else {
				s.openProviderPicker()
			}
			host.MarkLayout()
			host.Layout()
			host.Handle(tea.MouseClickMsg{X: rendering.VisibleWidth("Session Settings"), Y: 0, Button: tea.MouseLeft})
			if s.page != picker {
				t.Fatal("breadcrumb separator navigated as if it were a label")
			}
			parentX := rendering.VisibleWidth("Session Settings"+ui.TitleBreadcrumbSeparator) + 1
			host.Handle(tea.MouseClickMsg{X: parentX, Y: 0, Button: tea.MouseLeft})
			if s.page != rolePage || s.role != "default" {
				t.Fatalf("intermediate breadcrumb did not return to Main Agent at width %d", width)
			}
			s.formModel.SetValue(fieldContextCompaction, ui.StringValue("42"))
			host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
			if s.page != rootPage {
				t.Fatal("form-owned root breadcrumb did not return to session settings")
			}
			if value := s.ToSettings().SessionOverrides["default"].ContextCompactionPercent; value == nil || *value != 42 {
				t.Fatal("breadcrumb navigation dropped the staged role edit")
			}
		}
	}
}

func TestRolePageTextEditorJoinsValueColumn(t *testing.T) {
	settings := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "opencode-go", AccountID: "work", Model: "deepseek-v4-flash"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)

	screen.ConfigureAccounts(map[string]provider.Account{"work": {ID: "work", ProviderID: "opencode-go", Label: "Work", Connection: provider.DefaultTemplates()["opencode-go"]}})
	screen.openRole("default")
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 72, H: 12})
	buf := surface.NewCellBuffer(72, 12)
	screen.form.Paint(buf, 0, 0, ui.PaintContext{})
	plain := bufferString(buf)
	// The default compaction percent renders as the value of the compact
	// single-line editor row.
	compaction := rowContaining(buf, "Compact context at")
	provider := rowContaining(buf, "Account")
	if compaction == "" || provider == "" {
		t.Fatalf("missing role rows:\n%s", plain)
	}
	value := "Auto"
	if !strings.Contains(compaction, value) {
		t.Fatalf("compaction row missing %q:\n%s", value, compaction)
	}
	// The "85" must share the nav rows' value column (Ollama-style nav value
	// cell), never dangle at the label edge.
	if colOf(compaction, value) != colOf(provider, "Work") {
		t.Fatalf("compaction editor at col %d, want shared value column %d\n%s\n%s",
			colOf(compaction, value), colOf(provider, "Work"), provider, compaction)
	}
}

func TestProviderPickerHasOwnPage(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", AccountID: "router"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)

	screen.ConfigureAccounts(map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", Connection: provider.DefaultTemplates()["openrouter"]}, "openai-account": {ID: "openai-account", ProviderID: "openai", Label: "Work", Connection: provider.DefaultTemplates()["openai"]}})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.provider.open"})
	if screen.page != providerPage {
		t.Fatalf("page=%d, want provider picker", screen.page)
	}
	if result.Cmd != nil {
		t.Fatal("provider picker should not fetch models")
	}
	if item, ok := screen.modelList.Selected(); !ok || item.ID != "router" {
		t.Fatalf("provider picker selection=%#v ok=%v", item, ok)
	}
}

func TestInheritedAccountChangeRefreshesModelPicker(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "custom", AccountID: "old", Model: "old-model"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "default", types.CategorySettings{Inherit: InheritWorkspaceMarker})
	screen := New(theme.DefaultDark(), effective, base, nil)
	screen.ConfigureAccounts(map[string]provider.Account{
		"old": {ID: "old", ProviderID: "custom", Label: "Personal"},
		"new": {ID: "new", ProviderID: "custom", Label: "Work"},
	})
	screen.OpenAccountPicker("default")
	for i, item := range screen.modelList.Items() {
		if item.ID == "new" {
			screen.modelList.SelectIndex(i)
		}
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.provider.select"})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.model.open"})
	if result.Cmd == nil {
		t.Fatal("account change did not request models")
	}
	request := result.Cmd().(FetchModelsMsg)
	if request.AccountID != "new" {
		t.Fatalf("model discovery still targets inherited account: %+v", request)
	}
	screen.ApplyModelsForAccount("custom", "new", request.Generation, []types.ModelEntry{{ID: "work-model"}})
	if rows := screen.modelList.Items(); len(rows) != 1 || rows[0].ID != "work-model" {
		t.Fatalf("new account models not displayed: %+v", rows)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.model.select"})
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.role.apply"})
	got := screen.ToSettings()
	if got.AccountID != "new" || got.Model != "work-model" {
		t.Fatalf("new selection not saved: account=%q model=%q", got.AccountID, got.Model)
	}
}

func TestAccountPickerHasNoTemplateRows(t *testing.T) {
	screen := New(theme.DefaultDark(), types.Settings{}, types.Settings{}, nil)

	screen.ConfigureAccounts(map[string]provider.Account{"work": {ID: "work", ProviderID: "custom", Label: "Work"}})
	screen.OpenAccountPicker("default")
	rows := screen.modelList.Items()
	if len(rows) != 1 || rows[0].Header || rows[0].Label != "Work" || screen.accountLabel("work") != "Work" {
		t.Fatalf("session account picker exposes templates: %+v label=%q", rows, screen.accountLabel("work"))
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

// colOfPos finds the x of needle on a buffer row y using rune columns.
func colOfPos(buf *surface.CellBuffer, y int, needle string) int {
	if y < 0 || y >= buf.Height() {
		return -1
	}
	row := bufferString(buf)
	lines := strings.Split(row, "\n")
	if y >= len(lines) {
		return -1
	}
	return colOf(lines[y], needle)
}

func TestMultiAgentControlAlignsUnderModelColumn(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{
		Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "stealth/ox-alpha"},
		},
	}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 12})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 13)
	host.Layout()

	buf := surface.NewCellBuffer(80, 13)
	host.Paint(buf)

	mainY, multiY := -1, -1
	for y := 0; y < buf.Height(); y++ {
		if colOfPos(buf, y, "Main Agent") >= 0 && mainY < 0 {
			mainY = y
		}
		if colOfPos(buf, y, "Multi Agent") >= 0 && multiY < 0 {
			multiY = y
		}
	}
	if mainY < 0 || multiY < 0 {
		t.Fatalf("missing rows: mainY=%d multiY=%d", mainY, multiY)
	}
	modelCol := colOfPos(buf, mainY, "stealth/ox-alpha")
	ctrlCol := colOfPos(buf, multiY, "‹")
	if modelCol < 0 || ctrlCol < 0 {
		t.Fatalf("model col=%d ctrl col=%d", modelCol, ctrlCol)
	}
	if modelCol != ctrlCol {
		t.Fatalf("Multi Agent control col=%d, want model col=%d", ctrlCol, modelCol)
	}
	// The inherit text must stay in the last column (right of the control).
	inh := colOfPos(buf, multiY, "inherited from workspace")
	if inh >= 0 && inh <= modelCol {
		t.Fatalf("inherit text must be right of the model column: inh=%d model=%d", inh, modelCol)
	}
}
