package session

import (
	"crude/provider"
	"testing"
)

func TestNewRuntimeProfile(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)
	if rp == nil {
		t.Fatal("NewRuntimeProfile returned nil")
	}
	if rp.base != base {
		t.Error("base pointer mismatch")
	}
	if len(rp.overrides) != 0 {
		t.Error("expected empty overrides map")
	}
}

func TestGetOrCreateOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	oo := rp.GetOrCreateOverride("orch-1")
	if oo == nil {
		t.Fatal("expected non-nil override")
	}
	if oo.CategoryOverrides == nil {
		t.Error("expected non-nil CategoryOverrides map")
	}

	// Calling again should return the same pointer.
	oo2 := rp.GetOrCreateOverride("orch-1")
	if oo != oo2 {
		t.Error("expected same pointer for same orchestrator")
	}
}

func TestSetAndGetOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	// Set a model override for the default category.
	rp.SetOrchestratorModel("orch-1", "gpt-4")

	oo := rp.GetOverride("orch-1")
	if oo == nil {
		t.Fatal("expected override")
	}
	cat, ok := oo.CategoryOverrides[CategoryDefault]
	if !ok {
		t.Fatal("expected default category override")
	}
	if cat.Model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", cat.Model)
	}
}

func TestRemoveOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	rp.GetOrCreateOverride("orch-1")
	rp.SetOrchestratorModel("orch-1", "gpt-4")

	if !rp.HasOverride("orch-1") {
		t.Error("expected override before removal")
	}

	rp.RemoveOverride("orch-1")
	if rp.HasOverride("orch-1") {
		t.Error("expected no override after removal")
	}
}

func TestGetEffectiveConfigNoOverrides(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	cfg := rp.GetEffectiveConfig("orch-nonexistent")
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.providerIDForAccount("account") != base.providerIDForAccount("account") {
		t.Errorf("expected account provider attribution to be preserved")
	}
}

func TestGetEffectiveConfigWithModelOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	rp.SetOrchestratorModel("orch-1", "gpt-4-turbo")

	cfg := rp.GetEffectiveConfig("orch-1")
	mainCat := cfg.GetActiveDefaultCategory()
	if mainCat.Model != "gpt-4-turbo" {
		t.Errorf("expected model 'gpt-4-turbo', got %q", mainCat.Model)
	}
	if mainCat.AccountID != base.GetActiveDefaultCategory().AccountID {
		t.Errorf("model override changed selected account: %q", mainCat.AccountID)
	}

	// Verify base config was not modified.
	baseMain := base.GetActiveDefaultCategory()
	if baseMain.Model == "gpt-4-turbo" {
		t.Error("base config should NOT have the overridden model")
	}
}

func TestMultipleOrchestratorsIndependent(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	rp.SetOrchestratorModel("orch-1", "gpt-4")
	rp.SetOrchestratorModel("orch-2", "claude-3")

	cfg1 := rp.GetEffectiveConfig("orch-1")
	cfg2 := rp.GetEffectiveConfig("orch-2")

	if cfg1.GetActiveDefaultCategory().Model != "gpt-4" {
		t.Errorf("orch-1 expected 'gpt-4', got %q", cfg1.GetActiveDefaultCategory().Model)
	}
	if cfg2.GetActiveDefaultCategory().Model != "claude-3" {
		t.Errorf("orch-2 expected 'claude-3', got %q", cfg2.GetActiveDefaultCategory().Model)
	}
}

func TestSnapshotOverrides(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	// No overrides should return nil.
	if snap := rp.SnapshotOverrides("orch-1"); snap != nil {
		t.Error("expected nil snapshot for non-existent orchestrator")
	}

	rp.SetOrchestratorModel("orch-1", "gpt-4")
	multi := true
	oo := rp.GetOrCreateOverride("orch-1")
	oo.MultiAgent = &multi

	snap := rp.SnapshotOverrides("orch-1")
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.MultiAgent == nil || !*snap.MultiAgent {
		t.Error("expected MultiAgent=true in snapshot")
	}
	cat, ok := snap.CategoryOverrides[CategoryDefault]
	if !ok {
		t.Fatal("expected default category in snapshot")
	}
	if cat.Model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", cat.Model)
	}
}

func TestMergeCategoryConfig(t *testing.T) {
	base := CategoryConfig{
		Model: "base-model", AccountID: "base-account",
	}
	override := CategoryConfig{
		Model: "override-model",
	}

	merged := mergeCategoryConfig(base, override)
	if merged.Model != "override-model" {
		t.Errorf("expected model 'override-model', got %q", merged.Model)
	}
	if merged.AccountID != "base-account" {
		t.Errorf("expected original account, got %q", merged.AccountID)
	}
}

