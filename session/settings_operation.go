package session

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"crude/sessiondb"
	"crude/types"
)

// ErrSettingsSessionClosing rejects settings operations after shutdown starts.
var ErrSettingsSessionClosing = errors.New("session is closing")

// ErrSettingsRevisionConflict means the exact settings preimage changed.
var ErrSettingsRevisionConflict = errors.New("session settings revision changed")

// SettingsSnapshotSet is one detached, atomically captured session projection.
type SettingsSnapshotSet struct {
	Revision  uint64
	Effective types.Settings
	Base      types.Settings
	Overrides map[string]types.CategorySettings
}

// beginSettingsOperation returns with the lifetime fence held on success.
func (s *Session) beginSettingsOperation() error {
	if s == nil || atomic.LoadInt32(&s.closing) != 0 {
		return ErrSettingsSessionClosing
	}
	s.settingsOperationMu.Lock()
	if atomic.LoadInt32(&s.closing) != 0 {
		s.settingsOperationMu.Unlock()
		return ErrSettingsSessionClosing
	}
	return nil
}

func (s *Session) settingsSnapshotSetLocked() SettingsSnapshotSet {
	return SettingsSnapshotSet{
		Revision:  s.settingsRevision,
		Effective: s.settingsSnapshotLocked(),
		Base:      detachedSettingsSnapshot(s.workspaceCfg),
		Overrides: s.sessionOverridesSnapshotLocked(),
	}
}

func detachedSettingsSnapshot(cfg *Config) types.Settings {
	settings := ConfigToSettings(copyConfig(cfg))
	// Memory's category is already a settings value, unlike role categories,
	// so ConfigToSettings otherwise passes its nested references through.
	category := settings.Memory.MemoryCategory
	category.Enabled = cloneBoolPtr(category.Enabled)
	category.AllowedTools = slices.Clone(category.AllowedTools)
	category.DeniedTools = slices.Clone(category.DeniedTools)
	category.SkillModes = maps.Clone(category.SkillModes)
	category.ModelModalities = slices.Clone(category.ModelModalities)
	category.ModelTemperature = cloneFloatPtr(category.ModelTemperature)
	category.ModelTopP = cloneFloatPtr(category.ModelTopP)
	category.ContextCompactionPercent = cloneIntPtr(category.ContextCompactionPercent)
	category.ContextRetentionTokens = cloneIntPtr(category.ContextRetentionTokens)
	settings.Memory.MemoryCategory = category
	return settings
}

// SettingsSnapshots captures the effective settings, baseline and overrides under
// one lock, including their publication revision.
func (s *Session) SettingsSnapshots() SettingsSnapshotSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settingsSnapshotSetLocked()
}

// SaveSettingsSnapshot saves a form and captures all projections before another
// settings mutation can intervene. Failed saves return no success projection.
func (s *Session) SaveSettingsSnapshot(settings types.Settings) (SettingsSnapshotSet, error) {
	return s.saveSettingsSnapshot(settings, nil)
}

// SaveSettingsSnapshotAtRevision atomically checks the preimage and commits it.
func (s *Session) SaveSettingsSnapshotAtRevision(settings types.Settings, expected uint64) (SettingsSnapshotSet, error) {
	return s.saveSettingsSnapshot(settings, &expected)
}

func (s *Session) withSettingsSnapshot(expected *uint64, mutate func() error) (SettingsSnapshotSet, error) {
	if err := s.beginSettingsOperation(); err != nil {
		return SettingsSnapshotSet{}, err
	}
	defer s.settingsOperationMu.Unlock()
	s.delegationMu.Lock()
	defer s.delegationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != nil && s.settingsRevision != *expected {
		return SettingsSnapshotSet{}, ErrSettingsRevisionConflict
	}
	if err := mutate(); err != nil {
		return SettingsSnapshotSet{}, err
	}
	return s.settingsSnapshotSetLocked(), nil
}

func (s *Session) saveSettingsSnapshot(settings types.Settings, expected *uint64) (SettingsSnapshotSet, error) {
	return s.withSettingsSnapshot(expected, func() error { return s.saveSettingsLocked(settings) })
}

// ToggleMultiAgent changes only the current session's delegation flag.
func (s *Session) ToggleMultiAgent() (SettingsSnapshotSet, error) {
	return s.toggleMultiAgent(nil)
}

func (s *Session) ToggleMultiAgentAtRevision(expected uint64) (SettingsSnapshotSet, error) {
	return s.toggleMultiAgent(&expected)
}

func (s *Session) toggleMultiAgent(expected *uint64) (SettingsSnapshotSet, error) {
	return s.withSettingsSnapshot(expected, s.toggleMultiAgentLocked)
}

