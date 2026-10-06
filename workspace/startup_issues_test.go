package workspace

import (
	"crude/collaboration"
	"crude/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceStorageFailureStillConstructsShellWithIssue(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	path, err := WorkspaceDBPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	w := NewWorkspace(root, "model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	issues := w.TakeStartupIssues()
	if w.AgentMgr == nil || w.Documents == nil || len(issues) == 0 {
		t.Fatal("storage failure did not produce a usable shell with repair context")
	}
	if !strings.Contains(issues[0].Title, "storage") || issues[0].Detail == "" {
		t.Fatal("storage failure has no meaningful recovery context")
	}
	if len(w.TakeStartupIssues()) != 0 {
		t.Fatal("startup issue replayed")
	}
}

func TestLauncherMissingTransportDoesNotCreateInvisibleSession(t *testing.T) {
	setTestHome(t)
	w := NewWorkspace(t.TempDir(), "model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	w.SetBackendConfig(workspaceToolRootConfig(), t.TempDir())
	result := w.NewSessionFromLauncher()
	if !strings.Contains(result.Error, "transport") || len(w.Sessions) != 0 || w.ActiveSession != nil {
		t.Fatal("failed transport created a hidden session")
	}
	files, err := filepath.Glob(filepath.Join(w.WorkspaceDir, "sessions", "*.db"))
	if err != nil || len(files) != 0 {
		t.Fatal("transport failure wrote session data")
	}
}

func TestLauncherRegistrationFailureCleansFailedSession(t *testing.T) {
	setTestHome(t)
	w := NewWorkspace(t.TempDir(), "model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	w.SetBackendConfig(workspaceToolRootConfig(), t.TempDir())
	w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	_, err := w.WorkspaceDB.DB.Exec(`CREATE TRIGGER reject_test_agent BEFORE INSERT ON collaboration_agents BEGIN SELECT RAISE(ABORT,'registration blocked'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	result := w.NewSessionFromLauncher()
	if !strings.Contains(result.Error, "registration blocked") {
		t.Fatalf("did not reproduce real agent-registration failure: %+v", result)
	}
	if len(w.Sessions) != 0 || len(w.SessionsByID) != 0 || w.ActiveSession != nil {
		t.Fatal("startup failure retained an invisible backend")
	}
	if agents, err := w.Collaboration.ListAgents(t.Context(), collaboration.AgentQuery{}); err != nil || len(agents) != 0 {
		t.Fatal("failed registration retained live agent ownership")
	}
}
