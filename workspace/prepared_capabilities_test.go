package workspace

import (
	"context"
	"crude/types"
	captypes "crude/types/capability"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestProjectToolDecisionConcurrentPreferenceSaves(t *testing.T) {
	w := NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, nil)
	defer w.Close()
	if err := w.ensureWorkspacePersistence(); err != nil {
		t.Fatal(err)
	}
	const decision = `{ "grant" : [ "exact", 2 ] }`
	if err := w.WorkspaceDB.SetSetting(workspaceSettingsKey, `{"capabilities":{"project_tool_decisions":`+decision+`}}`); err != nil {
		t.Fatal(err)
	}
	saves := []func() PreferencesSaveResult{
		w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage(`6`)}}),
		w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"editor", "word_wrap"}, After: json.RawMessage(`true`)}}),
	}
	var wg sync.WaitGroup
	errors := make(chan error, len(saves))
	for _, save := range saves {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- save().Err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	row := w.WorkspaceDB.GetSetting(workspaceSettingsKey)
	if !strings.Contains(row, `"project_tool_decisions":`+decision) || !strings.Contains(row, `"tab_size":6`) || !strings.Contains(row, `"word_wrap":true`) {
		t.Fatalf("concurrent saves lost data: %s", row)
	}
}

func TestProjectToolDecisionGenericWriters(t *testing.T) {
	w := NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, nil)
	defer w.Close()
	if err := w.ensureWorkspacePersistence(); err != nil {
		t.Fatal(err)
	}
	initial := `{"capabilities":{"project_tool_decisions":{ "invalid" : [ 1, 2 ] }}}`
	if err := w.WorkspaceDB.SetSetting(workspaceSettingsKey, initial); err != nil {
		t.Fatal(err)
	}
	assertStored := func() {
		t.Helper()
		if row := w.WorkspaceDB.GetSetting(workspaceSettingsKey); !strings.Contains(row, `"project_tool_decisions":{ "invalid" : [ 1, 2 ] }`) {
			t.Fatalf("decision changed: %s", row)
		}
	}
	for _, edit := range []types.SettingEdit{
		{Path: []string{"capabilities"}, After: json.RawMessage(`{"project_tool_decisions":{"grant":true}}`)},
		{Path: []string{"capabilities", "project_tool_decisions"}},
		{Path: []string{"capabilities", "project_tool_decisions", "grant"}, After: json.RawMessage(`true`)},
	} {
		if result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{edit})(); result.Err == nil {
			t.Fatalf("accepted authority edit: %v", edit.Path)
		}
	}
	if result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage(`6`)}})(); result.Err != nil {
		t.Fatal(result.Err)
	}
	assertStored()
	if err := w.SaveWorkspaceSettings(types.WorkspaceOverrides{Capabilities: json.RawMessage(`{"project_tool_decisions":{"forged":true}}`)}); err != nil {
		t.Fatal(err)
	}
	assertStored()
	before := w.LoadAppSettings()
	after := types.CloneAppSettings(before)
	after.Capabilities.ProjectToolDecisions = json.RawMessage(`{"forged":true}`)
	if _, err := w.PrepareCapabilityOverlayCommit(captypes.Action{})(context.Background(), before, after, captypes.ScopeWorkspace); err == nil {
		t.Fatal("capability snapshot accepted decision change")
	}
	assertStored()
}

