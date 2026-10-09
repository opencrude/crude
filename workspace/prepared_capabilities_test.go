package workspace

import (
	"context"
	"crude/types"
	captypes "crude/types/capability"
	"testing"
)

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

func TestPreparedCapabilityOverlayPreservesNeighborsAndConflicts(t *testing.T) {
	w := NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, nil)
	defer w.Close()
	if err := w.ensureWorkspacePersistence(); err != nil {
		t.Fatal(err)
	}
	before := w.LoadAppSettings()
	commit := w.PrepareCapabilityOverlayCommit()
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
	if _, err := commit(context.Background(), before, after, captypes.ScopeWorkspace); err != ErrApplicationSettingsConflict {
		t.Fatalf("stale overlay accepted: %v", err)
	}
	if w.LoadAppSettings().Capabilities.Sources["local"].ID != "local" {
		t.Fatal("conflict lost committed registration")
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
	if _, _, _, _, err := run(context.Background()); err != ErrApplicationSettingsConflict {
		t.Fatalf("stale runtime action admitted: %v", err)
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
