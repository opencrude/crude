package session

import (
	captypes "crude/types/capability"
	"maps"
	"sync"

	"crude/provider"
	ttypes "crude/types"
)

// RuntimeProfile provides ephemeral per-orchestrator config overrides
// that never persist to disk. Each orchestrator gets its own runtime
// profile that can override models, providers, categories, and other
// settings without interfering with other orchestrators or the base config.
type RuntimeProfile struct {
	mu sync.RWMutex

	// base is the persistent config (never modified by runtime overrides).
	base *Config

	// overrides maps orchestratorID -> per-orchestrator override state.
	overrides map[string]*OrchestratorOverride

	// defaultCategoryOverrides holds any runtime changes that should apply
	// to all orchestrators not having their own explicit override.
	defaultCategoryOverrides map[string]CategoryConfig
}

// OrchestratorOverride holds ephemeral overrides for a single orchestrator.
// All fields are nil/zero when not overridden, meaning "use base config value".
type OrchestratorOverride struct {
	// ProfileOverride is a temporary profile name switch (runtime-only).
	ProfileOverride string

	// MultiAgent overrides the base multi-agent setting.
	MultiAgent *bool

	// TurnLogs overrides the base turn-logs setting.
	TurnLogs *bool

	// CategoryOverrides contains per-category overrides keyed by category name.
	CategoryOverrides map[string]CategoryConfig
}

// NewRuntimeProfile creates a new runtime profile wrapping the given base config.
func NewRuntimeProfile(base *Config) *RuntimeProfile {
	return &RuntimeProfile{
		base:                     base,
		overrides:                make(map[string]*OrchestratorOverride),
		defaultCategoryOverrides: make(map[string]CategoryConfig),
	}
}

// GetOrCreateOverride returns the override state for an orchestrator,
// creating a default one if it doesn't exist.
func (rp *RuntimeProfile) GetOrCreateOverride(orchID string) *OrchestratorOverride {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if oo, ok := rp.overrides[orchID]; ok {
		return oo
	}
	oo := &OrchestratorOverride{
		CategoryOverrides: make(map[string]CategoryConfig),
	}
	rp.overrides[orchID] = oo
	return oo
}

// GetOverride returns the override state for an orchestrator, or nil.
func (rp *RuntimeProfile) GetOverride(orchID string) *OrchestratorOverride {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return rp.overrides[orchID]
}

// RemoveOverride removes the override for an orchestrator (when it closes).
func (rp *RuntimeProfile) RemoveOverride(orchID string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	delete(rp.overrides, orchID)
}

// HasOverride reports whether an orchestrator has any active overrides.
func (rp *RuntimeProfile) HasOverride(orchID string) bool {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	oo, ok := rp.overrides[orchID]
	if !ok {
		return false
	}
	return oo.ProfileOverride != "" ||
		oo.MultiAgent != nil ||
		oo.TurnLogs != nil ||
		len(oo.CategoryOverrides) > 0
}

// SetCategoryOverride sets a category config override for an orchestrator.
// Pass an empty CategoryConfig to clear the override for that category.
func (rp *RuntimeProfile) SetCategoryOverride(orchID, category string, cat CategoryConfig) {
	oo := rp.GetOrCreateOverride(orchID)
	rp.mu.Lock()
	if cat.Model == "" && cat.AccountID == "" && cat.ThinkingEffort == "" && cat.ContextCompactionPercent == nil {
		delete(oo.CategoryOverrides, category)
	} else {
		if oo.CategoryOverrides == nil {
			oo.CategoryOverrides = make(map[string]CategoryConfig)
		}
		oo.CategoryOverrides[category] = cat
	}
	rp.mu.Unlock()
}

// SetOrchestratorModel is a convenience method to set the model for the
// default category of a specific orchestrator.
func (rp *RuntimeProfile) SetOrchestratorModel(orchID, model string) {
	oo := rp.GetOrCreateOverride(orchID)
	rp.mu.Lock()
	if oo.CategoryOverrides == nil {
		oo.CategoryOverrides = make(map[string]CategoryConfig)
	}
	cat := oo.CategoryOverrides[CategoryDefault]
	cat.Model = model
	oo.CategoryOverrides[CategoryDefault] = cat
	rp.mu.Unlock()
}

// SetOrchestratorAccount selects the account for one orchestrator's main role.
func (rp *RuntimeProfile) SetOrchestratorAccount(orchID, accountID string) {
	oo := rp.GetOrCreateOverride(orchID)
	rp.mu.Lock()
	if oo.CategoryOverrides == nil {
		oo.CategoryOverrides = make(map[string]CategoryConfig)
	}
	cat := oo.CategoryOverrides[CategoryDefault]
	cat.AccountID = accountID
	oo.CategoryOverrides[CategoryDefault] = cat
	rp.mu.Unlock()
}

