package settings

import (
	"crude/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"strings"
	"testing"
)

func TestAccountsReplaceProvidersAndOwnCompleteConnection(t *testing.T) {
	m := New(theme.DefaultDark(), accountSettingsFixture(), nil)
	s := NewSurface(theme.DefaultDark(), m)
	for _, item := range m.CategoryItems() {
		if item.Label == "Providers" {
			t.Fatal("Providers remains in Settings navigation")
		}
	}
	m.SelectAccounts()
	m.selectAccount("work")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	form := m.categories[catAccounts].Form
	for _, id := range []string{accountFieldEndpoint, accountFieldAuthType, accountFieldHeaders, accountFieldProtocol, fieldDialTimeout, fieldExtraBody} {
		if form.Field(id) == nil {
			t.Fatalf("unified account editor omitted %s", id)
		}
	}
	if form.Field(accountFieldSeed) != nil || form.Field(fieldModel) != nil {
		t.Fatal("existing account has preset/model selector")
	}
	for _, field := range form.Fields() {
		if strings.Contains(field.ID, "override") {
			t.Fatal("account retained template override controls")
		}
	}
	m.SetValueForTest(catAccounts, accountFieldEndpoint, "https://lab.example/v1")
	m.SetValueForTest(catAccounts, accountFieldAuthType, provider.AuthCustomHeader)
	m.SetValueForTest(catAccounts, accountFieldAuthHeader, "X-Lab-Key")
	m.SetValueForTest(catAccounts, fieldAPIKey, "new-key")
	m.SetValueForTest(catAccounts, accountFieldHeaders, "X-Trace: enabled")
	m.SetValueForTest(catAccounts, accountFieldProtocol, "anthropic")
	change, err := m.accountEditChange()
	if err != nil {
		t.Fatal(err)
	}
	c := change.After.Connection
	if c.ProviderURL != "https://lab.example/v1" || c.AuthType != provider.AuthCustomHeader || c.AuthHeader != "X-Lab-Key" || c.APIStandard != "anthropic" || c.Headers["X-Trace"] != "enabled" {
		t.Fatal("unified fields did not commit one account connection")
	}
	if m.accounts["home"].APIKey != "home-secret" || m.accounts["work"].Connection.ProviderURL == c.ProviderURL {
		t.Fatal("draft altered saved account or neighbour")
	}
}

func TestNewAccountPresetIsCopyNotInheritance(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	s.ShowAccounts()
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.begin-add"})
	if m.accountEditor == nil || len(m.accounts) != 0 {
		t.Fatal("new account modified saved catalog")
	}
	m.SetValueForTest(catAccounts, accountFieldSeed, "groq")
	if got := m.value(catAccounts, accountFieldEndpoint); got != "https://api.groq.com/openai/v1" {
		t.Fatalf("preset endpoint=%s", got)
	}
	m.SetValueForTest(catAccounts, fieldAccountLabel, "My Groq")
	m.SetValueForTest(catAccounts, fieldAPIKey, "test-key")
	change, err := m.accountEditChange()
	if err != nil {
		t.Fatal(err)
	}
	if change.Before != nil || change.After == nil || change.After.Label != "My Groq" || change.After.Connection.ProviderURL != "https://api.groq.com/openai/v1" {
		t.Fatal("new account did not own preset copy")
	}
	change.After.Connection.ProviderURL = "https://edited.example/v1"
	if provider.DefaultTemplates()["groq"].ProviderURL == change.After.Connection.ProviderURL {
		t.Fatal("editing account changed immutable seed")
	}
}

func TestAccountClonePreservesConnectionButNeverCredentials(t *testing.T) {
	settings := accountSettingsFixture()
	source := settings.Accounts["work"]
	source.Connection.ProviderURL = "https://custom.example/v1"
	source.Connection.Headers = map[string]string{"X-Team": "alpha"}
	source.OAuth = &provider.OAuthCredentials{AccessToken: "private-token"}
	settings.Accounts["work"] = source
	m := New(theme.DefaultDark(), settings, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectAccounts()
	m.selectAccount("work")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.clone"})
	if m.accountEditor == nil {
		t.Fatal("Clone did not open a draft")
	}
	draft := m.accountEditor.draft
	if draft.ID == source.ID || draft.ID == "" || draft.APIKey != "" || draft.OAuth != nil || draft.Connection.ProviderURL != source.Connection.ProviderURL {
		t.Fatal("Clone lost custom config or copied credentials/identity")
	}
	draft.Connection.Headers["X-Team"] = "changed"
	if m.accounts["work"].Connection.Headers["X-Team"] != "alpha" {
		t.Fatal("clone connection aliases source")
	}
}
