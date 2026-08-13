package provider

import (
	"encoding/json"
	"testing"
)

func TestPresetList_NotEmpty(t *testing.T) {
	list := PresetList()
	if len(list) == 0 {
		t.Fatal("PresetList returned empty slice")
	}
}

func TestPresetList_ImmutableCopy(t *testing.T) {
	list1 := PresetList()
	list2 := PresetList()
	if len(list1) != len(list2) {
		t.Fatal("PresetList returned different lengths")
	}
	list1[0].Name = "modified"
	if list2[0].Name == "modified" {
		t.Fatal("PresetList returned shared mutable slice")
	}
	list1[0].ExtraBody["mutated"] = true
	if _, exists := list2[0].ExtraBody["mutated"]; exists {
		t.Fatal("PresetList returned shared ExtraBody map")
	}
}

func TestPresetList_EveryPresetHasID(t *testing.T) {
	for i, p := range PresetList() {
		if p.ID == "" {
			t.Errorf("preset at index %d has empty ID", i)
		}
	}
}

func TestPresetList_EveryPresetHasName(t *testing.T) {
	for i, p := range PresetList() {
		if p.Name == "" {
			t.Errorf("preset at index %d (%q) has empty Name", i, p.ID)
		}
	}
}

func TestPresetList_EveryNonCustomPresetHasBaseURL(t *testing.T) {
	for _, p := range PresetList() {
		if p.ID == "custom" {
			continue
		}
		if p.BaseURL == "" {
			t.Errorf("preset %q has empty BaseURL", p.ID)
		}
	}
}

func TestGetPreset_Found(t *testing.T) {
	tests := []struct {
		id   string
		name string
	}{
		{"openrouter", "OpenRouter"},
		{"openai", "OpenAI"},
		{"anthropic", "Anthropic"},
		{"google", "Google AI"},
		{"groq", "Groq"},
		{"custom", "Custom"},
	}

	for _, tt := range tests {
		p := GetPreset(tt.id)
		if p == nil {
			t.Errorf("GetPreset(%q) returned nil", tt.id)
			continue
		}
		if p.Name != tt.name {
			t.Errorf("GetPreset(%q).Name = %q, want %q", tt.id, p.Name, tt.name)
		}
	}
}

func TestGetPreset_NotFound(t *testing.T) {
	p := GetPreset("nonexistent")
	if p != nil {
		t.Errorf("GetPreset(nonexistent) = %v, want nil", p)
	}
}

func TestFromPreset_Valid(t *testing.T) {
	p := FromPreset("openrouter", "sk-test", "")
	if p == nil {
		t.Fatal("FromPreset returned nil")
	}
	if p.ID != "openrouter" {
		t.Errorf("ID = %q, want openrouter", p.ID)
	}
	if p.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, want sk-test", p.APIKey)
	}
	if p.BaseURL == "" {
		t.Error("BaseURL is empty")
	}
}

func TestNativePresetStandards(t *testing.T) {
	openAI := FromPreset("openai", "sk-test", "")
	if got := openAI.StandardID(); got != "openai-responses" {
		t.Fatalf("OpenAI standard = %q", got)
	}
	if got := openAI.ChatEndpoint(); got != "https://api.openai.com/v1/responses" {
		t.Fatalf("OpenAI endpoint = %q", got)
	}

	anthropic := FromPreset("anthropic", "sk-test", "")
	if got := anthropic.StandardID(); got != "anthropic" {
		t.Fatalf("Anthropic standard = %q", got)
	}
	if got := anthropic.ChatEndpoint(); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("Anthropic endpoint = %q", got)
	}

	legacy := FromPreset("openai-chat", "sk-test", "")
	if got := legacy.StandardID(); got != "openai" {
		t.Fatalf("legacy OpenAI standard = %q", got)
	}
}

func TestProviderJSONRoundTripPreservesStandard(t *testing.T) {
	for _, presetID := range []string{"openai", "anthropic", "openai-chat"} {
		original := FromPreset(presetID, "key", "")
		encoded, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("%s marshal: %v", presetID, err)
		}
		var restored Provider
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatalf("%s unmarshal: %v", presetID, err)
		}
		if restored.StandardID() != original.StandardID() {
			t.Fatalf("%s standard = %q, want %q", presetID, restored.StandardID(), original.StandardID())
		}
		if restored.ChatEndpoint() != original.ChatEndpoint() {
			t.Fatalf("%s endpoint = %q, want %q", presetID, restored.ChatEndpoint(), original.ChatEndpoint())
		}
	}
}