// GetEffectiveConfig returns a merged Config for a given orchestrator.
// The base config is deep-copied, then any orchestrator overrides are applied.
// The returned Config is safe to use for a single turn.
func (rp *RuntimeProfile) GetEffectiveConfig(orchID string) *Config {
	rp.mu.RLock()
	base := rp.base
	oo := rp.overrides[orchID]
	rp.mu.RUnlock()

	// Deep-copy the base config.
	cfg := copyConfig(base)

	if oo == nil {
		return cfg
	}

	rp.mu.RLock()
	defer rp.mu.RUnlock()

	_ = rp.applyOverrideToCategories(cfg, oo)
	return cfg
}

// applyOverrideToCategories merges orchestrator overrides into a config's
// active profile categories.
func (rp *RuntimeProfile) applyOverrideToCategories(cfg *Config, oo *OrchestratorOverride) *Config {
	ap := cfg.Agents.ActiveProfile
	if oo.ProfileOverride != "" {
		ap = oo.ProfileOverride
	}
	cfg.Agents.ActiveProfile = ap

	prof, found := cfg.Agents.Profiles[ap]
	if !found {
		return cfg
	}

	if oo.MultiAgent != nil {
		prof.MultiAgent = oo.MultiAgent
		cfg.Agents.Enabled = *oo.MultiAgent
	}
	if oo.TurnLogs != nil {
		prof.TurnLogs = oo.TurnLogs
		cfg.TurnLogsEnabled = *oo.TurnLogs
	}

	for catName, overrideCat := range oo.CategoryOverrides {
		existing, exists := prof.Categories[catName]
		if !exists {
			existing = prof.Categories[CategoryDefault]
		}
		merged := mergeCategoryConfig(existing, overrideCat)
		if prof.Categories == nil {
			prof.Categories = make(map[string]CategoryConfig)
		}
		prof.Categories[catName] = merged
	}

	cfg.Agents.Profiles[ap] = prof
	return cfg
}

// mergeCategoryConfig merges override values into base, with override taking
// priority. Only non-zero/non-empty override fields replace the base.
// API keys are never a category property and are cleared on the result.
func mergeCategoryConfig(base, override CategoryConfig) CategoryConfig {
	merged := base
	if override.AccountID != "" {
		merged.AccountID = override.AccountID
	}
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.ThinkingEffort != "" {
		merged.ThinkingEffort = override.ThinkingEffort
	}
	if override.Policy != "" {
		merged.Policy = override.Policy
	}
	if override.Description != "" {
		merged.Description = override.Description
	}
	if override.Instructions != "" {
		merged.Instructions = override.Instructions
	}
	if len(override.AllowedTools) > 0 {
		merged.AllowedTools = override.AllowedTools
	}
	if len(override.DeniedTools) > 0 {
		merged.DeniedTools = override.DeniedTools
	}
	if override.SkillModes != nil {
		merged.SkillModes = maps.Clone(base.SkillModes)
		if merged.SkillModes == nil {
			merged.SkillModes = make(map[string]string)
		}
		maps.Copy(merged.SkillModes, override.SkillModes)
	}
	if override.ModelContextWindow > 0 {
		merged.ModelContextWindow = override.ModelContextWindow
	}
	if override.ModelInputPrice > 0 {
		merged.ModelInputPrice = override.ModelInputPrice
	}
	if override.ModelOutputPrice > 0 {
		merged.ModelOutputPrice = override.ModelOutputPrice
	}
	if override.ModelCachePrice > 0 {
		merged.ModelCachePrice = override.ModelCachePrice
	}
	merged.ModelModalities = provider.UnionModalities(merged.ModelModalities, override.ModelModalities)
	if override.ModelSupportsTools {
		merged.ModelSupportsTools = true
	}
	if override.ModelMaxOutput > 0 {
		merged.ModelMaxOutput = override.ModelMaxOutput
	}
	if override.ModelTemperature != nil {
		temperature := *override.ModelTemperature
		merged.ModelTemperature = &temperature
	}
	if override.ModelTopP != nil {
		topP := *override.ModelTopP
		merged.ModelTopP = &topP
	}
	if override.ContextCompactionPercent != nil {
		value := *override.ContextCompactionPercent
		merged.ContextCompactionPercent = &value
	}
	return merged
}

