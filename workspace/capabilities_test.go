package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
)

func TestCapabilityManualManagerOperationsAndInheritedRemoval(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	skill, err := skills.Create(filepath.Join(root, "review"), "review", "Inspect exact evidence")
	if err != nil {
		t.Fatal(err)
	}
	workspace := &Workspace{RootPath: root, WorkspaceDir: t.TempDir(), WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	settings := types.AppSettings{Capabilities: captypes.Settings{Skills: map[string]captypes.SkillConfig{"team:review": {ID: "team:review", Name: "review", Path: skill.Path, Managed: true, Digest: skill.Digest, Scope: captypes.ScopeApplication}}}}
	workspace.SetAppDefaults(settings)
	t.Cleanup(func() {
		if workspace.Capabilities != nil {
			workspace.Capabilities.Close()
		}
	})
	ctx := context.Background()
	operation := func(action captypes.Action) (captypes.Snapshot, string) {
		t.Helper()
		action.Scope = captypes.ScopeWorkspace
		snapshot, _, content, err := workspace.CapabilityAction(ctx, action)
		if err != nil {
			t.Fatalf("%s: %v", action.Operation, err)
		}
		return snapshot, content
	}
	_, content := operation(captypes.Action{Operation: "list-files", Kind: captypes.KindSkill, ID: "team:review"})
	var files []string
	if json.Unmarshal([]byte(content), &files) != nil || !slices.Contains(files, "SKILL.md") {
		t.Fatalf("files: %s", content)
	}
	if _, _, _, err := workspace.CapabilityAction(ctx, captypes.Action{Operation: "open-file", ID: "team:review", Content: "SKILL.md"}); err == nil {
		t.Fatal("managed artifact opened for editing")
	}
	_, content = operation(captypes.Action{Operation: "make-local-copy", Kind: captypes.KindSkill, ID: "team:review", Skill: &captypes.SkillConfig{ID: "local:review-copy", Name: "review-copy", Path: filepath.Join(root, "review-copy"), Mode: "manual"}})
	if content != "" {
		t.Fatalf("unexpected copy output %q", content)
	}
	_, opened := operation(captypes.Action{Operation: "open-file", Kind: captypes.KindSkill, ID: "local:review-copy", Content: "SKILL.md"})
	if opened != filepath.Join(root, "review-copy", "SKILL.md") {
		t.Fatalf("open path %q", opened)
	}
	operation(captypes.Action{Operation: "remove", Kind: captypes.KindSkill, ID: "team:review"})
	if _, exists := workspace.LoadAppSettings().Capabilities.Skills["team:review"]; exists {
		t.Fatal("removed inherited registration reappeared")
	}
	if _, exists := workspace.AppDefaults().Capabilities.Skills["team:review"]; !exists {
		t.Fatal("workspace removal changed application registration")
	}
	if _, err := os.Stat(filepath.Join(skill.Path, "SKILL.md")); err != nil {
		t.Fatal("removal deleted authored files")
	}
	operation(captypes.Action{Operation: "reset-inherited", Kind: captypes.KindSkill, ID: "team:review"})
	if _, exists := workspace.LoadAppSettings().Capabilities.Skills["team:review"]; !exists {
		t.Fatal("reset failed to restore inherited registration")
	}
	operation(captypes.Action{Operation: "remove", Kind: captypes.KindSkill, ID: "local:review-copy"})
	if _, exists := workspace.LoadAppSettings().Capabilities.Skills["local:review-copy"]; exists {
		t.Fatal("local removal failed")
	}
	if _, err := os.Stat(opened); err != nil {
		t.Fatal("local removal deleted user directory")
	}
}

func TestBuiltInCapabilitySourceCanDisableButNotDisappear(t *testing.T) {
	setTestHome(t)
	owner := &Workspace{
		RootPath: t.TempDir(), WorkspaceDir: t.TempDir(),
		WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{},
	}
	defaults := types.AppSettings{Capabilities: captypes.WithDefaults(captypes.Settings{})}
	owner.SetAppDefaults(defaults)
	t.Cleanup(func() {
		if owner.Capabilities != nil {
			owner.Capabilities.Close()
		}
	})

	_, _, _, err := owner.CapabilityAction(t.Context(), captypes.Action{
		Operation: "remove-source", Kind: "source",
		ID: captypes.OfficialMCPRegistrySourceID, Scope: captypes.ScopeApplication,
	})
	if err == nil {
		t.Fatal("built-in source was removed instead of requiring disable")
	}
	if _, _, _, err := owner.CapabilityAction(t.Context(), captypes.Action{
		Operation: "disable", Kind: "source",
		ID: captypes.OfficialMCPRegistrySourceID, Scope: captypes.ScopeApplication,
	}); err != nil {
		t.Fatal(err)
	}
	source := owner.LoadAppSettings().Capabilities.Sources[captypes.OfficialMCPRegistrySourceID]
	if !source.Builtin || !source.Disabled {
		t.Fatalf("disabled built-in source=%+v", source)
	}
}