func (s *Session) toggleMultiAgentLocked() error {
	before := s.cfg.IsMultiAgentEnabled()
	enabled := !before
	if s.db != nil {
		if err := sessiondb.SaveSessionFlags(s.db, s.sessionID, sessiondb.SessionFlags{MultiAgent: &enabled, MainAgent: s.selectedMainAgent}); err != nil {
			return fmt.Errorf("save session delegation: %w", err)
		}
	}
	applySessionMultiAgent(s.cfg, enabled)
	s.multiAgentOverride = &enabled
	s.permissionReviewRevision.Add(1)
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	s.settingsRevision++
	s.recordDelegationChange(before, enabled)
	s.wakeAllRecoveries()
	return nil
}

// ResetSettingsSnapshotAtRevision restores workspace defaults while retaining
// connection pins, exactly like the explicit session reset.
func (s *Session) ResetSettingsSnapshotAtRevision(expected uint64) (SettingsSnapshotSet, error) {
	return s.withSettingsSnapshot(&expected, s.resetSessionSettingsLocked)
}

// ResetSettingsSnapshot is the unconditioned reset entry point.
func (s *Session) ResetSettingsSnapshot() (SettingsSnapshotSet, error) {
	return s.withSettingsSnapshot(nil, s.resetSessionSettingsLocked)
}

// resetSessionSettingsLocked requires settingsOperationMu, delegationMu and mu.
func (s *Session) resetSessionSettingsLocked() error {
	if s.accountUnpinned {
		return fmt.Errorf("select a connection for this session before resetting: %w", ErrAccountRequired)
	}
	oldDelegation := s.cfg.IsMultiAgentEnabled()
	pins := make(map[string]sessiondb.AgentSettingOverride)
	for role, override := range s.sessionOverrides {
		if strings.HasPrefix(role, "root/") {
			continue
		}
		if override.AccountID != "" {
			pins[role] = sessiondb.AgentSettingOverride{AccountID: override.AccountID}
		}
	}
	if _, ok := pins[CategoryDefault]; !ok {
		main := s.cfg.GetActiveDefaultCategory()
		pins[CategoryDefault] = sessiondb.AgentSettingOverride{AccountID: main.AccountID}
	}
	newCfg := configWithSessionOverrides(s.workspaceCfg, pins)
	main := newCfg.GetActiveDefaultCategory()
	configuredProvider, err := newCfg.providerForCategory(main)
	if err != nil {
		return err
	}
	if s.db != nil {
		if err := sessiondb.SaveSessionSettings(s.db, s.sessionID, newCfg.providerIDForAccount(main.AccountID), main.AccountID, pins, sessiondb.SessionFlags{}); err != nil {
			return err
		}
	}
	s.permissionReviewRevision.Add(1)
	s.cfg = newCfg
	s.settingsRevision++
	s.selectedMainAgent = ""
	s.sessionOverrides = pins
	s.multiAgentOverride = nil
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	s.provider = configuredProvider
	s.recordDelegationChange(oldDelegation, s.cfg.IsMultiAgentEnabled())
	s.wakeAllRecoveries()
	return nil
}

// DisabledCapabilities returns a detached, sorted copy of the session-wide
// capability gate. Safe without locks: agents read it at every request.
func (s *Session) DisabledCapabilities() []string {
	if s == nil {
		return nil
	}
	if ids := s.disabledCapabilities.Load(); ids != nil && len(*ids) > 0 {
		return slices.Clone(*ids)
	}
	return nil
}

// SetCapabilityEnabled turns one installed skill or MCP server on or off for
// this session and every agent it runs. Running agents observe the change at
// their next request or tool call; the registration itself is untouched.
func (s *Session) SetCapabilityEnabled(id string, enabled bool) (SettingsSnapshotSet, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SettingsSnapshotSet{}, errors.New("capability id is required")
	}
	return s.withSettingsSnapshot(nil, func() error {
		current := s.DisabledCapabilities()
		next := slices.DeleteFunc(slices.Clone(current), func(value string) bool { return value == id })
		if !enabled {
			next = append(next, id)
			slices.Sort(next)
		}
		if slices.Equal(current, next) {
			return nil
		}
		if s.db != nil {
			if err := sessiondb.SaveDisabledCapabilities(s.db, s.sessionID, next); err != nil {
				return fmt.Errorf("save session capabilities: %w", err)
			}
		}
		if len(next) == 0 {
			next = nil
		}
		s.disabledCapabilities.Store(&next)
		s.settingsRevision++
		return nil
	})
}
