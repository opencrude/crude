package session

import (
	"testing"
)

// TestMergeCategoryConfigClearsStaleURLOnProviderSwitch reproduces the bug
// where switching a role from opencode-go to chatgpt kept the opencode-go base
// URL and standard, so the agent posted the chatgpt OAuth token to the wrong
// endpoint and failed auth.
func TestMergeCategoryConfigClearsStaleURLOnProviderSwitch(t *testing.T) {
	base := CategoryConfig{
		Provider:    "opencode-go",
		ProviderURL: "https://opencode.ai/zen/go/v1",
		APIStandard: "openai",
		Model:       "deepseek-v4-flash",
	}
	// A settings save for chatgpt carries only provider + model + standard;
	// ProviderURL is not part of the agent form, and the standard is "inherit"
	// (empty) after a provider switch.
	override := CategoryConfig{
		Provider: "chatgpt",
		Model:    "gpt-5.6-luna",
	}

	merged := mergeCategoryConfig(base, override)
	if merged.Provider != "chatgpt" {
		t.Errorf("provider = %q, want chatgpt", merged.Provider)
	}
	if merged.ProviderURL != "" {
		t.Errorf("provider_url = %q, want empty so it resolves from chatgpt's preset", merged.ProviderURL)
	}
	if merged.APIStandard != "" {
		t.Errorf("api_standard = %q, want empty so it resolves to codex", merged.APIStandard)
	}
	if merged.Model != "gpt-5.6-luna" {
		t.Errorf("model = %q, want gpt-5.6-luna", merged.Model)
	}
}

// TestMergeCategoryConfigKeepsExplicitOverrideValues verifies that an explicit
// ProviderURL/APIStandard in the override still wins.
func TestMergeCategoryConfigKeepsExplicitOverrideValues(t *testing.T) {
	base := CategoryConfig{
		Provider:    "opencode-go",
		ProviderURL: "https://opencode.ai/zen/go/v1",
		APIStandard: "openai",
	}
	override := CategoryConfig{
		Provider:    "chatgpt",
		ProviderURL: "https://proxy.example.com/codex",
		APIStandard: "codex",
	}
	merged := mergeCategoryConfig(base, override)
	if merged.ProviderURL != "https://proxy.example.com/codex" {
		t.Errorf("provider_url = %q, want the explicit override", merged.ProviderURL)
	}
	if merged.APIStandard != "codex" {
		t.Errorf("api_standard = %q, want codex", merged.APIStandard)
	}
}