func TestProviderInfersStandardForLegacyJSON(t *testing.T) {
	var restored Provider
	if err := json.Unmarshal([]byte(`{
		"id":"anthropic","base_url":"https://api.anthropic.com/v1"
	}`), &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.StandardID() != "anthropic" {
		t.Fatalf("standard = %q", restored.StandardID())
	}
	if restored.NewStreamDecoder() == nil {
		t.Fatal("provider returned nil stream decoder")
	}
}

func TestProviderStandardCanOverridePreset(t *testing.T) {
	p := FromPreset("openai", "key", "")
	if err := p.SetStandardID("anthropic"); err != nil {
		t.Fatalf("SetStandardID: %v", err)
	}
	if p.StandardID() != "anthropic" {
		t.Fatalf("standard = %q", p.StandardID())
	}
	if got := p.ChatEndpoint(); got != "https://api.openai.com/v1/messages" {
		t.Fatalf("endpoint = %q", got)
	}
	if err := p.SetStandardID("not-a-standard"); err == nil {
		t.Fatal("invalid standard was accepted")
	}
}

func TestDefaultStandardIDUsesPresetProtocol(t *testing.T) {
	tests := map[string]string{
		"openai": "openai-responses", "anthropic": "anthropic",
		"openrouter": "openai", "custom": "openai",
	}
	for providerID, want := range tests {
		if got := DefaultStandardID(providerID); got != want {
			t.Errorf("%s standard = %q, want %q", providerID, got, want)
		}
	}
}

func TestProviderExtraBodyIsDeeplyIsolated(t *testing.T) {
	input := map[string]any{
		"reasoning": map[string]any{"effort": "high"},
		"include":   []any{"reasoning.encrypted_content"},
	}
	p := FromPreset("openai", "sk-test", "")
	p.SetExtraBody(input)
	input["reasoning"].(map[string]any)["effort"] = "low"
	input["include"].([]any)[0] = "mutated"

	first := p.BuildExtraBody()
	if got := first["reasoning"].(map[string]any)["effort"]; got != "high" {
		t.Fatalf("stored nested map was mutated: %v", got)
	}
	if got := first["include"].([]any)[0]; got != "reasoning.encrypted_content" {
		t.Fatalf("stored nested slice was mutated: %v", got)
	}
	first["reasoning"].(map[string]any)["effort"] = "low"
	if got := p.BuildExtraBody()["reasoning"].(map[string]any)["effort"]; got != "high" {
		t.Fatalf("BuildExtraBody returned shared nested state: %v", got)
	}
}

func TestFromPreset_UnknownFallsBackToOpenRouter(t *testing.T) {
	p := FromPreset("nonexistent", "sk-test", "")
	if p == nil {
		t.Fatal("FromPreset returned nil for unknown preset")
	}
	if p.ID != "openrouter" {
		t.Errorf("ID = %q, want openrouter (fallback)", p.ID)
	}
}

func TestFromPreset_CustomBaseURL(t *testing.T) {
	p := FromPreset("openrouter", "sk-test", "https://custom.example.com/v1")
	if p.BaseURL != "https://custom.example.com/v1" {
		t.Errorf("BaseURL = %q, want custom URL", p.BaseURL)
	}
}

func TestFromPreset_CustomEmptyURL(t *testing.T) {
	p := FromPreset("custom", "no-key", "")
	if p == nil {
		t.Fatal("FromPreset returned nil")
	}
	if p.ID != "custom" {
		t.Errorf("ID = %q, want custom", p.ID)
	}
}

func TestPresetNames(t *testing.T) {
	names := PresetNames()
	if len(names) != len(PresetList()) {
		t.Errorf("PresetNames len = %d, PresetList len = %d", len(names), len(PresetList()))
	}
	for i, name := range names {
		if name == "" {
			t.Errorf("PresetNames[%d] is empty", i)
		}
	}
}

func TestPresetIDs(t *testing.T) {
	ids := PresetIDs()
	if len(ids) != len(PresetList()) {
		t.Errorf("PresetIDs len = %d, PresetList len = %d", len(ids), len(PresetList()))
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if id == "" {
			t.Error("PresetIDs contains empty string")
		}
		if seen[id] {
			t.Errorf("duplicate ID %q in PresetIDs", id)
		}
		seen[id] = true
	}
}

func TestPresetIDs_ContainsKnown(t *testing.T) {
	ids := PresetIDs()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	for _, want := range []string{"openrouter", "openai", "anthropic", "custom"} {
		if !set[want] {
			t.Errorf("PresetIDs missing %q", want)
		}
	}
}
