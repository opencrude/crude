package settings

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestMainAgentKeepsModelDropdownAndMultiAgentToggle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		m := New(theme.DefaultDark(), types.AppSettings{Agents: types.AgentsSettings{Enabled: !enabled}}, nil)
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		host := ui.NewHost(theme.DefaultDark(), s)
		s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
		host.Resize(130, 36)
		host.Layout()
		c := s.agents
		c.begin("role/default", false)
		host.MarkLayout()
		host.Layout()
		if field := c.form.Field(fieldModel); field == nil || field.FieldState().Type != ui.FieldSelect || field.FieldState().Label != "Model" {
			t.Fatal("Main Agent lost its existing Model dropdown")
		}
		if c.draft.Field("agent-model-browse") != nil {
			t.Fatal("unrequested model-ID/browse redesign returned")
		}
		if !c.form.FocusField(fieldMultiAgent) {
			t.Fatal("Multi Agent toggle is missing from the visible Model page")
		}
		host.Focus(AgentSettingsFormID)
		host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
		buf := surface.NewCellBuffer(130, 36)
		host.Paint(buf)
		if !strings.Contains(bufferPlain(buf), "Multi Agent") {
			t.Fatal("Multi Agent toggle cannot be reached and painted")
		}
		if !c.apply() || m.AppSettings().Agents.Enabled != enabled {
			t.Fatalf("Multi Agent toggle did not save %v", enabled)
		}
		c.begin("role/default", false)
		if ui.BoolFieldValue(c.draft.Field(fieldMultiAgent)) != enabled {
			t.Fatalf("reopened Multi Agent toggle lost %v", enabled)
		}
	}
}

func TestAgentCatalogCreatesStructuredProfilesWithoutMutatingDefaults(t *testing.T) {
	defaults := types.AppSettings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Instructions: "Original instructions"}}, AgentProfiles: map[string]types.AgentProfileSettings{"existing": {Category: "researcher", Instructions: "Keep this"}}}}
	m := New(theme.DefaultDark(), defaults, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("", false)
	c.draft.SetValue("agent-name", ui.StringValue("review-specialist"))
	c.draft.SetValue("agent-role", ui.StringValue("researcher"))
	c.draft.SetValue("agent-description", ui.StringValue("Review correctness and tests"))
	c.draft.SetValue("agent-instructions", ui.StringValue("Inspect failure paths.\nReport actionable findings."))
	c.draft.SetValue("agent-tool-mode", ui.StringValue("Custom selection"))
	if field := c.draft.Field("agent-tool/write_file"); !field.Disabled || field.Value.Bool() {
		t.Fatal("researcher editor offered a forbidden write tool")
	}
	c.draft.SetValue("agent-tool/web_fetch", ui.BoolValue(false))
	if !c.apply() {
		t.Fatal("valid structured agent draft did not apply")
	}
	got := m.AppSettings().Agents.AgentProfiles["review-specialist"]
	if got.Category != "researcher" || !strings.Contains(got.Instructions, "failure paths") || !slices.Contains(got.DeniedTools, "web_fetch") {
		t.Fatalf("agent controls were not persisted: %+v", got)
	}
	if _, ok := defaults.Agents.AgentProfiles["review-specialist"]; ok {
		t.Fatal("editing preferences mutated the saved defaults")
	}
	c.begin("role/default", false)
	c.draft.SetValue("agent-instructions", ui.StringValue("Updated system prompt"))
	c.draft.SetValue(fieldMultiAgent, ui.StringValue("on"))
	c.apply()
	if !m.AppSettings().Agents.Enabled {
		t.Fatal("Main Agent editor dropped the multi-agent control")
	}
	if defaults.Agents.Categories["default"].Instructions != "Original instructions" {
		t.Fatal("role editor mutated defaults before Save")
	}
	if m.AppSettings().Agents.Categories["default"].Instructions != "Updated system prompt" {
		t.Fatal("built-in role prompt edit was lost")
	}
}

func TestAgentCatalogDraftValidationAndDiscard(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("", false)
	c.draft.SetValue("agent-name", ui.StringValue("bad name"))
	if c.apply() {
		t.Fatal("invalid name accepted")
	}
	s.cancelSettings()
	if c.draft == nil || c.confirm != "discard" {
		t.Fatal("closing discarded an unapplied agent without confirmation")
	}
	c.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.discard"})
	if c.draft != nil || len(m.AppSettings().Agents.AgentProfiles) != 0 {
		t.Fatal("discard committed the draft")
	}
}

