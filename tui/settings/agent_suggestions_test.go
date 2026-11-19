package settings

import (
	"fmt"
	"slices"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func newAeronSuggestionSettings() types.AppSettings {
	return types.AppSettings{
		Accounts: map[string]provider.Account{
			"role-account": {ID: "role-account", ProviderID: "openai", Label: "Role account", APIKey: "test",
				Connection: provider.Template{ProviderURL: "https://role.example.invalid/v1", APIStandard: "anthropic"}},
			"separate": {ID: "separate", ProviderID: "openai", Label: "Suggestions account", APIKey: "test",
				Connection: provider.Template{ProviderURL: "https://suggestions.example.invalid", APIStandard: "ollama"}},
		},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"aeron": {AccountID: "role-account", Model: "role-model"},
		}},
	}
}

func openAeronSuggestions(settings types.AppSettings) (*Model, *WorkspaceSettingsSurface, tea.Cmd) {
	th := theme.DefaultDark()
	m := New(th, settings, nil)
	s := NewSurface(th, m)
	m.SelectCategory(catAgents)
	cmd := s.agents.begin("role/aeron", false)
	s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.enter", Payload: "suggestions"})
	return m, s, cmd
}

func TestAeronSuggestionSpeedUsesResolvedAccountAndMarksUnavailableChoice(t *testing.T) {
	for _, tc := range []struct {
		name, account, saved string
		options              []ui.FormOption
	}{
		{"inherit Aeron's protocol", "", "", []ui.FormOption{{ID: "standard", Label: "Standard"}, {ID: "fast", Label: "Fast"}}},
		{"explicit protocol without tiers", "separate", "", nil},
		{"saved tier is not offered", "separate", "fast", []ui.FormOption{{ID: "fast", Label: "Fast (not offered)"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := newAeronSuggestionSettings()
			settings.Agents.Suggestions = types.SuggestionSettings{AccountID: tc.account, ServiceTier: tc.saved}
			if tc.account != "" {
				settings.Agents.Suggestions.Model = "custom-model"
			}
			_, s, _ := openAeronSuggestions(settings)
			c := s.agents
			tier := c.form.Field(fieldSuggestTier).FieldState()
			want := append([]ui.FormOption{{ID: "", Label: "Account default"}}, tc.options...)
			if !slices.Equal(tier.Options, want) || tier.Value.String() != tc.saved {
				t.Fatalf("Speed options = %+v, chosen=%q; want %+v, chosen=%q", tier.Options, tier.Value.String(), want, tc.saved)
			}
			applied := c.apply()
			if tc.saved != "" {
				want := []string{"This account does not offer this speed. Choose another speed."}
				if applied || !slices.Equal(tier.Errors, want) || c.page != "suggestions" {
					t.Fatalf("invalid saved Speed: applied=%t, errors=%v, page=%q; want %v on Suggestions", applied, tier.Errors, c.page, want)
				}
			} else if !applied {
				t.Fatalf("account default Speed blocked Apply: %v", tier.Errors)
			}
		})
	}
}

func TestAeronSuggestionNumericLimitsRejectInvalidValuesAndAcceptBounds(t *testing.T) {
	for _, tc := range []struct {
		label, id string
		low, high int
	}{
		{"Timeout", fieldSuggestTimeout, types.SuggestionMinTimeoutSeconds, types.SuggestionMaxTimeoutSeconds},
		{"Output limit", fieldSuggestOutput, types.SuggestionMinOutputTokens, types.SuggestionMaxOutputTokens},
	} {
		t.Run(tc.label, func(t *testing.T) {
			for _, value := range []string{"not a number", "1.5", "0", "-1", strconv.Itoa(tc.high + 1)} {
				t.Run(value, func(t *testing.T) {
					m, s, _ := openAeronSuggestions(newAeronSuggestionSettings())
					c := s.agents
					before := m.AppSettings().Agents.Suggestions
					c.draft.SetValue(tc.id, ui.StringValue(value))
					want := []string{fmt.Sprintf("Enter a whole number from %d to %d.", tc.low, tc.high)}
					if c.apply() || !slices.Equal(c.form.Field(tc.id).FieldState().Errors, want) || c.page != "suggestions" ||
						m.AppSettings().Agents.Suggestions != before {
						t.Fatalf("%s=%q was not rejected with %v on Suggestions", tc.label, value, want)
					}
				})
			}
			for _, value := range []int{tc.low, tc.high} {
				t.Run(strconv.Itoa(value), func(t *testing.T) {
					m, s, _ := openAeronSuggestions(newAeronSuggestionSettings())
					c := s.agents
					c.draft.SetValue(tc.id, ui.StringValue(strconv.Itoa(value)))
					if !c.apply() {
						t.Fatalf("%s=%d blocked Apply: %v", tc.label, value, c.draft.Field(tc.id).Errors)
					}
					saved := m.AppSettings().Agents.Suggestions
					got := saved.TimeoutSeconds
					if tc.id == fieldSuggestOutput {
						got = saved.MaxOutputTokens
					}
					if got != value {
						t.Fatalf("applied %s=%d, want %d", tc.label, got, value)
					}
				})
			}
		})
	}
}

func TestAeronSuggestionsFetchesModelsAndShowsTheAccountError(t *testing.T) {
	settings := newAeronSuggestionSettings()
	settings.Agents.Suggestions = types.SuggestionSettings{AccountID: "separate", Model: "quick"}
	m, s, open := openAeronSuggestions(settings)
	field := s.agents.form.Field(fieldSuggestModel).FieldState()
	request := accountModelsRequest(t, open, "separate")
	if request.ProviderID != "openai" || request.StandardID != "ollama" || request.BaseURL != "https://suggestions.example.invalid" ||
		!field.Loading || field.LoadMessage != "Fetching models…" {
		t.Fatalf("Aeron prefetch = account %q, protocol %q, URL %q; loading=%t %q", request.AccountID, request.StandardID, request.BaseURL, field.Loading, field.LoadMessage)
	}
	const problem = "Cannot load models. Check the connection."
	m.Update(ModelsErrorMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Error: problem})
	if field.Loading || !slices.Equal(field.Errors, []string{problem}) {
		t.Fatalf("Suggestions Model did not show fetch failure: loading=%t, errors=%v", field.Loading, field.Errors)
	}
	openAgain := s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "form.select.open", Payload: fieldSuggestModel})
	retry := accountModelsRequest(t, openAgain.Cmd, "separate")
	m.Update(ModelsMsg{ProviderID: retry.ProviderID, AccountID: retry.AccountID, Generation: retry.Generation,
		Models: []types.ModelEntry{{ID: "quick", Name: "Quick model"}}})
	if field.Loading || len(field.Errors) != 0 || !slices.Contains(field.Options, ui.FormOption{ID: "quick", Label: "Quick model"}) ||
		field.Value.String() != "quick" {
		t.Fatalf("Suggestions Model did not recover with the account's result: options=%+v, selected=%q, loading=%t, errors=%v",
			field.Options, field.Value.String(), field.Loading, field.Errors)
	}
	if cmd := s.agents.begin("role/aeron", false); cmd != nil {
		// The Model tab may still fetch Aeron's separate account. The
		// Suggestions model list already arrived and must not fetch again.
		if _, found := findAccountModelsRequest(cmd, "separate"); found {
			t.Fatal("reopening Aeron fetched the cached Suggestions account again")
		}
	}
}

