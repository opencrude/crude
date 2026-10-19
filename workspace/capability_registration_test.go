package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
)

func TestCapabilityRegistrationWaitingForClosingOwnerIsCancellable(t *testing.T) {
	owner := &Workspace{RootPath: t.TempDir(), WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	store := newCapabilityRegistrationStore(owner)
	owner.capabilityMu.Lock()
	defer owner.capabilityMu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := store.CommitRegistration(ctx, "pending", captypes.Settings{}, captypes.Settings{})
		result <- err
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("registration bypassed the closing owner's lease: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("registration wait did not preserve cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session teardown would deadlock joining a capability registration worker")
	}
}

func TestCapabilityRegistrationStoreMergesOnlySelectedIdentity(t *testing.T) {
	db := openTestWorkspaceDB(t)
	root := t.TempDir()
	owner := &Workspace{RootPath: root, WorkspaceDB: db, Logger: testLogger{}}
	defaults := types.AppSettings{
		Editor: types.EditorSettings{TabSize: 4},
		Capabilities: captypes.Settings{
			Sources: map[string]captypes.SourceConfig{
				"local": {ID: "local", Name: "Local", Kind: "local", Location: root, Trusted: true, Scope: captypes.ScopeApplication},
			},
			Connections: map[string]captypes.ConnectionConfig{
				"custom": {ID: "custom", Name: "Custom", Transport: "http", URL: "https://example.test", Scope: captypes.ScopeApplication},
			},
			Acquisition: captypes.AcquisitionSettings{Mode: "automatic", AutomaticSelection: "on"},
		},
	}
	owner.SetAppDefaults(defaults)
	before := owner.LoadAppSettings().Capabilities

	working := owner.LoadAppSettings()
	working.Editor.TabSize = 8
	working.Capabilities.Skills["neighbor"] = captypes.SkillConfig{
		ID: "neighbor", Name: "Neighbor", Path: filepath.Join(root, "neighbor"), Scope: captypes.ScopeWorkspace,
	}
	if err := owner.SaveWorkspaceSettings(types.OverridesFromDiff(owner.AppDefaults(), working)); err != nil {
		t.Fatal(err)
	}

	after := captypes.Clone(before)
	after.Skills["local:selected"] = captypes.SkillConfig{
		ID: "local:selected", Name: "Selected", Path: filepath.Join(root, "selected"),
		Source: "local", Digest: "digest", Managed: true, Mode: "automatic", Scope: captypes.ScopeWorkspace,
	}
	persisted, err := newCapabilityRegistrationStore(owner).CommitRegistration(t.Context(), "local:selected", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := persisted.Skills["neighbor"]; !exists {
		t.Fatal("registration replaced a concurrently persisted neighboring skill")
	}
	if _, exists := persisted.Skills["local:selected"]; !exists {
		t.Fatal("selected registration was not returned")
	}
	effective := owner.LoadAppSettings()
	if effective.Editor.TabSize != 8 {
		t.Fatalf("unrelated workspace preference changed: tab size=%d", effective.Editor.TabSize)
	}
	if _, exists := effective.Capabilities.Skills["neighbor"]; !exists {
		t.Fatal("neighboring registration was not preserved in workspace settings")
	}
	if _, exists := owner.AppDefaults().Capabilities.Skills["local:selected"]; exists {
		t.Fatal("workspace registration was promoted to application defaults")
	}

	before = owner.LoadAppSettings().Capabilities
	after = captypes.Clone(before)
	custom := after.Connections["custom"]
	custom.Scope = captypes.ScopeWorkspace
	custom.Grants = map[string]captypes.ToolGrant{
		"review": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: "schema"},
	}
	after.Connections["custom"] = custom
	if _, err := newCapabilityRegistrationStore(owner).CommitRegistration(t.Context(), "custom", before, after); err != nil {
		t.Fatalf("grant update for an existing source-less custom connection: %v", err)
	}
	if owner.LoadAppSettings().Capabilities.Connections["custom"].Grants["review"].Fingerprint != "schema" {
		t.Fatal("custom connection grant was not published")
	}
}

func TestPreferenceDraftCannotEraseLaterAgentCapabilityInstall(t *testing.T) {
	owner := &Workspace{RootPath: t.TempDir(), WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	owner.SetAppDefaults(types.AppSettings{Editor: types.EditorSettings{TabSize: 4}})
	draft := owner.LoadAppSettings()
	draft.Editor.TabSize = 8
	draft.Capabilities.Acquisition.Mode = "manual"
	save := owner.PreparePreferencesSave(types.SettingsScopeWorkspace, types.SettingsEdits(owner.LoadAppSettings(), draft))
	current := owner.LoadAppSettings()
	current.Capabilities.Skills["later-install"] = captypes.SkillConfig{ID: "later-install", Name: "Later skill", Path: "later", Scope: captypes.ScopeWorkspace}
	current.Capabilities.Connections["later-mcp"] = captypes.ConnectionConfig{
		ID: "later-mcp", Name: "Later MCP", Transport: "http", URL: "https://example.test", Scope: captypes.ScopeWorkspace,
		Grants: map[string]captypes.ToolGrant{"lookup": {Effect: captypes.EffectRead, Scope: "public", Fingerprint: "exact"}},
	}
	if err := owner.SaveWorkspaceSettings(types.OverridesFromDiff(owner.AppDefaults(), current)); err != nil {
		t.Fatal(err)
	}
	result := save()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	persisted := owner.LoadAppSettings()
	if persisted.Editor.TabSize != 8 || persisted.Capabilities.Acquisition.Mode != "manual" {
		t.Fatal("preference draft changes were not saved")
	}
	if persisted.Capabilities.Skills["later-install"].ID == "" || persisted.Capabilities.Connections["later-mcp"].Grants["lookup"].Fingerprint != "exact" {
		t.Fatal("saving unrelated preferences erased a later capability install or grant")
	}
}

func TestCapabilityRegistrationStoreRejectsStalePreimageAndReplacementOwner(t *testing.T) {
	db := openTestWorkspaceDB(t)
	root := t.TempDir()
	owner := &Workspace{RootPath: root, WorkspaceDB: db, Logger: testLogger{}}
	owner.SetAppDefaults(types.AppSettings{Capabilities: captypes.Settings{
		Sources: map[string]captypes.SourceConfig{
			"local": {ID: "local", Name: "Local", Kind: "local", Location: root, Trusted: true},
		},
		Acquisition: captypes.AcquisitionSettings{Mode: "automatic"},
	}})
	store := newCapabilityRegistrationStore(owner)
	before := owner.LoadAppSettings().Capabilities
	after := captypes.Clone(before)
	after.Skills["local:selected"] = captypes.SkillConfig{
		ID: "local:selected", Name: "Selected", Path: filepath.Join(root, "selected"),
		Source: "local", Managed: true, Scope: captypes.ScopeWorkspace,
	}

	working := owner.LoadAppSettings()
	working.Capabilities.Acquisition.Mode = "manual"
	if err := owner.SaveWorkspaceSettings(types.OverridesFromDiff(owner.AppDefaults(), working)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitRegistration(t.Context(), "local:selected", before, after); err == nil {
		t.Fatal("stale acquisition policy was accepted")
	}

	before = owner.LoadAppSettings().Capabilities
	after = captypes.Clone(before)
	after.Skills["local:selected"] = captypes.SkillConfig{
		ID: "local:selected", Name: "Selected", Path: filepath.Join(root, "selected"),
		Source: "local", Managed: true, Scope: captypes.ScopeWorkspace,
	}
	working = owner.LoadAppSettings()
	source := working.Capabilities.Sources["local"]
	source.Location = filepath.Join(root, "replacement")
	working.Capabilities.Sources["local"] = source
	if err := owner.SaveWorkspaceSettings(types.OverridesFromDiff(owner.AppDefaults(), working)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitRegistration(t.Context(), "local:selected", before, after); err == nil {
		t.Fatal("stale capability source was accepted")
	}

	owner.WorkspaceDB = openTestWorkspaceDB(t)
	if _, err := store.CommitRegistration(t.Context(), "local:selected", owner.LoadAppSettings().Capabilities, after); err == nil {
		t.Fatal("registration wrote through a replacement workspace owner")
	}
}

func TestAgentCapabilityActivationPersistsInWorkspaceSettingsWithoutShadowInstall(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	catalog := filepath.Join(root, "catalog")
	sourceDirectory := filepath.Join(catalog, "review")
	if err := os.MkdirAll(catalog, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Create(sourceDirectory, "review", "Review exact evidence before claiming success."); err != nil {
		t.Fatal(err)
	}
	owner := &Workspace{RootPath: root, WorkspaceDir: t.TempDir(), WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	sources := captypes.DefaultSources()
	for id, source := range sources {
		source.Disabled = true
		sources[id] = source
	}
	sources["local"] = captypes.SourceConfig{ID: "local", Name: "Local", Kind: "local", Location: catalog, Trusted: true}
	owner.SetAppDefaults(types.AppSettings{Capabilities: captypes.Settings{
		Sources:     sources,
		Acquisition: captypes.AcquisitionSettings{Mode: "automatic", AutomaticSelection: "on"},
	}})
	t.Cleanup(func() {
		if owner.Capabilities != nil {
			owner.Capabilities.Close()
		}
	})
	actor := activeCapabilityActor(t, owner, "agent-1")
	if err := owner.ensureCapabilities(t.Context()); err != nil {
		t.Fatal(err)
	}
	search, err := owner.Capabilities.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_search", map[string]any{
		"query": "review",
		"kind":  string(captypes.KindSkill),
	})
	if err != nil {
		t.Fatal(err)
	}
	var entries []captypes.Entry
	if len(search.Content) != 1 || json.Unmarshal([]byte(search.Content[0].Text), &entries) != nil || len(entries) == 0 {
		t.Fatalf("search result=%+v", search)
	}
	id := "local:review"
	// Explicit workspace binding: the task asked for a project-owned capability.
	if _, err := owner.Capabilities.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": id, "scope": "workspace"}); err != nil {
		t.Fatal(err)
	}
	installed := owner.LoadAppSettings().Capabilities.Skills[id]
	if installed.ID != id || !installed.Managed || installed.Digest == "" {
		t.Fatalf("workspace settings missing acquired registration: %+v", installed)
	}
	if installed.Scope != captypes.ScopeWorkspace {
		t.Fatalf("explicit workspace binding lost its scope: %q", installed.Scope)
	}
	var shadowTable string
	err = owner.WorkspaceDB.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='capability_installs'`).Scan(&shadowTable)
	if err == nil {
		t.Fatal("capability_installs shadow persistence was recreated")
	}

	if err := os.RemoveAll(catalog); err != nil {
		t.Fatal(err)
	}
	owner.Capabilities.Close()
	owner.Capabilities = nil
	snapshot, err := owner.CapabilitySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := snapshot.Settings.Skills[id]; !exists {
		t.Fatal("fresh service did not reopen the workspace registration from settings")
	}
	if _, err := owner.Capabilities.Execute(t.Context(), activeCapabilityActor(t, owner, "agent-2"), captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": id, "scope": "workspace"}); err != nil {
		t.Fatalf("fresh service tried to reacquire removed source bytes: %v", err)
	}

	if _, _, _, err := owner.CapabilityAction(t.Context(), captypes.Action{Operation: "disable", Kind: captypes.KindSkill, ID: id, Scope: captypes.ScopeWorkspace}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Capabilities.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": id, "scope": "workspace"}); err == nil {
		t.Fatal("disabled registration remained activatable")
	}
	if _, _, _, err := owner.CapabilityAction(t.Context(), captypes.Action{Operation: "remove", Kind: captypes.KindSkill, ID: id, Scope: captypes.ScopeWorkspace}); err != nil {
		t.Fatal(err)
	}
	owner.Capabilities.Close()
	owner.Capabilities = nil
	snapshot, err = owner.CapabilitySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := snapshot.Settings.Skills[id]; exists {
		t.Fatal("removed registration resurrected after service reopen")
	}
}
