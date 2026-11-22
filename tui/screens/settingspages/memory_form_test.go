package settingspages

import (
	"testing"

	"crude/agent/provider"
	"crude/tui/theme"
	"crude/types/setting"
)

// TestMemoryFormKeepsRecallAndAddsAgentBehavior verifies the Memory settings
// form retains the recall/extract/consolidation controls and adds an agent
// behavior section (provider/model/thinking) that round-trips into
// Memory.MemoryCategory, without losing the legacy settings.
func TestMemoryFormKeepsRecallAndAddsAgentBehavior(t *testing.T) {
	th := theme.DefaultDark()
	disabled := 0
	current := setting.App{

		Accounts: map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Personal", Connection: provider.DefaultTemplates()["openrouter"]}, "work": {ID: "work", ProviderID: "openai", Label: "Work", Connection: provider.DefaultTemplates()["openai"]}},
		Memory: setting.Memory{
			AutoRecall:         "off",
			AutoExtract:        "off",
			Consolidation:      "off",
			RecallBudgetTokens: 2000,
			RecallLimit:        20,
			ExtractEveryTurns:  7,
			MemoryCategory: setting.Category{
				Provider: "openai", AccountID: "work", Model: "model-b", ThinkingEffort: "high", ContextCompactionPercent: &disabled,
			},
		},
		Agents: setting.Agents{Categories: map[string]setting.Category{
			"default": {AccountID: "router", Model: "model-a"},
		}},
	}
	m := New(th, current, []setting.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}})

	got := m.AppSettings()
	if got.Memory.AutoRecall != "off" || got.Memory.RecallBudgetTokens != 2000 ||
		got.Memory.RecallLimit != 20 || got.Memory.ExtractEveryTurns != 7 {
		t.Fatalf("recall settings lost: %#v", got.Memory)
	}
	if got.Memory.MemoryCategory.AccountID != "work" || got.Memory.MemoryCategory.Model != "model-b" ||
		got.Memory.MemoryCategory.ThinkingEffort != "high" || got.Memory.MemoryCategory.ContextCompactionPercent == nil ||
		*got.Memory.MemoryCategory.ContextCompactionPercent != 0 {
		t.Fatalf("memory agent behavior lost: %#v", got.Memory.MemoryCategory)
	}
}
