package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
)

// localCatalogOwner builds a workspace whose only enabled source is a local
// skill catalog, so agent acquisition can be exercised without networking.
func localCatalogOwner(t *testing.T) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	catalog := filepath.Join(root, "catalog")
	if err := os.MkdirAll(catalog, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Create(filepath.Join(catalog, "review"), "review", "Review exact evidence before claiming success."); err != nil {
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
	// Exercise the same routing the app layer installs: application-bound
	// registrations are published through the app owner, workspace-bound ones
	// through the workspace overlay.
	owner.OnAppCapabilityRegistration = appRegistrationFixture(owner)
	t.Cleanup(func() {
		if owner.Capabilities != nil {
			owner.Capabilities.Close()
		}
	})
	return owner, catalog
}

// appRegistrationFixture mirrors the app layer's durable publication so the
// workspace tests can exercise application-scope routing without config.json.
func appRegistrationFixture(owner *Workspace) func(captypes.RegistrationChange) (captypes.Settings, error) {
	return func(change captypes.RegistrationChange) (captypes.Settings, error) {
		settings := owner.AppDefaults()
		merged, err := captypes.ApplyRegistration(captypes.Normalize(settings.Capabilities), change, owner.LoadAppSettings().Capabilities)
		if err != nil {
			return captypes.Settings{}, err
		}
		settings.Capabilities = merged
		owner.SetAppDefaults(settings)
		return merged, nil
	}
}

func acquireLocalSkill(t *testing.T, owner *Workspace, activation map[string]any) captypes.SkillConfig {
	t.Helper()
	if err := owner.ensureCapabilities(t.Context()); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "agent-1", Effects: []captypes.Effect{captypes.EffectRead}}
	if _, err := owner.Capabilities.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_search", map[string]any{"query": "review", "kind": "skill"}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Capabilities.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", activation); err != nil {
		t.Fatal(err)
	}
	return owner.LoadAppSettings().Capabilities.Skills["local:review"]
}

// TestAgentAcquisitionDefaultsToApplicationScope is the regression for the
// product default: an agent installing a capability must bind it to the user's
// application settings, not to the workspace it happened to run in.
func TestAgentAcquisitionDefaultsToApplicationScope(t *testing.T) {
	setTestHome(t)
	owner, _ := localCatalogOwner(t)
	installed := acquireLocalSkill(t, owner, map[string]any{"id": "local:review"})
	if installed.ID != "local:review" || installed.Scope != captypes.ScopeApplication {
		t.Fatalf("agent acquisition did not land in application scope: %+v", installed)
	}
	if _, inApp := owner.AppDefaults().Capabilities.Skills["local:review"]; !inApp {
		t.Fatal("default acquisition was not published to application settings")
	}
	overrides := owner.LoadWorkspaceOverrides()
	var parsed captypes.Settings
	if len(overrides.Capabilities) > 0 && json.Unmarshal(overrides.Capabilities, &parsed) == nil {
		if skill, exists := parsed.Skills["local:review"]; exists && skill.Scope == captypes.ScopeWorkspace {
			t.Fatal("default acquisition leaked into a workspace overlay")
		}
	}
}

// TestExplicitWorkspaceBindingHonorsScope confirms the opt-in path still works:
// asking for workspace scope must produce a workspace overlay.
func TestExplicitWorkspaceBindingHonorsScope(t *testing.T) {
	setTestHome(t)
	owner, _ := localCatalogOwner(t)
	installed := acquireLocalSkill(t, owner, map[string]any{"id": "local:review", "scope": "workspace"})
	if installed.Scope != captypes.ScopeWorkspace {
		t.Fatalf("explicit workspace binding produced scope %q", installed.Scope)
	}
	if _, inApp := owner.AppDefaults().Capabilities.Skills["local:review"]; inApp {
		t.Fatal("explicit workspace binding was promoted into application settings")
	}
}

// TestApplicationInstallSurvivesWorkspaceDeletion proves the consequence the
// user asked for: deleting a saved workspace must not destroy an
// application-bound capability or its stored artifact bytes.
func TestApplicationInstallSurvivesWorkspaceDeletion(t *testing.T) {
	setTestHome(t)
	owner, catalog := localCatalogOwner(t)
	installed := acquireLocalSkill(t, owner, map[string]any{"id": "local:review"})
	if installed.Scope != captypes.ScopeApplication || installed.Path == "" {
		t.Fatalf("install is not application-bound: %+v", installed)
	}
	// Downloaded/verified payloads must live outside the workspace store: that
	// store is exactly what workspace deletion moves to recovery storage.
	workspacesRoot, err := WorkspaceRoot()
	if err != nil {
		t.Fatal(err)
	}
	if insideDir(workspacesRoot, installed.Path) {
		t.Fatalf("application-owned artifact was stored inside the workspace store: %s", installed.Path)
	}

	projectDir := t.TempDir()
	createSavedWorkspace(t, projectDir)
	plan, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(projectDir), projectDir, owner.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteSavedWorkspace(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RestoreDeletedWorkspace(context.Background(), deleted) })

	if _, err := os.Stat(installed.Path); err != nil {
		t.Fatalf("workspace deletion destroyed the application-bound capability bytes: %v", err)
	}
	if got := owner.AppDefaults().Capabilities.Skills["local:review"]; got.Digest != installed.Digest {
		t.Fatal("workspace deletion changed the application registration")
	}
	// The source catalog is gone too; the registration must still reopen.
	if err := os.RemoveAll(catalog); err != nil {
		t.Fatal(err)
	}
	owner.Capabilities.Close()
	owner.Capabilities = nil
	snapshot, err := owner.CapabilitySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := snapshot.Settings.Skills["local:review"]; !exists {
		t.Fatal("application registration did not survive service reopen")
	}
}

// insideDir reports whether path is contained in root.
func insideDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func createSavedWorkspace(t *testing.T, projectDir string) {
	t.Helper()
	dir, err := WorkspaceDir(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := openWorkspaceDBAt(filepath.Join(dir, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wdb.Close() }()
	if err := wdb.SetSetting("project_dir", projectDir); err != nil {
		t.Fatal(err)
	}
}
