package session

import (
	"testing"

	"crude/provider"
)

// TestProviderForCategoryAttachesOAuth verifies the main agent's provider is
// built with the stored OAuth credentials when the category uses the ChatGPT
// subscription provider.
func TestProviderForCategoryBindsOAuthAccount(t *testing.T) {
	agents := DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "work"}
	agents.Profiles[agents.ActiveProfile] = profile
	cfg := &Config{Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "chatgpt", OAuth: &provider.OAuthCredentials{AccessToken: "AT-oauth", AccountID: "acct_1"}, Connection: provider.DefaultTemplates()["chatgpt"]}},
		Agents: agents}
	p, err := cfg.providerForCategory(cfg.GetActiveDefaultCategory())
	if err != nil {
		t.Fatal(err)
	}
	if p.AccountID != "work" {
		t.Fatalf("account = %q", p.AccountID)
	}
	if got := bearerForTest(t, p); got != "Bearer AT-oauth" {
		t.Fatalf("auth = %q", got)
	}
}

func TestAccountSelectionDoesNotCrossSameProviderIdentities(t *testing.T) {
	cfg := &Config{Accounts: map[string]provider.Account{
		"work":     {ID: "work", ProviderID: "openrouter", APIKey: "work-key", Connection: provider.DefaultTemplates()["openrouter"]},
		"personal": {ID: "personal", ProviderID: "openrouter", APIKey: "personal-key", Connection: provider.DefaultTemplates()["openrouter"]}}}
	for _, tc := range []struct{ id, key string }{{"work", "work-key"}, {"personal", "personal-key"}} {
		p, err := cfg.providerForCategory(CategoryConfig{AccountID: tc.id})
		if err != nil {
			t.Fatal(err)
		}
		if got := bearerForTest(t, p); got != "Bearer "+tc.key {
			t.Errorf("%s auth = %q", tc.id, got)
		}
	}
	delete(cfg.Accounts, "personal")
	if _, err := cfg.providerForCategory(CategoryConfig{AccountID: "personal"}); err == nil {
		t.Fatal("deleted explicit account silently reassigned")
	}
}

func bearerForTest(t *testing.T, p *provider.Provider) string {
	t.Helper()
	headers, err := p.RequestHeaders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return headers.Get("Authorization")
}
