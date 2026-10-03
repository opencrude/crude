package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
)

func TestManualAcquisitionWorkflowRequiresExactPreviewAndPersistsSelection(test *testing.T) {
	setTestHome(test)
	root := test.TempDir()
	directory := filepath.Join(root, "catalog", "review")
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		test.Fatal(err)
	}
	if _, err := skills.Create(directory, "review", "Review exact evidence before claiming success."); err != nil {
		test.Fatal(err)
	}
	owner := &Workspace{RootPath: root, WorkspaceDir: test.TempDir(), WorkspaceDB: openTestWorkspaceDB(test), Logger: testLogger{}}
	owner.SetAppDefaults(types.AppSettings{Capabilities: captypes.Settings{Sources: map[string]captypes.SourceConfig{"local": {ID: "local", Name: "Local", Kind: "local", Location: filepath.Dir(directory), Trusted: true}}, Acquisition: captypes.AcquisitionSettings{Mode: "manual"}}})
	test.Cleanup(func() { owner.Capabilities.Close() })
	_, entries, _, err := owner.CapabilityAction(test.Context(), captypes.Action{Operation: "discover", Kind: captypes.KindSkill, Scope: captypes.ScopeWorkspace, Query: "review"})
	if err != nil || len(entries) != 1 {
		test.Fatalf("manual Discover: %+v %v", entries, err)
	}
	action := captypes.Action{Operation: "install", Kind: captypes.KindSkill, ID: entries[0].ID, Scope: captypes.ScopeWorkspace}
	if _, _, _, err := owner.CapabilityAction(test.Context(), action); err == nil {
		test.Fatal("manual install skipped preview")
	}
	previewAction := action
	previewAction.Operation = "preview-install"
	_, _, content, err := owner.CapabilityAction(test.Context(), previewAction)
	var preview captypes.AcquisitionPreview
	if err != nil || json.Unmarshal([]byte(content), &preview) != nil || preview.Scope != action.Scope {
		test.Fatalf("preview: %s %v", content, err)
	}
	if len(owner.LoadAppSettings().Capabilities.Skills) != 0 {
		test.Fatal("preview published active selection")
	}
	action.Preview = &preview
	snapshot, _, _, err := owner.CapabilityAction(test.Context(), action)
	if err != nil || !snapshot.Settings.Skills[action.ID].Managed {
		test.Fatalf("reviewed install: %+v %v", snapshot, err)
	}
	if _, exists := owner.AppDefaults().Capabilities.Skills[action.ID]; exists {
		test.Fatal("workspace install promoted itself to application")
	}
	if owner.LoadAppSettings().Capabilities.Skills[action.ID].Digest == "" {
		test.Fatal("selected digest did not persist")
	}
	if _, _, _, err := owner.CapabilityAction(test.Context(), action); err == nil {
		test.Fatal("older install preview survived binding publication")
	}
}
