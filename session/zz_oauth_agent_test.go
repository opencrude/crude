package session

import (
	"testing"

	"crude/provider"
)

// TestProviderForCategoryAttachesOAuth verifies the main agent's provider is
// built with the stored OAuth credentials when the category uses the ChatGPT
// subscription provider.
func TestProviderForCategoryAttachesOAuth(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "chatgpt",
		Providers: map[string]ProviderConfig{
			"chatgpt": {
				OAuth: &provider.OAuthCredentials{AccessToken: "AT-oauth", AccountID: "acct_1"},
			},
		},
		Agents: DefaultAgentsConfig(),
	}
	cfg.Agents.Profiles["default"].Categories[CategoryDefault] = CategoryConfig{
		Provider: "chatgpt", Model: "gpt-5.6",
	}

	mainCat := cfg.GetActiveDefaultCategory()
	if mainCat.OAuth == nil || mainCat.OAuth.AccessToken != "AT-oauth" {
		t.Fatalf("main category OAuth not resolved: %+v", mainCat.OAuth)
	}

	p, err := cfg.providerForCategory(mainCat)
	if err != nil {
		t.Fatalf("providerForCategory: %v", err)
	}
	if p.OAuth() == nil || p.OAuth().AccessToken != "AT-oauth" {
		t.Fatalf("provider OAuth not attached: %+v", p.OAuth())
	}
	// The bearer must be the OAuth access token, not an API key.
	headers, err := p.RequestHeaders(t.Context())
	if err != nil {
		t.Fatalf("headerSource: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer AT-oauth" {
		t.Errorf("auth = %q, want Bearer AT-oauth", got)
	}
}

// TestSubAgentOAuthResolutionMixedProviders verifies the sub-agent provider
// resolution picks the right credential per provider: OAuth for chatgpt, API
// key for opencode-go, even when the main agent is on a different provider.
func TestSubAgentOAuthResolutionMixedProviders(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "opencode-go",
		Providers: map[string]ProviderConfig{
			"opencode-go": {APIKey: "sk-opencode"},
			"chatgpt":     {OAuth: &provider.OAuthCredentials{AccessToken: "AT-oauth", AccountID: "acct_1"}},
		},
		Agents: DefaultAgentsConfig(),
	}
	cfg.Agents.Profiles["default"].Categories[CategoryDefault] = CategoryConfig{
		Provider: "opencode-go", Model: "deepseek-v4-flash",
	}

	// Sub-agent on chatgpt (OAuth) while main is on opencode-go.
	pChatGPT := resolveSubAgentProviderForTest(t, cfg, "chatgpt")
	if pChatGPT.OAuth() == nil || pChatGPT.OAuth().AccessToken != "AT-oauth" {
		t.Errorf("chatgpt sub-agent OAuth not attached: %+v", pChatGPT.OAuth())
	}
	if got := bearerForTest(t, pChatGPT); got != "Bearer AT-oauth" {
		t.Errorf("chatgpt sub-agent auth = %q, want Bearer AT-oauth", got)
	}

	// Sub-agent on opencode-go (API key) while main is on opencode-go.
	pOpenCode := resolveSubAgentProviderForTest(t, cfg, "opencode-go")
	if pOpenCode.OAuth() != nil {
		t.Errorf("opencode-go sub-agent should not have OAuth: %+v", pOpenCode.OAuth())
	}
	if got := bearerForTest(t, pOpenCode); got != "Bearer sk-opencode" {
		t.Errorf("opencode-go sub-agent auth = %q, want Bearer sk-opencode", got)
	}
}

// bearerForTest resolves the provider's request headers and returns the
// Authorization value.
func bearerForTest(t *testing.T, p *provider.Provider) string {
	t.Helper()
	headers, err := p.RequestHeaders(t.Context())
	if err != nil {
		t.Fatalf("headerSource: %v", err)
	}
	return headers.Get("Authorization")
}

// resolveSubAgentProviderForTest mirrors the provider resolution in
// SubAgentRunner.launchSubAgent for a given provider ID.
func resolveSubAgentProviderForTest(t *testing.T, cfg *Config, providerID string) *provider.Provider {
	t.Helper()
	resolvedProvider := providerID
	resolvedURL := ""
	resolvedAPIKey := ""
	var resolvedOAuth *provider.OAuthCredentials
	if prov, ok := cfg.Providers[resolvedProvider]; ok {
		resolvedAPIKey = prov.APIKey
		resolvedOAuth = prov.OAuth
		resolvedURL = prov.ProviderURL
	} else {
		mainCat := cfg.GetActiveDefaultCategory()
		resolvedAPIKey = mainCat.APIKey
		resolvedOAuth = mainCat.OAuth
		resolvedURL = mainCat.ProviderURL
	}
	p := provider.FromPreset(resolvedProvider, resolvedAPIKey, resolvedURL)
	if resolvedOAuth != nil {
		p.SetOAuth(resolvedOAuth)
	}
	return p
}
