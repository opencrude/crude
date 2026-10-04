package session

import (
	"context"
	"testing"

	"crude/provider"
)

// Switching an agent's account changes the endpoint and wire protocol as one
// unit. Stale transport values from a former account must never receive a new
// account's OAuth token.
func TestAgentAccountSwitchUsesNewTemplateWithoutStaleTransport(t *testing.T) {
	base := CategoryConfig{AccountID: "opencode", Model: "deepseek-v4-flash"}
	override := CategoryConfig{AccountID: "codex", Model: "gpt-5.6-luna"}
	merged := mergeCategoryConfig(base, override)
	cfg := &Config{
		Providers: provider.DefaultTemplates(),
		Accounts: map[string]provider.Account{
			"opencode": {ID: "opencode", ProviderID: "opencode-go", APIKey: "opencode-secret"},
			"codex":    {ID: "codex", ProviderID: "chatgpt", OAuth: &provider.OAuthCredentials{AccessToken: "codex-secret"}},
		},
	}
	p, err := cfg.providerForCategory(merged)
	if err != nil {
		t.Fatal(err)
	}
	if merged.AccountID != "codex" || merged.Model != "gpt-5.6-luna" || p.AccountID != "codex" || p.StandardName != "codex" || p.BaseURL != cfg.Providers["chatgpt"].ProviderURL {
		t.Fatalf("agent account switch retained prior transport: merged=%+v provider=%+v", merged, p)
	}
}

func TestAccountOverrideWinsWithoutChangingAgentModel(t *testing.T) {
	endpoint := "https://private.example/v1"
	protocol := "anthropic"
	auth := provider.AuthCustomHeader
	header := "X-Work-Key"
	headers := map[string]string{"X-Trace": "work"}
	extra := map[string]any{"work": true}
	disabled := false
	cfg := &Config{Providers: provider.DefaultTemplates(), Accounts: map[string]provider.Account{
		"work": {ID: "work", ProviderID: "openrouter", APIKey: "work-secret", Overrides: provider.TemplateOverrides{
			ProviderURL: &endpoint, APIStandard: &protocol, AuthType: &auth, AuthHeader: &header, Headers: &headers,
			Dialer: &provider.DialSettings{MaxAttempts: 2}, Protocol: &provider.ProtocolSettings{Store: &disabled}, ExtraBody: &extra,
		}},
		"personal": {ID: "personal", ProviderID: "openrouter", APIKey: "personal-secret"},
	}}
	cat := CategoryConfig{AccountID: "work", Model: "one-model"}
	p, err := cfg.providerForCategory(cat)
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != endpoint || p.Model != "one-model" || p.ProtocolID() != "anthropic" || p.DialSettings().MaxAttempts != 2 || p.ProtocolSettings().Store == nil || *p.ProtocolSettings().Store || p.ExtraBody()["work"] != true {
		t.Fatalf("account override or agent model lost: %+v", p)
	}
	h, err := p.RequestHeaders(context.Background())
	if err != nil || h.Get("X-Work-Key") != "work-secret" || h.Get("X-Trace") != "work" {
		t.Fatalf("work account auth/headers were not resolved: %v", err)
	}
	other, err := cfg.providerForCategory(CategoryConfig{AccountID: "personal", Model: "two-model"})
	if err != nil {
		t.Fatal(err)
	}
	if other.BaseURL == endpoint || other.ProtocolID() == "anthropic" || other.DialSettings().MaxAttempts == 2 || other.ExtraBody()["work"] != nil || other.Model != "two-model" {
		t.Fatal("account override leaked into another account using the same template")
	}
}

func TestAccountOverrideCanDisableResponsesMediaReplayRecovery(t *testing.T) {
	disabled := false
	cfg := &Config{
		Providers: provider.DefaultTemplates(),
		Accounts: map[string]provider.Account{
			"guarded": {
				ID: "guarded", ProviderID: "openai", APIKey: "guarded-secret",
				Overrides: provider.TemplateOverrides{
					Protocol: &provider.ProtocolSettings{MediaReplayRecovery: &disabled},
				},
			},
			"default": {ID: "default", ProviderID: "openai", APIKey: "default-secret"},
		},
	}
	guarded, err := cfg.providerForCategory(CategoryConfig{AccountID: "guarded", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if guarded.MediaReplayRecoveryEnabled() {
		t.Fatal("account override did not disable adaptive media replay recovery")
	}
	inherited, err := cfg.providerForCategory(CategoryConfig{AccountID: "default", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inherited.MediaReplayRecoveryEnabled() {
		t.Fatal("account override leaked into the template default")
	}
}