func TestAgentCatalogPaintsNativeTabsAndToolControls(t *testing.T) {
	for _, width := range []int{80, 130} {
		m := New(theme.DefaultDark(), types.AppSettings{}, nil)
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		if painted := bufferPlain(paintAccountSurface(t, s, width, 32)); !strings.Contains(painted, "Main Agent") {
			t.Fatal("agent catalog is not painted")
		}
		s.agents.begin("role/crafter", false)
		s.agents.setPage("tools")
		painted := bufferPlain(paintAccountSurface(t, s, width, 32))
		if !strings.Contains(painted, "Instructions") || !strings.Contains(painted, "Tool Access") || !strings.Contains(painted, "Tool access") {
			t.Fatalf("native editor missing controls at width %d:\n%s", width, painted)
		}
	}
}

func TestInactiveAgentCatalogDoesNotPaintItsActions(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	firstRow := strings.SplitN(bufferPlain(paintAccountSurface(t, s, 120, 32)), "\n", 2)[0]
	if strings.Contains(firstRow, "Add") || strings.Contains(firstRow, "Clone") {
		t.Fatal("hidden agent catalog painted its action bar over another settings page")
	}
}

func TestAgentCatalogResetsBuiltinsAndPreservesCompleteNamedEditors(t *testing.T) {
	defaults := types.AppSettings{Agents: types.AgentsSettings{Categories: types.DefaultAgentRoleCategories(), AgentProfiles: types.DefaultNamedAgents()}}
	m := New(theme.DefaultDark(), defaults, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("role/researcher", false)
	c.draft.SetValue("agent-instructions", ui.StringValue("Customized"))
	c.draft.SetValue("agent-enabled", ui.StringValue("off"))
	if !c.apply() {
		t.Fatal("could not edit built-in agent")
	}
	if role := m.AppSettings().Agents.Categories["researcher"]; role.Enabled == nil || *role.Enabled || role.Instructions != "Customized" {
		t.Fatalf("role edits lost: %+v", role)
	}
	for i, row := range c.table.Rows() {
		if row.ID == "role/researcher" {
			c.table.SelectIndex(i)
		}
	}
	c.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.reset"})
	c.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.reset-confirm"})
	if got := m.AppSettings().Agents.Categories["researcher"]; got.Instructions != "" || got.Enabled != nil || got.Policy != "read-only" {
		t.Fatalf("built-in reset did not restore shipped role: %+v", got)
	}
	c.begin("profile/reviewer", true)
	c.draft.SetValue("agent-name", ui.StringValue("review-copy"))
	c.draft.SetValue(fieldConnection, ui.StringValue(""))
	c.draft.SetValue(fieldThinkingEffort, ui.StringValue("high"))
	c.draft.SetValue(fieldCtxWindow, ui.StringValue("64000"))
	c.draft.SetValue(fieldMaxOutput, ui.StringValue("4096"))
	c.draft.SetValue(fieldTemperature, ui.StringValue("0.3"))
	c.draft.SetValue("agent-file-ownership", ui.StringValue("pkg/a\npkg/b"))
	if !c.apply() {
		t.Fatal("cloned agent did not apply")
	}
	clone := m.AppSettings().Agents.AgentProfiles["review-copy"]
	if clone.ThinkingEffort != "high" || clone.ModelContextWindow != 64000 || clone.ModelMaxOutput != 4096 || clone.ModelTemperature == nil || *clone.ModelTemperature != 0.3 || !slices.Equal(clone.FileOwnership, []string{"pkg/a", "pkg/b"}) {
		t.Fatalf("named editor dropped supported fields: %+v", clone)
	}
	if _, ok := defaults.Agents.AgentProfiles["review-copy"]; ok {
		t.Fatal("clone mutated saved defaults")
	}
}

func TestMemoryAgentLivesInAgentCatalog(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("role/memory", false)
	c.draft.SetValue("agent-instructions", ui.StringValue("Summarize conservatively"))
	c.draft.SetValue("agent-enabled", ui.StringValue("off"))
	if !c.apply() {
		t.Fatal("memory agent was not editable")
	}
	got := m.AppSettings().Memory.MemoryCategory
	if got.Instructions != "Summarize conservatively" || got.Enabled == nil || *got.Enabled {
		t.Fatalf("memory edits were not persisted with Memory settings: %+v", got)
	}
	if out := bufferPlain(paintAccountSurface(t, s, 130, 26)); !strings.Contains(out, "Memory Agent") || !strings.Contains(out, "disabled") {
		t.Fatalf("memory agent state missing from catalog: %s", out)
	}
}

func TestAgentDiscoveryPreservesSavedAndPendingCapabilities(t *testing.T) {
	for _, pending := range []bool{false, true} {
		cfg := types.CategorySettings{AccountID: "work", Model: "vision", ModelContextWindow: 64000, ModelModalities: []string{provider.ModalityImage}}
		if pending {
			cfg.ModelContextWindow, cfg.ModelModalities = 0, nil
		}
		m := New(theme.DefaultDark(), types.AppSettings{
			Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Connection: provider.DefaultTemplates()["openai"]}},
			Agents:   types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": cfg}},
		}, nil)
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		c := s.agents
		c.begin("role/default", false)
		if pending {
			c.draft.SetValue(fieldCtxWindow, ui.StringValue("64000"))
			c.draft.SetValue(fieldModalityPrefix+provider.ModalityImage, ui.StringValue("yes"))
		}
		m.setModelsForAccount("openai", "work", []types.ModelEntry{{ID: "vision", ContextWindow: 128000, Modalities: []string{provider.ModalityImage, provider.ModalityPDF}}})
		if c.draft.Field(fieldCtxWindow).Value.String() != "64000" || c.draft.Field(fieldModalityPrefix+provider.ModalityPDF).Value.String() != "no" {
			t.Fatalf("discovery overwrote %v pending capability edits", pending)
		}
		pdf := c.draft.Field(fieldModalityPrefix + provider.ModalityPDF)
		if !pdf.DescriptionImportant || !strings.Contains(pdf.Description, "Inconsistent") || len(pdf.Errors) > 0 {
			t.Fatalf("explicit override disagreement did not produce a non-blocking description: %+v", pdf)
		}
		c.draft.SetValue(fieldModalityPrefix+provider.ModalityImage, ui.StringValue("no"))
		if !c.apply() {
			t.Fatal("capability warning prevented the user from applying an override")
		}
		if modalities := m.AppSettings().Agents.Categories["default"].ModelModalities; modalities == nil || len(modalities) != 0 {
			t.Fatalf("explicit all-No modalities became inherited defaults: %#v", modalities)
		}
	}
}