func TestAeronSuggestionChoicesSaveReopenAndReturnToDefaults(t *testing.T) {
	stored := newAeronSuggestionSettings()
	m, s, _ := openAeronSuggestions(stored)
	change := chooseAeronSuggestion(t, s.agents, fieldSuggestAccount, "separate")
	request := accountModelsRequest(t, change, "separate")
	m.Update(ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation,
		Models: []types.ModelEntry{{ID: "quick", Name: "Quick model"}}})
	chooseAeronSuggestion(t, s.agents, fieldSuggestModel, "quick")
	saveAgentCatalogSettings(t, s, &stored)
	if got := stored.Agents.Suggestions; got != (types.SuggestionSettings{AccountID: "separate", Model: "quick"}) {
		t.Fatalf("saved Suggestion choices = %+v", got)
	}
	m, s, _ = openAeronSuggestions(stored)
	c := s.agents
	if account, model := c.draft.Field(fieldSuggestAccount).Value.String(), c.draft.Field(fieldSuggestModel).Value.String(); account != "separate" || model != "quick" {
		t.Fatalf("reopened suggests account=%q model=%q, want separate/quick", account, model)
	}
	change = chooseAeronSuggestion(t, c, fieldSuggestAccount, "")
	if request, ok := findAccountModelsRequest(change, "role-account"); ok {
		m.Update(ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation,
			Models: []types.ModelEntry{{ID: "role-model", Name: "Role model"}}})
	}
	if c.draft.Field(fieldSuggestModel).Value.String() != "" {
		chooseAeronSuggestion(t, c, fieldSuggestModel, "")
	}
	saveAgentCatalogSettings(t, s, &stored)
	if got := stored.Agents.Suggestions; got != (types.SuggestionSettings{}) {
		t.Fatalf("returned to defaults but stored Suggestion overrides remain: %+v", got)
	}
	_, s, _ = openAeronSuggestions(stored)
	for id, want := range map[string]string{
		fieldSuggestAccount: "", fieldSuggestModel: "", fieldSuggestFormat: types.SuggestionFormatChat,
		fieldSuggestTier: "", fieldSuggestTimeout: "5", fieldSuggestOutput: "256",
	} {
		if got := s.agents.draft.Field(id).Value.String(); got != want {
			t.Errorf("reopened default %s = %q, want %q", id, got, want)
		}
	}
}