// copyConfig creates a deep copy of a Config.
func copyConfig(src *Config) *Config {
	dst := &Config{
		SelectedAgent:   src.SelectedAgent,
		AccountManager:  src.AccountManager,
		Accounts:        src.Accounts,
		TurnLogsEnabled: src.TurnLogsEnabled,
		Memory:          src.Memory,
		Capabilities:    captypes.Clone(src.Capabilities),
		Safety:          src.Safety,
	}
	if src.selectedMainOverride != nil {
		override := *src.selectedMainOverride
		override.ContextCompactionPercent = cloneIntPtr(override.ContextCompactionPercent)
		dst.selectedMainOverride = &override
	}
	if src.Accounts != nil {
		dst.Accounts = make(map[string]provider.Account, len(src.Accounts))
		for id, account := range src.Accounts {
			dst.Accounts[id] = provider.CloneAccount(account)
		}
	}
	dst.Safety.CommandRules = append([]ttypes.CommandSeverityRule(nil), src.Safety.CommandRules...)
	dst.Memory.MemoryCategory.ContextCompactionPercent = cloneIntPtr(src.Memory.MemoryCategory.ContextCompactionPercent)

	if src.workspaceMain != nil {
		m := *src.workspaceMain
		m.ContextCompactionPercent = cloneIntPtr(src.workspaceMain.ContextCompactionPercent)
		dst.workspaceMain = &m
	}

	agents := src.Agents
	if src.Agents.Profiles != nil {
		agents.Profiles = make(map[string]ProfileConfig, len(src.Agents.Profiles))
		for name, prof := range src.Agents.Profiles {
			profCopy := ProfileConfig{
				MultiAgent: cloneBoolPtr(prof.MultiAgent),
				TurnLogs:   cloneBoolPtr(prof.TurnLogs),
			}
			if prof.Categories != nil {
				profCopy.Categories = make(map[string]CategoryConfig, len(prof.Categories))
				for k, v := range prof.Categories {
					v.Enabled = cloneBoolPtr(v.Enabled)
					v.AllowedTools = append([]string(nil), v.AllowedTools...)
					v.DeniedTools = append([]string(nil), v.DeniedTools...)
					v.SkillModes = maps.Clone(v.SkillModes)
					v.ContextCompactionPercent = cloneIntPtr(v.ContextCompactionPercent)
					v.ModelModalities = append([]string(nil), v.ModelModalities...)
					v.ModelTemperature = cloneFloatPtr(v.ModelTemperature)
					v.ModelTopP = cloneFloatPtr(v.ModelTopP)
					profCopy.Categories[k] = v
				}
			}
			agents.Profiles[name] = profCopy
		}
	}
	agents.AgentProfiles = ttypes.CloneAgentProfiles(src.Agents.AgentProfiles)
	dst.Agents = agents

	return dst
}

func cloneFloatPtr(src *float64) *float64 {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

// Snapshot creates an immutable snapshot of the overrides for a specific
// orchestrator, used for a single turn.
type OverrideSnapshot struct {
	CategoryOverrides map[string]CategoryConfig
	MultiAgent        *bool
	TurnLogs          *bool
}

// SnapshotOverrides returns a snapshot of overrides for a given orchestrator.
// Returns nil if no overrides exist.
func (rp *RuntimeProfile) SnapshotOverrides(orchID string) *OverrideSnapshot {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	oo, ok := rp.overrides[orchID]
	if !ok {
		return nil
	}

	snap := &OverrideSnapshot{}
	if oo.MultiAgent != nil {
		snap.MultiAgent = oo.MultiAgent
	}
	if oo.TurnLogs != nil {
		snap.TurnLogs = oo.TurnLogs
	}
	if len(oo.CategoryOverrides) > 0 {
		snap.CategoryOverrides = make(map[string]CategoryConfig, len(oo.CategoryOverrides))
		for k, v := range oo.CategoryOverrides {
			snap.CategoryOverrides[k] = v
		}
	}
	return snap
}

// Reset clears all runtime overrides for all orchestrators.
func (rp *RuntimeProfile) Reset() {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.overrides = make(map[string]*OrchestratorOverride)
	rp.defaultCategoryOverrides = make(map[string]CategoryConfig)
}

// UpdateBase replaces the base config reference. Called when settings are
// persisted so subsequent effective configs reflect the new base.
func (rp *RuntimeProfile) UpdateBase(base *Config) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.base = base
}

// AllOrchestratorIDs returns a copy of all tracked orchestrator IDs.
func (rp *RuntimeProfile) AllOrchestratorIDs() []string {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	ids := make([]string, 0, len(rp.overrides))
	for id := range rp.overrides {
		ids = append(ids, id)
	}
	return ids
}
