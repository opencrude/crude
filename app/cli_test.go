package app

import (
	"path/filepath"
	"testing"

	"crude/workspace"
)

func TestResolveCLISessionStoreUsesCurrentWorkspaceLayout(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	store, err := resolveCLISessionStore(home, project)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := DataDir(home) + string(filepath.Separator) + "workspaces" + string(filepath.Separator) + workspace.WorkspaceID(project)
	if filepath.Dir(store.workspaceDB) != wantDir || store.sessionsDir != filepath.Join(wantDir, "sessions") {
		t.Fatalf("store = %+v, want workspace directory %q", store, wantDir)
	}
}
