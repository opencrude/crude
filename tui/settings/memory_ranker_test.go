package settings

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

func TestMemoryRankingSettingsJourney(t *testing.T) {
	for _, width := range []int{80, 160} {
		cfg := types.AppSettings{Accounts: map[string]provider.Account{"chosen": {ID: "chosen", ProviderID: "openai", Label: "Decision service"}}}
		m := New(theme.DefaultDark(), cfg, nil)
		s := NewSurface(theme.DefaultDark(), m)
		before := m.AppSettings()
		c := s.agents
		s.ShowAgent("role/memory")
		if c.tabs.ActiveID() != "model" || c.form.Field("ranker-model") == nil || c.form.Field("ranker-model").FieldState().Type != ui.FieldSelect {
			t.Fatal("Memory Agent must open on Model with the standard decision model picker")
		}
		if _, err := c.tabs.HandleControl(ui.EventContext{}, ui.ControlOperation{Verb: "select", ItemID: "ranking"}); err == nil {
			t.Fatal("Ranking still has a separate tab")
		}
		if heading := c.form.Field("ranking"); heading == nil || heading.FieldState().Type != ui.FieldSeparator {
			t.Fatal("Ranking needs a section heading")
		}
		c.form.FocusField("ranker-enabled")
		frame := bufferPlain(paintAccountSurface(t, s, width, 32))
		if !strings.Contains(frame, "Ranking") || !strings.Contains(frame, "Rank after actions") {
			t.Fatalf("missing ranking settings at %d: %s", width, frame)
		}
		c.draft.SetValue("ranker-enabled", ui.StringValue("on"))
		if c.apply() {
			t.Fatal("enabled without a connection")
		}
		effect, human, err := s.ControlMetadata(ui.NewID(AgentSettingsFormID, "ranker-enabled"), "")
		if err != nil || !human || effect != control.Authority {
			t.Fatal("native opt-in not protected", effect, human, err)
		}
		c.draft.SetValue("ranker-account_id", ui.StringValue("chosen"))
		c.draft.SetValue("ranker-model", ui.StringValue("my-decider"))
		c.draft.SetValue("ranker-every_actions", ui.StringValue("0"))
		if c.apply() {
			t.Fatal("zero cadence accepted")
		}
		c.draft.SetValue("ranker-every_actions", ui.StringValue("1"))
		c.draft.SetValue("ranker-top_k", ui.StringValue("3"))
		c.draft.SetValue("ranker-model_type", ui.StringValue(types.ModelTypeChat))
		if !c.apply() {
			t.Fatal("valid ranking settings rejected")
		}
		saved := m.AppSettings()
		if saved.Memory.Ranker.AccountID != "chosen" || saved.Memory.Ranker.EveryActions != 1 || saved.Memory.Ranker.TopK != 3 || saved.Memory.Ranker.ModelType != types.ModelTypeChat || saved.Memory.MemoryCategory.Model != "" || saved.Memory.Ranker.Model != "my-decider" {
			t.Fatalf("wrong owner: %+v", saved.Memory)
		}
		if !control.SettingsChangesAuthority(before.Memory.Ranker, saved.Memory.Ranker, "memory.ranker") {
			t.Fatal("autonomous account opt-in allowed")
		}
		c.begin("role/memory", false)
		if c.form.Field("ranker-model") == nil {
			t.Fatal("reopened ranking outside Model tab")
		}
		if c.draft.Field("ranker-model").Value.String() != "my-decider" {
			t.Fatal("saved ranker was not restored")
		}
	}
}

func TestMemoryRankingAccountCatalogAndManualEntry(t *testing.T) {
	cfg := newAeronSuggestionSettings()
	cfg.Memory.MemoryCategory = types.CategorySettings{AccountID: "role-account", Model: "extractor"}
	cfg.Memory.Ranker = types.MemoryRankerSettings{AccountID: "separate", Model: "chosen"}
	m := New(theme.DefaultDark(), cfg, nil)
	s := NewSurface(theme.DefaultDark(), m)
	open := s.ShowAgent("role/memory")
	c := s.agents
	request := accountModelsRequest(t, open, "separate")
	other := accountModelsRequest(t, open, "role-account")
	m.Update(ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Models: []types.ModelEntry{{ID: "quick", Name: "Decision model"}}})
	m.Update(ModelsMsg{ProviderID: other.ProviderID, AccountID: other.AccountID, Generation: other.Generation, Models: []types.ModelEntry{{ID: "extractor", Name: "Role model"}}})
	pick := c.form.Field("ranker-model")
	if pick == nil || !slices.Equal(pick.FieldState().Options, []ui.FormOption{{ID: "quick", Label: "Decision model"}}) {
		t.Fatal("ranking picker did not load the decision account's catalog")
	}
	key := func(k rune) { pick.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: k})) }
	key(tea.KeyEnter)
	key(tea.KeyEnter)
	if pick.FieldState().Value.String() != "quick" {
		t.Fatal("catalog selection did not commit")
	}
	c.draft.SetValue("ranker-account_id", ui.StringValue("role-account"))
	c.HandleAction(ui.EventContext{}, ui.Action{ID: "form.changed", Payload: "ranker-account_id"})
	if pick.FieldState().Value.String() != "" || !slices.Equal(pick.FieldState().Options, []ui.FormOption{{ID: "extractor", Label: "Role model"}}) {
		t.Fatalf("new account retained wrong model or options: %+v", pick.FieldState())
	}
	m.setModelsForAccount(request.ProviderID, "separate", []types.ModelEntry{{ID: "stale"}})
	if pick.FieldState().Options[0].ID != "extractor" {
		t.Fatal("late result replaced selected decision account")
	}
	key(tea.KeyEnter)
	for _, r := range "Custom/Exact:1" {
		pick.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: r, Text: string(r)}))
	}
	key(tea.KeyEnter)
	saveAgentCatalogSettings(t, s, &cfg)
	got := cfg.Memory
	if got.Ranker.Model != "Custom/Exact:1" || got.Ranker.AccountID != "role-account" || got.MemoryCategory.Model != "extractor" {
		t.Fatalf("independent models not saved: %+v", got)
	}
	s.ShowAgent("role/memory")
	for _, pg := range []string{"instructions", "tools"} {
		c.setPage(pg)
		if c.form.Field("ranker-model") != nil {
			t.Fatal("ranking duplicated on", pg)
		}
	}
	s.ShowAgent("role/researcher")
	if c.form.Field("ranker-model") != nil {
		t.Fatal("ranking leaked to another agent")
	}
}

// Memory ranking and Auto Review offer the same Model type control, so both
// accept a decision model or an ordinary chat model.
func TestAutoReviewAndRankingShareModelType(t *testing.T) {
	m := &Model{autoReviewer: types.AutoPermissionSettings{ModelType: types.ModelTypeDecision}}
	form := m.buildAutoReviewerForm()
	field := form.Field(fieldReviewModelType)
	if field == nil || field.Value.String() != types.ModelTypeDecision || len(field.Options) != 3 {
		t.Fatal("Auto Review has no model type control")
	}
	form.SetValue(fieldReviewModelType, ui.StringValue(types.ModelTypeChat))
	if got := autoPermissionFromForm(m.autoReviewer, form); got.ModelType != types.ModelTypeChat {
		t.Fatalf("model type not saved: %q", got.ModelType)
	}
	ranking := modelTypeField(fieldRankerModelType, "")
	if ranking.Label != field.Label || len(ranking.Options) != len(field.Options) || ranking.Value.String() != types.ModelTypeAutomatic {
		t.Fatal("ranking and Auto Review use different model type controls")
	}
}