// The model catalog lives in the core. A catalog update therefore refetches
// every loaded account list through the core instead of re-deriving cards in
// this catalog-less shell; arriving cards populate inherited modalities
// without dirtying unedited settings or becoming explicit overrides.
func TestAgentModalitiesTrackCatalogWithoutPersistingAutomaticOverrides(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "test", Connection: provider.Template{ProviderURL: "https://example.test/v1", APIStandard: "openai"}}},
		Agents:   types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "work", Model: "vision"}}},
	}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	open := c.begin("role/default", false)
	image := c.draft.Field(fieldModalityPrefix + provider.ModalityImage)
	if !strings.Contains(image.Description, "unknown") {
		t.Fatal("missing metadata was presented as a verified No")
	}
	first, ok := open().(FetchModelsMsg)
	if !ok {
		t.Fatal("editor did not prefetch its account's models")
	}
	m.Update(ModelsMsg{ProviderID: first.ProviderID, AccountID: first.AccountID, Generation: first.Generation, Models: []types.ModelEntry{{ID: "vision"}}})
	before := m.preferenceBytes()
	_, refetch := m.Update(types.ModelCatalogUpdatedMsg{Generation: 1})
	if refetch == nil {
		t.Fatal("catalog update did not refetch the loaded account through the core")
	}
	request, ok := refetch().(FetchModelsMsg)
	if !ok || request.AccountID != "work" {
		t.Fatalf("catalog refetch = %#v, want the work account", request)
	}
	m.Update(ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Models: []types.ModelEntry{{ID: "vision", ContextWindow: 128000, MaxOutput: 8192, ModalitySupport: map[string]bool{provider.ModalityImage: true}, Modalities: []string{provider.ModalityImage}}}})
	c.syncModel()
	if c.draft.AnyDirty() || !slices.Equal(m.preferenceBytes(), before) {
		t.Fatal("background metadata made unedited settings dirty")
	}
	if image.Value.String() != "yes" || !image.Inherited {
		t.Fatal("background card arrival did not populate inherited modality")
	}
	if !c.apply() || m.AppSettings().Agents.Categories["default"].ModelModalities != nil {
		t.Fatal("automatic discovery became an explicit user override")
	}
}

