package session

import (
	"testing"

	ttypes "crude/types"
)

func TestGetMemoryCategoryInheritsMainByDefault(t *testing.T) {
	cfg := &Config{ActiveProvider: "openrouter", Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {Provider: "openai", Model: "gpt-5.6"},
		}}},
	}}
	cfg.Providers = map[string]ProviderConfig{"openai": {APIKey: "k"}}
	got := cfg.GetMemoryCategory()
	if got.Model != "gpt-5.6" || got.Provider != "openai" {
		t.Fatalf("memory category = %+v, want inherit main", got)
	}
}

func TestGetMemoryCategoryUsesProfileOverride(t *testing.T) {
	cfg := &Config{ActiveProvider: "openrouter", Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {Provider: "openai", Model: "gpt-5.6"},
			CategoryMemory:  {Provider: "anthropic", Model: "claude-sonnet-4"},
		}}},
	}}
	cfg.Providers = map[string]ProviderConfig{
		"openai":    {APIKey: "k"},
		"anthropic": {APIKey: "k2"},
	}
	got := cfg.GetMemoryCategory()
	if got.Model != "claude-sonnet-4" || got.Provider != "anthropic" {
		t.Fatalf("memory category = %+v, want anthropic claude-sonnet-4", got)
	}
}

func TestGetMemoryCategoryFoldsLegacyModel(t *testing.T) {
	cfg := &Config{ActiveProvider: "openrouter", Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {Provider: "openai", Model: "gpt-5.6"},
		}}},
	}}
	cfg.Providers = map[string]ProviderConfig{"openai": {APIKey: "k"}}
	cfg.Memory = ttypes.MemorySettings{Model: "gpt-4o"}
	got := cfg.GetMemoryCategory()
	if got.Model != "gpt-4o" {
		t.Fatalf("memory category = %+v, want legacy gpt-4o", got)
	}
}
