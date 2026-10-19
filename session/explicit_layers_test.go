package session

import (
	"crude/sessiondb"
	"crude/types"
	"testing"
)

func TestSessionFieldEditsSurviveParentAndUnrelatedLocalChanges(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	snap := s.SettingsSnapshots()
	draft := snap.Effective
	draft.SessionOverrides = snap.Overrides
	base := types.SessionPreferencesOf(draft)
	code := false
	draft.SessionCodeIntelligence = &code
	draft.SessionEdits = types.SettingsEdits(base, types.SessionPreferencesOf(draft))
	if _, err := s.ToggleMultiAgent(); err != nil {
		t.Fatal(err)
	}
	s.UpdateWorkspaceDefaults(types.AppSettings{Accounts: s.cfg.Accounts, Agents: snap.Base.Agents})
	got, err := s.SaveSettingsSnapshot(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.Effective.SessionCodeIntelligence == nil || *got.Effective.SessionCodeIntelligence || got.Effective.Agents.Enabled {
		t.Fatal("field edit lost unrelated live change")
	}
}

func TestSessionStaleSelectionLeafKeepsValidNeighbors(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	if _, err := s.db.Exec(`INSERT INTO session_agent_settings(session_id,category,model,capability_selection) VALUES(?,?,?,?)`, s.sessionID, "crafter", "kept-model", `{"AllowedTools":"obsolete","context_retention_tokens":32000,"service_tier":"fast"}`); err != nil {
		t.Fatal(err)
	}
	got, err := sessiondb.LoadAgentSettingOverrides(s.db, s.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got["crafter"].Model != "kept-model" || got["crafter"].AllowedTools != nil || got["crafter"].ContextRetentionTokens == nil || got["crafter"].ServiceTier != "fast" {
		t.Fatal("stale leaf poisoned valid neighboring overrides")
	}
}

func TestSessionRemovingLastExplicitRoleDoesNotRepinEffectiveValues(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	settings := s.SettingsSnapshot()
	settings.SessionOverrides = map[string]types.CategorySettings{"default": {Model: "local-model"}}
	if _, err := s.SaveSettingsSnapshot(settings); err != nil {
		t.Fatal(err)
	}
	settings = s.SettingsSnapshot()
	settings.SessionEdits = []types.SettingEdit{{Path: []string{"overrides", "default"}}}
	got, err := s.SaveSettingsSnapshot(settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Overrides) != 0 || got.Effective.Model == "local-model" {
		t.Fatal("removing last override repinned resolved view")
	}
	stored, err := sessiondb.LoadAgentSettingOverrides(s.db, s.sessionID)
	if err != nil || len(stored) != 0 {
		t.Fatal("reset did not remove stored entries", err)
	}
}
