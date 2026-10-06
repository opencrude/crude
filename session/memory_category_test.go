package session

import (
	"testing"

	ttypes "crude/types"
)

func TestGetMemoryCategoryInheritsMainByDefault(t *testing.T) {
	cfg := &Config{Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {AccountID: "openai-account", Model: "gpt-5.6"},
		}}},
	}}
	got := cfg.GetMemoryCategory()
	if got.Model != "gpt-5.6" || got.AccountID != "openai-account" {
		t.Fatalf("memory category = %+v, want inherit main", got)
	}
}

func TestGetMemoryCategoryUsesProfileOverride(t *testing.T) {
	cfg := &Config{Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {AccountID: "openai-account", Model: "gpt-5.6"},
			CategoryMemory:  {AccountID: "anthropic-account", Model: "claude-sonnet-4"},
		}}},
	}}
	got := cfg.GetMemoryCategory()
	if got.Model != "claude-sonnet-4" || got.AccountID != "anthropic-account" {
		t.Fatalf("memory category = %+v, want anthropic claude-sonnet-4", got)
	}
}

func TestGetMemoryCategoryFoldsLegacyModel(t *testing.T) {
	cfg := &Config{Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {AccountID: "openai-account", Model: "gpt-5.6"},
		}}},
	}}
	cfg.Memory = ttypes.MemorySettings{Model: "gpt-4o"}
	got := cfg.GetMemoryCategory()
	if got.Model != "gpt-4o" || got.AccountID != "openai-account" {
		t.Fatalf("memory category = %+v, want legacy gpt-4o", got)
	}
}