func chooseAeronSuggestion(t *testing.T, c *agentCatalog, id, value string) tea.Cmd {
	t.Helper()
	field := c.form.Field(id).FieldState()
	if !slices.ContainsFunc(field.Options, func(o ui.FormOption) bool { return o.ID == value && !o.Disabled }) {
		t.Fatalf("%s has no selectable %q choice: %+v", field.Label, value, field.Options)
	}
	c.draft.SetValue(id, ui.StringValue(value))
	return c.HandleAction(ui.EventContext{}, ui.Action{ID: "form.changed", Payload: id}).Cmd
}

func saveAgentCatalogSettings(t *testing.T, s *WorkspaceSettingsSurface, stored *types.AppSettings) {
	t.Helper()
	applied := s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.save"})
	if applied.Action == nil || applied.Action.ID != "settings.save" {
		t.Fatal("valid agent draft did not pass validation and reach Save")
	}
	result := s.HandleAction(ui.EventContext{}, *applied.Action)
	if result.Cmd == nil {
		t.Fatal("Save did not emit the staged Settings edits")
	}
	msg, ok := result.Cmd().(SaveMsg)
	if !ok || msg.Scope != types.SettingsScopeApplication {
		t.Fatalf("Agent Save = %+v, want an application Save", msg)
	}
	if err := types.ApplySettingsEdits(stored, msg.Edits); err != nil {
		t.Fatal(err)
	}
}

func accountModelsRequest(t *testing.T, cmd tea.Cmd, account string) FetchModelsMsg {
	t.Helper()
	msg, ok := findAccountModelsRequest(cmd, account)
	if !ok || msg.Generation == 0 {
		t.Fatalf("no model fetch was scheduled for %q", account)
	}
	return msg
}

func findAccountModelsRequest(cmd tea.Cmd, account string) (FetchModelsMsg, bool) {
	if cmd != nil {
		switch msg := cmd().(type) {
		case FetchModelsMsg:
			return msg, msg.AccountID == account
		case tea.BatchMsg:
			for _, child := range msg {
				if request, ok := findAccountModelsRequest(child, account); ok {
					return request, true
				}
			}
		}
	}
	return FetchModelsMsg{}, false
}
