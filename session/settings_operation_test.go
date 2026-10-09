package session

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

func newSettingsOperationSession(t *testing.T, enabled bool) *Session {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig()
	applySessionMultiAgent(cfg, enabled)
	cfg.Agents.AgentProfiles["reviewer"] = AgentProfileConfig{Category: CategoryResearcher, AccountID: "account", Model: "review-model"}
	s, err := NewWithSessionDir("", cfg, root, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// Preserve the DB through the real shutdown path without running a provider.
	s.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "retain settings")}
	return s
}

func TestToggleMultiAgentPreservesCurrentSessionSettings(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(map[bool]string{false: "inherited-off", true: "inherited-on"}[inherited], func(t *testing.T) {
			s := newSettingsOperationSession(t, inherited)
			settings := s.SettingsSnapshot()
			settings.SessionMainAgent = "profile/reviewer"
			settings.TurnLogsEnabled = "off"
			percent := 61
			settings.SessionOverrides = map[string]types.CategorySettings{
				CategoryDefault:         {AccountID: "account", Model: "current-main", DeniedTools: []string{"write_file"}},
				CategoryCrafter:         {AccountID: "account", Model: "current-crafter", ThinkingEffort: "high", ContextCompactionPercent: &percent, AllowedTools: []string{"read_file"}, SkillModes: map[string]string{"skill": "manual"}},
				"root/profile/reviewer": {AccountID: "account", DeniedTools: []string{"execute_command"}},
			}
			before, err := s.SaveSettingsSnapshot(settings)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ToggleMultiAgent()
			if err != nil {
				t.Fatal(err)
			}
			if got.Revision <= before.Revision || !reflect.DeepEqual(got, s.SettingsSnapshots()) {
				t.Fatal("toggle did not publish one revision-fenced atomic snapshot")
			}
			if got.Effective.SessionMultiAgent == nil || *got.Effective.SessionMultiAgent != !inherited || got.Effective.Agents.Enabled != !inherited {
				t.Fatal("toggle did not replace inheritance with the explicit opposite")
			}
			if !reflect.DeepEqual(got.Overrides, before.Overrides) || !reflect.DeepEqual(got.Base, before.Base) || got.Effective.SessionMainAgent != "profile/reviewer" || got.Effective.Model != "review-model" || got.Effective.TurnLogsEnabled != before.Effective.TurnLogsEnabled {
				t.Fatal("toggle overwrote unrelated root, role, baseline or logging settings")
			}
			persisted, err := sessiondb.LoadAgentSettingOverrides(s.db, s.sessionID)
			if err != nil || !reflect.DeepEqual(persisted, s.sessionOverrides) {
				t.Fatalf("toggle changed persisted role overrides: %v", err)
			}
			flags, err := sessiondb.LoadSessionFlags(s.db, s.sessionID)
			if err != nil || flags.MainAgent != "profile/reviewer" || flags.MultiAgent == nil || *flags.MultiAgent != !inherited {
				t.Fatalf("persisted delegation/root flags = %+v, %v", flags, err)
			}
			// Returned category maps, pointers and slices must not mutate session state.
			cat := got.Overrides[CategoryCrafter]
			cat.AllowedTools[0] = "write_file"
			cat.SkillModes["skill"] = "disabled"
			*cat.ContextCompactionPercent = 1
			effective := got.Effective.Agents.Categories[CategoryCrafter]
			effective.SkillModes["skill"] = "disabled"
			if !reflect.DeepEqual(s.SessionOverridesSnapshot(), before.Overrides) || s.SettingsSnapshot().Agents.Categories[CategoryCrafter].SkillModes["skill"] != "manual" {
				t.Fatal("returned snapshot aliases live settings")
			}
		})
	}
}

func TestSettingsSnapshotRevisionTracksDefaultsAndReset(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	before := s.SettingsSnapshots()
	s.UpdateWorkspaceDefaults(types.AppSettings{Accounts: s.cfg.Accounts, Agents: before.Base.Agents})
	updated := s.SettingsSnapshots()
	if updated.Revision <= before.Revision {
		t.Fatal("workspace update did not advance settings revision")
	}
	s.AdoptWorkspaceDefaults(types.AppSettings{Accounts: s.cfg.Accounts, Agents: updated.Base.Agents})
	adopted := s.SettingsSnapshots()
	if adopted.Revision <= updated.Revision {
		t.Fatal("workspace adoption did not advance settings revision")
	}
	if err := s.ResetSessionSettings(); err != nil {
		t.Fatal(err)
	}
	if s.SettingsSnapshots().Revision <= adopted.Revision {
		t.Fatal("reset did not advance settings revision")
	}
}

func TestSettingsCASOneWinner(t *testing.T) {
	for _, method := range []string{"save", "toggle", "reset"} {
		t.Run(method, func(t *testing.T) {
			s := newSettingsOperationSession(t, true)
			before := s.SettingsSnapshots()
			start := make(chan struct{})
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func() {
					<-start
					var err error
					switch method {
					case "save":
						_, err = s.SaveSettingsSnapshotAtRevision(before.Effective, before.Revision)
					case "toggle":
						_, err = s.ToggleMultiAgentAtRevision(before.Revision)
					case "reset":
						_, err = s.ResetSettingsSnapshotAtRevision(before.Revision)
					}
					results <- err
				}()
			}
			close(start)
			wins, conflicts := 0, 0
			for i := 0; i < 2; i++ {
				err := <-results
				if err == nil {
					wins++
				} else if errors.Is(err, ErrSettingsRevisionConflict) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if wins != 1 || conflicts != 1 || s.SettingsSnapshots().Revision != before.Revision+1 {
				t.Fatal("CAS did not admit exactly one mutation")
			}
		})
	}
}

