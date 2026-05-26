package provider

import (
	"sync"
	"testing"
)

// resetDynamicCaps clears the dynamicCaps map for test isolation.
func resetDynamicCaps() {
	dynamicCapsMu.Lock()
	dynamicCaps = make(map[string]modelCap)
	dynamicCapsMu.Unlock()
}

func TestSupportsVision_DynamicCaps(t *testing.T) {
	resetDynamicCaps()

	// Before loading, unknown models return false
	if SupportsVision("gpt-4o") {
		t.Error("SupportsVision should return false before dynamicCaps is populated")
	}

	// Load some capabilities
	LoadModelCaps(map[string]ModelMeta{
		"gpt-4o":            {SupportsVision: true, SupportsTools: true},
		"claude-3-5-sonnet": {SupportsVision: true, SupportsTools: true},
		"deepseek-chat":     {SupportsVision: false, SupportsTools: true},
		"o1-mini":           {SupportsVision: false, SupportsTools: false},
	})

	// Vision models
	for _, m := range []string{"gpt-4o", "claude-3-5-sonnet"} {
		if !SupportsVision(m) {
			t.Errorf("SupportsVision(%q): expected true after LoadModelCaps", m)
		}
	}

	// Non-vision models
	for _, m := range []string{"deepseek-chat", "o1-mini"} {
		if SupportsVision(m) {
			t.Errorf("SupportsVision(%q): expected false after LoadModelCaps", m)
		}
	}
}

func TestSupportsVision_ProviderPrefix(t *testing.T) {
	resetDynamicCaps()
	LoadModelCaps(map[string]ModelMeta{
		"gpt-4o": {SupportsVision: true, SupportsTools: true},
	})

	// Should strip provider prefix before lookup
	if !SupportsVision("openai/gpt-4o") {
		t.Error("SupportsVision should strip 'openai/' prefix")
	}
}

func TestSupportsVision_UnknownModel(t *testing.T) {
	resetDynamicCaps()

	// Unknown models default to false
	if SupportsVision("totally-unknown-model-xyz") {
		t.Error("SupportsVision should default to false for unknown models")
	}
}

func TestSupportsVision_CaseInsensitive(t *testing.T) {
	resetDynamicCaps()
	LoadModelCaps(map[string]ModelMeta{
		"gpt-4o": {SupportsVision: true},
	})

	if !SupportsVision("GPT-4O") {
		t.Error("SupportsVision should be case-insensitive")
	}
	if !SupportsVision("Openai/GPT-4O") {
		t.Error("SupportsVision should be case-insensitive with prefix")
	}
}

func TestSupportsVision_NoFuzzyMatch(t *testing.T) {
	resetDynamicCaps()

	// Fuzzy heuristics are removed — "vision" substring should NOT match
	if SupportsVision("my-custom-vision-model") {
		t.Error("SupportsVision should NOT use fuzzy 'vision' substring matching")
	}
	if SupportsVision("some-model-vl-large") {
		t.Error("SupportsVision should NOT use fuzzy '-vl-' pattern matching")
	}
}

func TestSupportsTools_DynamicCaps(t *testing.T) {
	resetDynamicCaps()

	// Before loading, unknown models default to true
	if !SupportsTools("totally-unknown-model-xyz") {
		t.Error("SupportsTools should default to true for unknown models")
	}

	// Load capabilities
	LoadModelCaps(map[string]ModelMeta{
		"gpt-4o":            {SupportsVision: true, SupportsTools: true},
		"claude-3-5-sonnet": {SupportsVision: true, SupportsTools: true},
		"o1-mini":           {SupportsVision: false, SupportsTools: false},
		"deepseek-reasoner": {SupportsVision: false, SupportsTools: false},
	})

	// Tool-supporting models
	for _, m := range []string{"gpt-4o", "claude-3-5-sonnet"} {
		if !SupportsTools(m) {
			t.Errorf("SupportsTools(%q): expected true after LoadModelCaps", m)
		}
	}

	// Non-tool models
	for _, m := range []string{"o1-mini", "deepseek-reasoner"} {
		if SupportsTools(m) {
			t.Errorf("SupportsTools(%q): expected false after LoadModelCaps", m)
		}
	}
}

func TestSupportsTools_ProviderPrefix(t *testing.T) {
	resetDynamicCaps()
	LoadModelCaps(map[string]ModelMeta{
		"gpt-4o": {SupportsTools: true},
	})

	if !SupportsTools("openai/gpt-4o") {
		t.Error("SupportsTools should strip provider prefix")
	}
}

func TestSetModelCap(t *testing.T) {
	resetDynamicCaps()

	SetModelCap("test-model", true, false)

	if !SupportsVision("test-model") {
		t.Error("SetModelCap should set vision capability")
	}
	if SupportsTools("test-model") {
		t.Error("SetModelCap should not set tools capability when false")
	}
}

func TestLoadModelCaps_Overwrite(t *testing.T) {
	resetDynamicCaps()

	SetModelCap("test-model", true, true)
	LoadModelCaps(map[string]ModelMeta{
		"test-model": {SupportsVision: false, SupportsTools: false},
	})

	if SupportsVision("test-model") {
		t.Error("LoadModelCaps should overwrite existing entries")
	}
	if SupportsTools("test-model") {
		t.Error("LoadModelCaps should overwrite existing entries")
	}
}

func TestSupportsVision_ConcurrentAccess(t *testing.T) {
	resetDynamicCaps()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			SupportsVision("model")
		}()
		go func(id int) {
			defer wg.Done()
			SetModelCap("model", id%2 == 0, id%2 == 1)
		}(i)
	}
	wg.Wait()
}
