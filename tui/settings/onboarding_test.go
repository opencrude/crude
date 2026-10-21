package settings

import (
	"errors"
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestOnboardingAccountOfferIsAnExplicitMainAgentDraft(t *testing.T) {
	th := theme.DefaultDark()
	settings := accountSettingsFixture()
	settings.Accounts["new"] = provider.Account{ID: "new", ProviderID: "openai", Label: "New account", APIKey: "secret", Connection: provider.DefaultTemplates()["openai"]}
	m := New(th, settings, nil)
	s := NewSurface(th, m)
	before := m.AppSettings().Agents.Categories["default"]
	cmd := s.ShowMainAgentForAccount("new")
	if cmd == nil {
		t.Fatal("account offer dropped model discovery command")
	}
	request, ok := cmd().(FetchModelsMsg)
	if !ok || request.AccountID != "new" || request.ProviderID != "openai" {
		t.Fatalf("wrong account model request: %#v", request)
	}
	if got := s.agents.draft.Field(fieldConnection).Value.String(); got != "new" {
		t.Fatalf("offered account = %q", got)
	}
	if got := m.AppSettings().Agents.Categories["default"]; got.AccountID != before.AccountID || got.Model != before.Model {
		t.Fatal("opening model setup silently applied Main Agent defaults")
	}
	for _, size := range [][2]int{{124, 28}, {80, 24}} {
		painted := bufferPlain(paintAccountSurface(t, s, size[0], size[1]))
		for _, want := range []string{"Main Agent", "New account", "Apply agent"} {
			if !strings.Contains(painted, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, painted)
			}
		}
	}
	m.Update(ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Models: []types.ModelEntry{{ID: "chosen", Name: "Chosen model"}}})
	s.syncModel()
	s.agents.draft.SetValue(fieldModel, ui.StringValue("chosen"))
	if got := m.AppSettings().Agents.Categories["default"]; got.AccountID != before.AccountID || got.Model != before.Model {
		t.Fatal("selecting model bypassed Apply agent")
	}
	s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.apply"})
	if got := m.AppSettings().Agents.Categories["default"]; got.AccountID != "new" || got.Model != "chosen" {
		t.Fatalf("explicit Apply agent did not publish settings draft: %+v", got)
	}
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if result.Cmd == nil {
		t.Fatal("explicit Save did not request persistence")
	}
	if _, ok := result.Cmd().(SaveMsg); !ok {
		t.Fatal("Save did not use ordinary preferences path")
	}
}

func TestOnboardingAccountOfferPreservesExistingDraft(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	s.ShowMainAgent()
	draft := s.agents.draft
	draft.SetValue("agent-instructions", ui.StringValue("Keep this instruction draft"))
	if cmd := s.ShowMainAgentForAccount("work"); cmd != nil || s.agents.draft != draft || draft.Field("agent-instructions").Value.String() != "Keep this instruction draft" {
		t.Fatal("account handoff replaced an unapplied agent draft")
	}
}

func TestOnboardingAccountFailureRetainsAddDraft(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	s := NewSurface(th, m)
	s.ShowAccountSetup()
	m.SetValueForTest(catAccounts, fieldAccountLabel, "My first account")
	m.SetValueForTest(catAccounts, fieldAPIKey, "secret")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if result.Cmd == nil {
		t.Fatal("valid Add did not request persistence")
	}
	request := result.Cmd().(AccountChangeMsg)
	m.Update(AccountChangeResultMsg{Target: m, Change: request.Change, Err: errors.New("disk full")})
	s.syncModel()
	if m.accountEditor == nil || m.value(catAccounts, fieldAccountLabel) != "My first account" || len(m.accounts) != 0 {
		t.Fatal("account failure lost draft or installed unsaved account")
	}
	painted := bufferPlain(paintAccountSurface(t, s, 124, 28))
	if !strings.Contains(painted, "disk full") || !strings.Contains(painted, "My first account") {
		t.Fatal("failed save did not paint retained draft and reason")
	}
}
