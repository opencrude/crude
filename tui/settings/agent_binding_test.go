package settings

import (
	"slices"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

const removedAccountID = "d1d2d3d4e5e6f7f8a9b0c1d2e3f4a5b6"

func agentBindingFixture() types.AppSettings {
	return types.AppSettings{
		Accounts: map[string]provider.Account{
			"example-account": {ID: "example-account", ProviderID: "custom", Label: "Example account", Connection: provider.Template{APIStandard: "openai", ProviderURL: "https://example.invalid/v1"}},
			"other-account":   {ID: "other-account", ProviderID: "ollama-cloud", Label: "Other account", Connection: provider.Template{APIStandard: "ollama", ProviderURL: "https://ollama.example/api"}},
		},
		Agents: types.AgentsSettings{
			Categories: map[string]types.CategorySettings{
				"default":          {AccountID: "example-account", Model: "example-main-model"},
				"researcher":       {},
				"crafter":          {},
				types.ErrandRoleID: {AccountID: "example-account", Model: "example-errand-model"},
			},
			AgentProfiles: map[string]types.AgentProfileSettings{"reviewer": {Category: "researcher"}},
		},
	}
}

// The screenshots showed Account "Not set" above Model "Errand's model · …",
// and Enabled as the last row. Every agent editor now names what an empty
// Account and Model use in the same words, and starts with its switch.
func TestAgentEditorsStartWithEnabledAndNameInheritedChoicesTheSameWay(t *testing.T) {
	m := New(theme.DefaultDark(), agentBindingFixture(), nil)
	// Loaded catalogs: the rows show their resting text, not "Fetching models…".
	m.models[accountModelKey("custom", "example-account")] = []types.ModelEntry{{ID: "example-main-model"}, {ID: "example-errand-model"}}
	m.models[accountModelKey("ollama-cloud", "other-account")] = []types.ModelEntry{{ID: "example-decider"}}
	s := NewSurface(theme.DefaultDark(), m)
	c := s.agents
	cases := []struct{ key, account, model string }{
		{"role/researcher", "Example account · Main Agent", "example-main-model · Main Agent"},
		{"role/crafter", "Example account · Main Agent", "example-main-model · Main Agent"},
		{"role/errand", "Example account · Main Agent", "example-main-model · Main Agent"},
		{"role/memory", "Example account · Errand", "example-errand-model · Errand"},
		{"role/auto-review", "Example account · Errand", "example-errand-model · Errand"},
		{"profile/reviewer", "Example account · Researcher", "example-main-model · Researcher"},
	}
	for _, tc := range cases {
		s.ShowAgent(tc.key)
		if c.tabs.ActiveID() != "model" {
			t.Fatalf("%s did not open on Model", tc.key)
		}
		var visible []string
		for _, field := range c.draft.Fields() {
			if field.Type != ui.FieldSeparator && c.form.Field(field.ID) != nil {
				visible = append(visible, field.ID)
			}
		}
		if len(visible) == 0 || visible[0] != "agent-enabled" {
			t.Fatalf("%s: Enabled is not the first row: %v", tc.key, visible)
		}
		account, model := c.draft.Field(fieldConnection), c.draft.Field(fieldModel)
		if account.Placeholder != tc.account || model.Placeholder != tc.model {
			t.Fatalf("%s: account %q, model %q", tc.key, account.Placeholder, model.Placeholder)
		}
		if account.Options[0].ID != "" || !strings.HasPrefix(tc.account, account.Options[0].Detail+" · ") {
			t.Fatalf("%s: the inherit choice does not match the placeholder: %+v", tc.key, account.Options[0])
		}
		frame := bufferPlain(paintAccountSurface(t, s, 160, 40))
		inherits := account.Value.String() == "" && model.Value.String() == ""
		if strings.Contains(frame, "Not set") || inherits && (!strings.Contains(frame, tc.account) || !strings.Contains(frame, tc.model)) {
			t.Fatalf("%s: the painted editor is not consistent:\n%s", tc.key, frame)
		}
		// A chosen account shows the parent's model only where it can be used.
		c.draft.SetValue(fieldConnection, ui.StringValue("other-account"))
		c.fieldChanged(fieldConnection)
		if tc.key == "role/memory" || tc.key == "role/auto-review" {
			if got := c.draft.Field(fieldModel).Placeholder; got != "Choose or enter a model ID" {
				t.Fatalf("%s: another account offered Errand's model: %q", tc.key, got)
			}
		}
		c.back()
	}

	s.ShowAgent("role/default")
	if c.draft.Field("agent-enabled") != nil {
		t.Fatal("Main Agent cannot be turned off")
	}

	s.ShowAgent("role/errand")
	c.setPage("suggestions")
	account, model := c.draft.Field(fieldSuggestAccount), c.draft.Field(fieldSuggestModel)
	if account.Placeholder != "Example account · Errand" || model.Placeholder != "example-errand-model · Errand" {
		t.Fatalf("suggestions: %q / %q", account.Placeholder, model.Placeholder)
	}
	c.setPage("model")
	c.draft.SetValue(fieldConnection, ui.StringValue("other-account"))
	c.fieldChanged(fieldConnection)
	if got := c.draft.Field(fieldSuggestAccount).Placeholder; got != "Other account · Errand" {
		t.Fatalf("suggestions did not follow the Errand draft: %q", got)
	}
	c.back()
}

// Memory ranking is a separate job with its own account; the page says so.
func TestMemoryAgentPageSeparatesRankingFromTheAgent(t *testing.T) {
	cfg := agentBindingFixture()
	cfg.Memory.Ranker = types.MemoryRankerSettings{Enabled: "on", AccountID: "other-account", Model: "example-decider"}
	m := New(theme.DefaultDark(), cfg, nil)
	s := NewSurface(theme.DefaultDark(), m)
	s.ShowAgent("role/memory")
	var order []string
	for _, field := range s.agents.draft.Fields() {
		if s.agents.form.Field(field.ID) != nil {
			order = append(order, field.Label)
		}
	}
	want := []string{"Enabled", "Learning and tidying", "Account", "Model", "Thinking", "Speed", "Memory ranking", "Rank memories", "Ranking account", "Ranking model"}
	if len(order) < len(want) || !slices.Equal(order[:len(want)], want) {
		t.Fatalf("Memory Agent rows = %q", order)
	}
	if got := s.agents.draft.Field(fieldRankerAccount); got.Value.String() != "other-account" || got.Placeholder != "Choose an account" {
		t.Fatalf("ranking account = %+v", got)
	}
}

// A setting that still names a removed account shows "Removed account",
// never the account's ID, and the account cannot be chosen again.
func TestRemovedAccountIsNamedNeverShownByID(t *testing.T) {
	cfg := agentBindingFixture()
	cfg.Agents.Categories["crafter"] = types.CategorySettings{AccountID: removedAccountID, Model: "other-model"}
	cfg.Memory.Ranker = types.MemoryRankerSettings{AccountID: removedAccountID, Model: "example-decider"}
	m := New(theme.DefaultDark(), cfg, nil)
	s := NewSurface(theme.DefaultDark(), m)
	for _, key := range []string{"role/crafter", "role/memory"} {
		s.ShowAgent(key)
		frame := bufferPlain(paintAccountSurface(t, s, 160, 60))
		if strings.Contains(frame, removedAccountID[:8]) || !strings.Contains(frame, types.RemovedAccountName) {
			t.Fatalf("%s shows the removed account by ID:\n%s", key, frame)
		}
		s.agents.back()
	}
	field := m.categories[catCrafter].Form.Field(fieldConnection)
	removed := field.Options[len(field.Options)-1]
	if removed.ID != removedAccountID || removed.Label != types.RemovedAccountName || !removed.Disabled {
		t.Fatalf("removed option = %+v", removed)
	}
	if rows := bufferPlain(paintAccountSurface(t, s, 160, 40)); strings.Contains(rows, removedAccountID[:8]) {
		t.Fatalf("the agent list shows an account ID:\n%s", rows)
	}
}

// Remove warns with every change it makes, from the same owner that makes
// them; afterwards the open Settings show the cleaned values.
func TestRemoveAccountWarnsWithEveryUseAndContinuesOnCleanedSettings(t *testing.T) {
	cfg := agentBindingFixture()
	cfg.Agents.Categories["crafter"] = types.CategorySettings{AccountID: "other-account", Model: "other-model"}
	cfg.Memory.Ranker = types.MemoryRankerSettings{Enabled: "on", AccountID: "other-account", Model: "example-decider"}
	cfg.Capabilities.WebSearchRoutes = []captypes.WebSearchRoute{{ID: "r1", Account: "other-account"}}
	m := New(theme.DefaultDark(), cfg, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAccounts)
	s.refreshForm()
	m.selectAccount("other-account")
	prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.remove"}))
	want := []string{"Crafter will use Main Agent's account and model.", "Memory ranking will turn off.", "A search method will be removed."}
	if prompt.Kind != PromptRemoveAccount || !slices.Equal(prompt.AccountUses, want) {
		t.Fatalf("warning = %q", prompt.AccountUses)
	}
	request := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.confirm-remove", Payload: prompt.ID}).Cmd().(AccountChangeMsg)

	// The core removes the account and releases its uses in one write.
	committed := types.CloneAppSettings(cfg)
	delete(committed.Accounts, "other-account")
	types.ReleaseAccount(&committed, "other-account")
	m.Update(AccountChangeResultMsg{Target: m, Change: request.Change, Settings: committed})
	next := m.AccountRemovedModel(committed, committed, types.WorkspaceOverrides{}, "other-account")
	s.SetModel(next)
	if next.hasUnsavedPreferences() {
		t.Fatal("the cleanup appears as unsaved edits")
	}
	got := next.AppSettings()
	if crafter := got.Agents.Categories["crafter"]; crafter.AccountID != "" || crafter.Model != "" {
		t.Fatalf("crafter = %+v", crafter)
	}
	if r := got.Memory.Ranker; r.Enabled != "off" || r.AccountID != "" {
		t.Fatalf("ranker = %+v", r)
	}
	s.ShowAgent("role/crafter")
	if got := s.agents.draft.Field(fieldConnection).Placeholder; got != "Example account · Main Agent" {
		t.Fatalf("crafter after removal: %q", got)
	}
}

// A failed Save scrolls to the row that needs a fix, also when the switch
// and section rows push it below the first screen.
func TestFailedAgentSaveRevealsTheInvalidRow(t *testing.T) {
	m := New(theme.DefaultDark(), agentBindingFixture(), nil)
	s := NewSurface(theme.DefaultDark(), m)
	s.ShowAgent("role/crafter")
	paintAccountSurface(t, s, 110, 30)
	s.agents.draft.SetValue("model-context-window", ui.StringValue("not-a-number"))
	if result := s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.save"}); result.Action != nil {
		t.Fatalf("invalid Save went on: %+v", result.Action)
	}
	frame := bufferPlain(paintAccountSurface(t, s, 110, 30))
	if !strings.Contains(frame, "not-a-number") || !strings.Contains(frame, "Must be a positive whole number.") {
		t.Fatalf("the invalid row is not visible:\n%s", frame)
	}
}