func TestPreparedCapabilitiesDetachesActionAndRejectsUnreviewedAcquisition(t *testing.T) {
	w := NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, nil)
	defer w.Close()
	if err := w.ensureWorkspacePersistence(); err != nil {
		t.Fatal(err)
	}
	if err := w.ensureCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	commits := 0
	prepared, err := w.PrepareCapabilities(func(context.Context, types.AppSettings, types.AppSettings, captypes.Scope) (types.AppSettings, error) {
		commits++
		return types.AppSettings{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	action := captypes.Action{Operation: "install", ID: "unknown", Scope: captypes.ScopeApplication}
	run := prepared.PrepareAction(action)
	action.Operation = "snapshot"
	_, _, _, _, err = run(context.Background())
	if err == nil {
		t.Fatal("unreviewed install accepted")
	}
	if commits != 0 {
		t.Fatal("unreviewed acquisition published")
	}
}

func TestPreparedCapabilityOverlayPreservesNeighborsAndAcceptsRepeatedSet(t *testing.T) {
	w := NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, nil)
	defer w.Close()
	if err := w.ensureWorkspacePersistence(); err != nil {
		t.Fatal(err)
	}
	before := w.LoadAppSettings()
	// Parent-equal and schema-stale neighbors are stored intent/data, not a
	// reason for a capability edit to rewrite other categories.
	neighbors := types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":4,"removed_editor_field":"kept"}`), Agents: json.RawMessage(`{"Enabled":false}`)}
	if err := w.SaveWorkspaceSettings(neighbors); err != nil {
		t.Fatal(err)
	}
	before = w.LoadAppSettings()
	commit := w.PrepareCapabilityOverlayCommit(captypes.Action{})
	after := before
	after.Capabilities = captypes.Clone(before.Capabilities)
	after.Capabilities.Sources["local"] = captypes.SourceConfig{ID: "local", Location: t.TempDir(), Scope: captypes.ScopeWorkspace}
	published, err := commit(context.Background(), before, after, captypes.ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := published.Capabilities.Sources["local"]; !ok {
		t.Fatal("source not persisted")
	}
	stored := w.LoadWorkspaceOverrides()
	if string(stored.Editor) != string(neighbors.Editor) || string(stored.Agents) != string(neighbors.Agents) {
		t.Fatal("capability save rewrote unrelated explicit/stale neighbors")
	}
	if _, err := commit(context.Background(), before, after, captypes.ScopeWorkspace); err != nil {
		t.Fatalf("repeated explicit set rejected: %v", err)
	}
	if w.LoadAppSettings().Capabilities.Sources["local"].ID != "local" {
		t.Fatal("repeated set lost committed registration")
	}
}

func TestPreparedOldSnapshotCannotRewindCapabilityService(t *testing.T) {
	w := NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, nil)
	defer w.Close()
	if err := w.ensureWorkspacePersistence(); err != nil {
		t.Fatal(err)
	}
	if err := w.ensureCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, err := w.PrepareCapabilities(nil)
	if err != nil {
		t.Fatal(err)
	}
	newer := w.LoadAppSettings().Capabilities
	newer.Connections["new"] = captypes.ConnectionConfig{ID: "new", Name: "new", Transport: "http", URL: "https://example.com"}
	if err := w.Capabilities.Configure(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	generation := w.Capabilities.Snapshot().Generation
	snapshot, err := old.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != generation || snapshot.Settings.Connections["new"].ID != "new" {
		t.Fatal("passive old snapshot rewound service")
	}
	run := old.PrepareAction(captypes.Action{Operation: "disconnect", ID: "new"})
	if _, _, _, _, err := run(context.Background()); err != nil {
		t.Fatalf("explicit runtime action rejected after unrelated change: %v", err)
	}
}

func TestAcquiredRegistrationDoesNotPromoteEffectiveSiblings(t *testing.T) {
	current := captypes.Normalize(captypes.Settings{})
	current.Sources["source"] = captypes.SourceConfig{ID: "source", Location: "https://example.com"}
	current.Connections["neighbor"] = captypes.ConnectionConfig{ID: "neighbor", Name: "app", Scope: captypes.ScopeApplication}
	policy := captypes.Clone(current)
	value := policy.Connections["neighbor"]
	value.Name = "workspace override"
	policy.Connections["neighbor"] = value
	acquired := captypes.Clone(policy)
	acquired.Connections["selected"] = captypes.ConnectionConfig{ID: "selected", Source: "source", Scope: captypes.ScopeApplication}
	result, err := applyAcquiredRegistration(current, policy, acquired, captypes.Action{ID: "selected", Kind: captypes.KindMCP, Scope: captypes.ScopeApplication})
	if err != nil {
		t.Fatal(err)
	}
	if result.Connections["neighbor"].Name != "app" || result.Connections["selected"].ID != "selected" {
		t.Fatal("acquisition promoted effective sibling override")
	}
}
