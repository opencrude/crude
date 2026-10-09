package workspace

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"crude/session"
	"crude/types"
)

func newSettingsOperationWorkspace(t *testing.T) (*Workspace, *Session) {
	t.Helper()
	setTestHome(t)
	w := NewWorkspace(t.TempDir(), "model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	owner, err := w.CreateSession(workspaceToolRootConfig(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return w, owner
}

func TestPreparedSessionSettingsPublishOnlyOnOwner(t *testing.T) {
	w, owner := newSettingsOperationWorkspace(t)
	before := owner.Settings
	operation, err := w.PrepareMultiAgentToggle(owner.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result := operation.Run()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if owner.Settings != before {
		t.Fatal("background operation mutated the owner-thread projection")
	}
	if !reflect.DeepEqual(result.Snapshot, operation.Run().Snapshot) {
		t.Fatal("repeating operation toggled delegation again")
	}
	if !w.ApplySessionSettingsResult(result) || owner.Settings.Effective.Agents.Enabled == before.Effective.Agents.Enabled {
		t.Fatal("owner publication did not install completed toggle")
	}

	newer, err := w.PrepareMultiAgentToggle(owner.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !w.ApplySessionSettingsResult(newer.Run()) || w.ApplySessionSettingsResult(result) {
		t.Fatal("out-of-order completion replaced newer settings")
	}
}

func TestPreparedSessionSettingsRetainExactDraftAndOwner(t *testing.T) {
	w, owner := newSettingsOperationWorkspace(t)
	owner.AgentID = "root"
	w.SessionsByAgent["root"] = owner
	w.RegisterAgentRoot("root")
	settings := owner.backend.SettingsSnapshot()
	percent := 63
	settings.SessionOverrides = map[string]types.CategorySettings{"default": {
		AccountID: settings.AccountID, Model: "exact-model", AllowedTools: []string{"read_file"},
		SkillModes: map[string]string{"procedure": "manual"}, ContextCompactionPercent: &percent,
	}}
	operation, err := w.PrepareSessionSettingsSave(owner.SessionID, settings)
	if err != nil {
		t.Fatal(err)
	}
	category := settings.SessionOverrides["default"]
	category.AllowedTools[0], category.SkillModes["procedure"], percent = "write_file", "disabled", 20
	settings.SessionOverrides["default"] = types.CategorySettings{Model: "replaced"}
	result := operation.Run()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	got := result.Snapshot.Overrides["default"]
	if got.Model != "exact-model" || got.AllowedTools[0] != "read_file" || got.SkillModes["procedure"] != "manual" || *got.ContextCompactionPercent != 63 {
		t.Fatalf("prepared settings borrowed mutable form bytes: %+v", got)
	}
	if !w.ApplySessionSettingsResult(result) || w.SnapshotAgents().Agents[0].Model != "exact-model" {
		t.Fatal("headless settings publication did not update canonical model identity")
	}
	got.AllowedTools[0] = "execute_command"
	if operation.Run().Snapshot.Overrides["default"].AllowedTools[0] != "read_file" {
		t.Fatal("result alias changed a replayed receipt")
	}
	replacement := &Session{SessionID: owner.SessionID}
	w.SessionsByID[owner.SessionID] = replacement
	if w.ApplySessionSettingsResult(result) || replacement.Settings != nil {
		t.Fatal("old result attached to a replacement owner")
	}
	w.SessionsByID[owner.SessionID] = owner
}

func TestPreparedSessionSettingsRaceWithCloseDoesNotReadWorkspaceMaps(t *testing.T) {
	w, owner := newSettingsOperationWorkspace(t)
	operation, err := w.PrepareMultiAgentToggle(owner.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var result SessionSettingsResult
	var joined sync.WaitGroup
	joined.Add(1)
	go func() {
		defer joined.Done()
		result = operation.Run()
	}()
	// Closing replaces maps and joins admitted backend mutations. The prepared
	// command must hold no reference to mutable workspace maps or UI settings.
	w.CloseAllSessions()
	joined.Wait()
	if result.Err != nil && !errors.Is(result.Err, session.ErrSettingsSessionClosing) {
		t.Fatalf("close race failed outside the lifetime fence: %v", result.Err)
	}
	if w.ApplySessionSettingsResult(result) {
		t.Fatal("closed owner accepted a late settings projection")
	}
	if _, err := w.PrepareMultiAgentToggle(owner.SessionID); err == nil {
		t.Fatal("closed session admitted a new settings operation")
	}
}

func TestSessionSettingsProjectionDetachesNamedProfilePolicy(t *testing.T) {
	source := types.Settings{Agents: types.AgentsSettings{AgentProfiles: map[string]types.AgentProfileSettings{
		"review": {SkillModes: map[string]string{"procedure": "manual"}},
	}}}
	projection := NewSessionSettingsState(source, source, nil)
	projection.Effective.Agents.AgentProfiles["review"].SkillModes["procedure"] = "disabled"
	if source.Agents.AgentProfiles["review"].SkillModes["procedure"] != "manual" ||
		projection.Base.Agents.AgentProfiles["review"].SkillModes["procedure"] != "manual" {
		t.Fatal("settings projection changed source or baseline policy through an alias")
	}
}

func TestPreparedSettingsCASRejectsStaleAndReplacementPublication(t *testing.T) {
	w, owner := newSettingsOperationWorkspace(t)
	before := owner.backend.SettingsSnapshots()
	first, err := w.PrepareSessionSettingsToggleAtRevision(owner.SessionID, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.PrepareSessionSettingsResetAtRevision(owner.SessionID, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	winner := first.Run()
	if winner.Err != nil {
		t.Fatal(winner.Err)
	}
	loser := second.Run()
	if !errors.Is(loser.Err, session.ErrSettingsRevisionConflict) || w.ApplySessionSettingsResult(loser) {
		t.Fatal("stale prepared operation applied")
	}
	if !w.ApplySessionSettingsResult(winner) {
		t.Fatal("winning prepared operation not published")
	}
}