func TestMergeCategoryConfigAllowsDisablingAutomaticCompaction(t *testing.T) {
	baseValue, disabled := 85, 0
	merged := mergeCategoryConfig(
		CategoryConfig{ContextCompactionPercent: &baseValue},
		CategoryConfig{ContextCompactionPercent: &disabled},
	)
	if merged.ContextCompactionPercent == nil || *merged.ContextCompactionPercent != 0 {
		t.Fatalf("compaction override = %#v", merged.ContextCompactionPercent)
	}
}

func TestSetCategoryOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	cat := CategoryConfig{Model: "custom-model", AccountID: "account"}
	rp.SetCategoryOverride("orch-1", CategoryResearcher, cat)

	cfg := rp.GetEffectiveConfig("orch-1")
	researcherCat := cfg.GetCategoryConfig(CategoryResearcher)
	if researcherCat.Model != "custom-model" {
		t.Errorf("expected researcher model 'custom-model', got %q", researcherCat.Model)
	}
}

func TestClearCategoryOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	cat := CategoryConfig{Model: "custom-model"}
	rp.SetCategoryOverride("orch-1", CategoryResearcher, cat)

	if !rp.HasOverride("orch-1") {
		t.Error("expected override after set")
	}

	// Clear by passing empty.
	rp.SetCategoryOverride("orch-1", CategoryResearcher, CategoryConfig{})

	// The override map entry should be gone.
	oo := rp.GetOverride("orch-1")
	if _, exists := oo.CategoryOverrides[CategoryResearcher]; exists {
		t.Error("expected category override to be cleared")
	}
}

func TestCopyConfigDeepCopy(t *testing.T) {
	base := DefaultConfig()
	base.Accounts = map[string]provider.Account{"account": {ID: "account", ProviderID: "openrouter", Connection: provider.Template{Name: "OpenRouter", ProviderURL: "https://openrouter.ai", APIStandard: "openai"}}}

	copied := copyConfig(base)
	// Verify deep copy: modifying copy should not affect original.
	account := copied.Accounts["account"]
	account.Connection.Name = "Modified"
	copied.Accounts["account"] = account
	if base.Accounts["account"].Connection.Name == "Modified" {
		t.Error("account connection was not deep-copied")
	}
}

func TestReset(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	rp.SetOrchestratorModel("orch-1", "gpt-4")
	rp.SetOrchestratorModel("orch-2", "claude-3")

	rp.Reset()
	if rp.HasOverride("orch-1") {
		t.Error("expected no override after reset")
	}
	if rp.HasOverride("orch-2") {
		t.Error("expected no override after reset")
	}
	if len(rp.AllOrchestratorIDs()) != 0 {
		t.Error("expected empty orchestrator list after reset")
	}
}

func TestAllOrchestratorIDs(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	rp.GetOrCreateOverride("orch-1")
	rp.GetOrCreateOverride("orch-2")

	ids := rp.AllOrchestratorIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %d", len(ids))
	}
}

func DefaultConfig() *Config {
	agents := DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	main := profile.Categories[CategoryDefault]
	main.AccountID = "account"
	profile.Categories[CategoryDefault] = main
	agents.Profiles[agents.ActiveProfile] = profile
	return &Config{
		Accounts:        map[string]provider.Account{"account": {ID: "account", ProviderID: "openrouter", Connection: provider.DefaultTemplates()["openrouter"]}},
		TurnLogsEnabled: true,
		Agents:          agents,
	}
}

func TestGetEffectiveConfigPreservesBaseAfterOverride(t *testing.T) {
	base := DefaultConfig()
	rp := NewRuntimeProfile(base)

	// Set override on orch-1.
	rp.SetOrchestratorModel("orch-1", "custom-model")

	// Get effective config for orch-1.
	cfg1 := rp.GetEffectiveConfig("orch-1")
	if cfg1.GetActiveDefaultCategory().Model != "custom-model" {
		t.Error("orch-1 should use custom model")
	}

	// Get effective config for nonexistent orchestrator should use base.
	cfg2 := rp.GetEffectiveConfig("orch-nonexistent")
	if cfg2.GetActiveDefaultCategory().Model == "custom-model" {
		t.Error("nonexistent orchestrator should not have custom model")
	}
}