func TestSettingsCASPersistenceFailureAndCloseLeaveSnapshotUnchanged(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	before := s.SettingsSnapshots()
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	operations := []func() error{
		func() error {
			_, err := s.SaveSettingsSnapshotAtRevision(before.Effective, before.Revision)
			return err
		},
		func() error { _, err := s.ToggleMultiAgentAtRevision(before.Revision); return err },
		func() error { _, err := s.ResetSettingsSnapshotAtRevision(before.Revision); return err },
	}
	for _, operation := range operations {
		if err := operation(); err == nil {
			t.Fatal("closed database accepted mutation")
		}
		if !reflect.DeepEqual(before, s.SettingsSnapshots()) {
			t.Fatal("failed persistence changed settings")
		}
	}
	s.Close()
	for _, operation := range operations {
		if err := operation(); !errors.Is(err, ErrSettingsSessionClosing) {
			t.Fatalf("closed CAS: %v", err)
		}
	}
}

func TestSettingsCASResetRetainsPinsAndRestoresDefaults(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	settings := s.SettingsSnapshot()
	disabled := false
	settings.SessionMainAgent, settings.SessionMultiAgent = "profile/reviewer", &disabled
	settings.SessionOverrides = map[string]types.CategorySettings{
		CategoryDefault:         {AccountID: "account", Model: "edited"},
		CategoryCrafter:         {AccountID: "account", Model: "edited-crafter"},
		"root/profile/reviewer": {AccountID: "account"},
	}
	saved, err := s.SaveSettingsSnapshotAtRevision(settings, s.SettingsSnapshots().Revision)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := s.ResetSettingsSnapshotAtRevision(saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Effective.SessionMainAgent != "" || reset.Effective.SessionMultiAgent != nil || !reset.Effective.Agents.Enabled || reset.Effective.Model == "edited" {
		t.Fatal("reset failed to restore workspace policy")
	}
	if reset.Overrides[CategoryDefault].AccountID != "account" || reset.Overrides[CategoryCrafter].AccountID != "account" || len(reset.Overrides) != 2 {
		t.Fatal("reset lost pins or kept root override")
	}
	if !reflect.DeepEqual(saved.Base, reset.Base) {
		t.Fatal("reset changed baseline")
	}
}

func TestSettingsOperationsRejectAfterCloseWithoutMutation(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	before := s.SettingsSnapshot()
	overrides := s.SessionOverridesSnapshot()
	s.Close()
	settings := before
	settings.SessionOverrides = map[string]types.CategorySettings{CategoryDefault: {AccountID: "account", Model: "late"}}
	for _, operation := range []func() error{
		func() error { _, err := s.SaveSettingsSnapshot(settings); return err },
		func() error { return s.HandleSettingsSave(settings) },
		func() error { _, err := s.ToggleMultiAgent(); return err },
		s.ResetSessionSettings,
	} {
		if err := operation(); !errors.Is(err, ErrSettingsSessionClosing) {
			t.Fatalf("late operation error = %v", err)
		}
	}
	if !reflect.DeepEqual(before, s.SettingsSnapshot()) || !reflect.DeepEqual(overrides, s.SessionOverridesSnapshot()) {
		t.Fatal("late operation mutated closed session")
	}
}

func TestSettingsOperationsConcurrentSaveToggleClose(t *testing.T) {
	s := newSettingsOperationSession(t, true)
	settings := s.SettingsSnapshot()
	settings.SessionOverrides = s.SessionOverridesSnapshot()
	settings.SessionOverrides[CategoryDefault] = types.CategorySettings{AccountID: "account", Model: "concurrent-model"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(toggle bool) {
			defer wg.Done()
			<-start
			var err error
			if toggle {
				_, err = s.ToggleMultiAgent()
			} else {
				_, err = s.SaveSettingsSnapshot(settings)
			}
			if err != nil && !errors.Is(err, ErrSettingsSessionClosing) {
				t.Errorf("concurrent operation: %v", err)
			}
		}(i%2 == 0)
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; s.Close() }()
	close(start)
	wg.Wait()
	before := s.SettingsSnapshot()
	if _, err := s.ToggleMultiAgent(); !errors.Is(err, ErrSettingsSessionClosing) {
		t.Fatalf("closed toggle: %v", err)
	}
	if !reflect.DeepEqual(before, s.SettingsSnapshot()) {
		t.Fatal("closed toggle changed memory")
	}
	db, err := sessiondb.Open(filepath.Clean(s.dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	persisted, err := sessiondb.LoadAgentSettingOverrides(db, s.sessionID)
	if err != nil || !reflect.DeepEqual(persisted, s.sessionOverrides) {
		t.Fatalf("close raced settings publication: %v", err)
	}
	flags, err := sessiondb.LoadSessionFlags(db, s.sessionID)
	if err != nil || !reflect.DeepEqual(flags.MultiAgent, before.SessionMultiAgent) {
		t.Fatalf("close raced delegation publication: %+v, %v", flags, err)
	}
}
