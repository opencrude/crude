package session

import (
	"testing"

	"crude/sessiondb"
	ttypes "crude/tui/types"
)

func TestConfigWithSessionOverridesUsesWorkspaceThenSession(t *testing.T) {
	base := &Config{
		ActiveProvider: "openrouter",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "workspace-key", Model: "workspace-model", ThinkingEffort: "low"},
			"openai":     {APIKey: "openai-key", ProviderURL: "https://api.openai.com/v1"},
		},
		Agents: AgentsConfig{
			ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {Categories: map[string]CategoryConfig{
					CategoryDefault: {Provider: "openrouter", Model: "workspace-main", ThinkingEffort: "medium"},
					"crafter":       {Provider: "openrouter", Model: "workspace-crafter"},
				}},
			},
		},
	}
	overrides := map[string]sessiondb.AgentSettingOverride{
		"crafter": {Provider: "openai", Model: "gpt-5", ThinkingEffort: "high"},
	}

	effective := configWithSessionOverrides(base, overrides)
	crafter := effective.GetCategoryConfig("crafter")
	if crafter.Provider != "openai" || crafter.Model != "gpt-5" || crafter.ThinkingEffort != "high" {
		t.Fatalf("crafter override not applied: %#v", crafter)
	}
	if crafter.APIKey != "openai-key" {
		t.Fatalf("provider credentials were not inherited: %#v", crafter)
	}
	main := effective.GetActiveDefaultCategory()
	if main.Model != "workspace-main" {
		t.Fatalf("unoverridden main model = %q, want workspace-main", main.Model)
	}
	if base.GetCategoryConfig("crafter").Model != "workspace-crafter" {
		t.Fatal("base workspace config was mutated")
	}
}

func TestSettingsToSessionOverridesHonorsExplicitMap(t *testing.T) {
	settings := ttypes.Settings{
		Agents: ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{
			"default": {Provider: "ignored", Model: "ignored"},
		}},
		SessionOverrides: map[string]ttypes.CategorySettings{
			"researcher": {Provider: "anthropic", Model: "claude", ThinkingEffort: "low"},
		},
	}
	overrides := settingsToSessionOverrides(settings)
	if len(overrides) != 1 || overrides["researcher"].Model != "claude" {
		t.Fatalf("explicit override map not honored: %#v", overrides)
	}
}
