package provider

import (
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