func TestBuiltinInstructionsEditThroughHostAndScrollbarMatchesPopup(t *testing.T) {
	for _, role := range agentRoles {
		m := New(theme.DefaultDark(), types.AppSettings{}, nil)
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		host := ui.NewHost(theme.DefaultDark(), s)
		s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
		host.Resize(130, 32)
		host.Layout()
		c := s.agents
		c.begin("role/"+role.key, false)
		host.MarkLayout()
		buf := surface.NewCellBuffer(130, 32)
		host.Paint(buf)
		track := c.formScroll.Bounds()
		if track.Empty() && role.key != "auto-review" {
			t.Fatal("fixture did not expose the editor scrollbar")
		}
		if !track.Empty() {
			if got := buf.Get(c.Bounds().X+track.X, c.Bounds().Y+track.Y).BG; !got.Equal(s.tokens.Color(ui.ColorPopupBg)) {
				t.Fatal("agent scrollbar gutter does not match the popup background")
			}
		}
		host.Focus(c.tabs.ID())
		host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
		if c.page != "instructions" {
			t.Fatal("the native Instructions tab did not switch the editor body")
		}
		if !c.form.FocusField("agent-instructions") {
			t.Fatalf("%s instructions are not editable", role.key)
		}
		host.Focus(AgentSettingsFormID)
		host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
		host.Handle(tea.PasteMsg{Content: "Edited built-in instructions\nSecond line"})
		host.Paint(buf)
		if !strings.Contains(bufferPlain(buf), "Edited built-in instructions") || !strings.Contains(bufferPlain(buf), "Purpose") || !strings.Contains(bufferPlain(buf), "Apply agent") {
			t.Fatalf("%s instructions or their Apply action were not painted", role.key)
		}
		// The editor's decisions replace Save/Close on the footer row.
		bar, ok := host.ScreenRect(c.bar.ID())
		closeBar := s.closeBar.Bounds()
		if !ok || bar.Y != s.Bounds().H-1 || closeBar.Y != bar.Y || bar.X+bar.W+s.tokens.ButtonGap() != closeBar.X || closeBar.X+closeBar.W != s.contentRect.X+s.contentRect.W {
			t.Fatalf("agent decisions are not on the footer row directly before Close: %+v close=%+v", bar, closeBar)
		}
		if !s.actions.Bounds().Empty() {
			t.Fatal("window Save/Close stayed mounted beside the agent decisions")
		}
		host.Handle(tea.MouseClickMsg{X: bar.X + 2, Y: bar.Y, Button: tea.MouseLeft})
		if c.draft != nil {
			t.Fatal("visible Apply agent did not apply the edited instructions")
		}
		got := m.AppSettings().Agents.Categories[role.key]
		if role.key == "memory" {
			got = m.AppSettings().Memory.MemoryCategory
		} else if role.key == "auto-review" {
			if restrictions := m.AppSettings().Safety.AutoPermission.Instructions; restrictions != "Edited built-in instructions\nSecond line" {
				t.Fatalf("auto reviewer restrictions were lost: %q", restrictions)
			}
			continue
		}
		if got.Instructions != "Edited built-in instructions\nSecond line" {
			t.Fatalf("%s edited instructions were lost: %q", role.key, got.Instructions)
		}
	}
}

func TestSettingsBreadcrumbReturnsToCatalogWithoutDiscardingEdits(t *testing.T) {
	for _, page := range []string{"agents", "accounts"} {
		for _, dirty := range []bool{false, true} {
			m := New(theme.DefaultDark(), accountSettingsFixture(), nil)
			s := NewSurface(theme.DefaultDark(), m)
			host := ui.NewHost(theme.DefaultDark(), s)
			s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
			host.Resize(100, 32)
			host.Layout()
			switch page {
			case "agents":
				m.SelectCategory(catAgents)
				s.agents.begin("role/default", false)
				if dirty {
					s.agents.draft.SetValue("agent-instructions", ui.StringValue("Keep this draft"))
				}
			case "accounts":
				m.SelectCategory(catAccounts)
				m.selectAccount("work")
				s.openAccountEditor(false, "")
				if dirty {
					m.currentForm().SetValue(fieldAccountLabel, ui.StringValue("Keep this draft"))
				}
			}
			host.MarkLayout()
			host.Layout()
			result := host.Handle(tea.MouseClickMsg{X: s.contentRect.X + 1, Y: s.topPad(), Button: tea.MouseLeft})
			if !result.Handled {
				t.Fatalf("%s breadcrumb did not handle its visible parent segment", page)
			}
			open := s.agents.draft != nil || m.accountEditor != nil
			if open != dirty {
				t.Fatalf("%s breadcrumb open=%v, dirty=%v", page, open, dirty)
			}
			if dirty {
				if page == "agents" {
					if s.agents.confirm != "discard" {
						t.Fatal("agent breadcrumb did not preserve the discard confirmation")
					}
				} else if result.Cmd == nil {
					t.Fatal("editor breadcrumb did not request discard confirmation")
				}
			}
		}
	}
}
