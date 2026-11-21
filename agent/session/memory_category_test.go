package session

import (
	"testing"
)

func TestGetMemoryCategoryInheritsErrandByDefault(t *testing.T) {
	cfg := &Config{Agents: AgentsConfig{
		Profiles: map[string]ProfileConfig{"default": {Categories: map[string]CategoryConfig{
			CategoryDefault: {AccountID: "openai-account", Model: "main-model"},
			CategoryErrand:  {AccountID: "errand-account", Model: "errand-model", ThinkingEffort: "high", ServiceTier: "fast", Instructions: "Errand tools and prompt", AllowedTools: []string{"execute_command"}},
		}}},
	}}
	got := cfg.GetMemoryCategory()
	if got.Model != "errand-model" || got.AccountID != "errand-account" || got.ThinkingEffort != "" || got.ServiceTier != "" || got.Instructions != "" || len(got.AllowedTools) != 0 {
		t.Fatalf("memory category = %+v, want only the Errand binding", got)
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
