package provider

import (
	"encoding/json"
	"testing"
)

func TestCodexDecodeModelsContextWindows(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"models": []map[string]any{
			{"slug": "gpt-5.6-codex", "display_name": "GPT-5.6 Codex", "context_window": 1000000},
			{"slug": "gpt-5-codex", "display_name": "GPT-5 Codex", "context_window": 400000},
			{"slug": "gpt-5.4-codex", "display_name": "GPT-5.4 Codex", "context_window": 272000},
		},
	})
	std := &CodexStandard{}
	page, err := std.DecodeModelsPage(body)
	models := page.Models
	if err != nil {
		t.Fatalf("DecodeModels: %v", err)
	}
	got := map[string]int{}
	for _, m := range models {
		got[m.ID] = m.ContextWindow
	}
	if got["gpt-5.6-codex"] != 1000000 {
		t.Errorf("gpt-5.6-codex ctx = %d, want 1000000", got["gpt-5.6-codex"])
	}
	if got["gpt-5-codex"] != 400000 {
		t.Errorf("gpt-5-codex ctx = %d, want 400000", got["gpt-5-codex"])
	}
	if got["gpt-5.4-codex"] != 272000 {
		t.Errorf("gpt-5.4-codex ctx = %d, want 272000", got["gpt-5.4-codex"])
	}
}
