package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
)

// TestMemoryFormKeepsRecallAndAddsAgentBehavior verifies the Memory settings
// form retains the recall/extract/consolidation controls and adds an agent
// behavior section (provider/model/thinking) that round-trips into
// Memory.MemoryCategory — without losing the legacy settings.
func TestMemoryFormKeepsRecallAndAddsAgentBehavior(t *testing.T) {
	th := theme.DefaultDark()
	disabled := 0
	current := types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {APIKey: "k", Model: "model-a"},
			"openai":     {APIKey: "k2", Model: "model-b"},
		},
		Memory: types.MemorySettings{
			AutoRecall:         "off",
			AutoExtract:        "off",
			Consolidation:      "off",
			RecallBudgetTokens: 2000,
			RecallLimit:        20,
			ExtractEveryTurns:  7,
			MemoryCategory: types.CategorySettings{
				Provider: "openai", Model: "model-b", ThinkingEffort: "high", ContextCompactionPercent: &disabled,
			},
		},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "model-a"},
		}},
	}
	m := New(th, current, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}})

	got := m.AppSettings()
	if got.Memory.AutoRecall != "off" || got.Memory.RecallBudgetTokens != 2000 ||
		got.Memory.RecallLimit != 20 || got.Memory.ExtractEveryTurns != 7 {
		t.Fatalf("recall settings lost: %#v", got.Memory)
	}
	if got.Memory.MemoryCategory.Provider != "openai" || got.Memory.MemoryCategory.Model != "model-b" ||
		got.Memory.MemoryCategory.ThinkingEffort != "high" || got.Memory.MemoryCategory.ContextCompactionPercent == nil ||
		*got.Memory.MemoryCategory.ContextCompactionPercent != 0 {
		t.Fatalf("memory agent behavior lost: %#v", got.Memory.MemoryCategory)
	}
}
