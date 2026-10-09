package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/collaboration"
	"crude/fileio"
	ttypes "crude/types"
)

type switchPreflightLogger struct{}

func (switchPreflightLogger) Debug(string, string, ...any) {}
func (switchPreflightLogger) Info(string, string, ...any)  {}
func (switchPreflightLogger) Warn(string, string, ...any)  {}
func (switchPreflightLogger) Error(string, string, ...any) {}

type switchPreflightSnapshot struct {
	root          string
	workspaceDir  string
	workspaceDB   *WorkspaceDB
	collaboration *collaboration.Store
	agentMgr      *AgentManager
	paneMgr       *PaneManager
	lspMgr        *LSPManager
	projectSearch *ProjectSearchService
	fileOps       *FileOpsService
	git           *GitService
	termMgr       *TerminalManager
	session       *Session
	search        ProjectSearchRequest
}

func newSwitchPreflightWorkspace(t *testing.T) (*Workspace, switchPreflightSnapshot) {
	t.Helper()
	setTestHome(t)
	root := t.TempDir()
	ws := NewWorkspace(root, "model", "user", ttypes.Settings{}, switchPreflightLogger{})
	t.Cleanup(ws.Close)

	frontendSession := NewSession("old-session", "", nil, switchPreflightLogger{})
	frontendSession.AgentID = "old-agent"
	ws.Sessions = []*Session{frontendSession}
	ws.SessionsByID = map[string]*Session{frontendSession.SessionID: frontendSession}
	ws.SessionsByAgent = map[string]*Session{frontendSession.AgentID: frontendSession}

	search := ws.ProjectSearch.Begin(root, "still-active", fileio.ProjectSearchOptions{})
	return ws, switchPreflightSnapshot{
		root:          ws.RootPath,
		workspaceDir:  ws.WorkspaceDir,
		workspaceDB:   ws.WorkspaceDB,
		collaboration: ws.Collaboration,
		agentMgr:      ws.AgentMgr,
		paneMgr:       ws.PaneMgr,
		lspMgr:        ws.LSPMgr,
		projectSearch: ws.ProjectSearch,
		fileOps:       ws.FileOps,
		git:           ws.Git,
		termMgr:       ws.TermMgr,
		session:       frontendSession,
		search:        search,
	}
}

func assertSwitchPreflightStateIntact(t *testing.T, ws *Workspace, before switchPreflightSnapshot) {
	t.Helper()
	if ws.RootPath != before.root {
		t.Fatalf("root changed after rejected switch: got %q want %q", ws.RootPath, before.root)
	}
	if ws.WorkspaceDir != before.workspaceDir || ws.WorkspaceDB != before.workspaceDB {
		t.Fatal("workspace persistence changed after rejected switch")
	}
	if ws.Collaboration != before.collaboration {
		t.Fatal("collaboration runtime changed after rejected switch")
	}
	if _, err := ws.Collaboration.Page(t.Context(), collaboration.PageQuery{Limit: 1}); err != nil {
		t.Fatalf("old collaboration runtime is unusable after rejected switch: %v", err)
	}
	if ws.AgentMgr != before.agentMgr ||
		ws.PaneMgr != before.paneMgr ||
		ws.LSPMgr != before.lspMgr ||
		ws.ProjectSearch != before.projectSearch ||
		ws.FileOps != before.fileOps ||
		ws.Git != before.git ||
		ws.TermMgr != before.termMgr {
		t.Fatal("one or more project managers changed after rejected switch")
	}
	if len(ws.Sessions) != 1 || ws.Sessions[0] != before.session {
		t.Fatal("session list changed after rejected switch")
	}
	if ws.SessionsByID[before.session.SessionID] != before.session ||
		ws.SessionsByAgent[before.session.AgentID] != before.session {
		t.Fatal("session indexes changed after rejected switch")
	}
	select {
	case <-before.search.ctx.Done():
		t.Fatal("active project search was canceled by rejected switch")
	default:
	}
	if err := before.workspaceDB.SetSetting("switch_preflight_probe", "alive"); err != nil {
		t.Fatalf("old workspace database was closed by rejected switch: %v", err)
	}
}

func TestSwitchProjectRejectsMissingAndRegularFileTargetsBeforeTeardown(t *testing.T) {
	tests := []struct {
		name       string
		target     func(*testing.T) string
		wantErrSub string
	}{
		{
			name: "missing",
			target: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing")
			},
			wantErrSub: "inspect project directory",
		},
		{
			name: "regular file",
			target: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "project.txt")
				if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErrSub: "is not a directory",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ws, before := newSwitchPreflightWorkspace(t)
			err := ws.SwitchProject(test.target(t))
			if err == nil {
				t.Fatal("SwitchProject succeeded for an unusable target")
			}
			if !strings.Contains(err.Error(), test.wantErrSub) {
				t.Fatalf("SwitchProject error = %q, want substring %q", err, test.wantErrSub)
			}
			assertSwitchPreflightStateIntact(t, ws, before)
		})
	}
}

func TestSwitchProjectPersistenceFailureLeavesOldWorkspaceIntact(t *testing.T) {
	ws, before := newSwitchPreflightWorkspace(t)
	target := t.TempDir()

	badHome := filepath.Join(t.TempDir(), "home-is-a-file")
	if err := os.WriteFile(badHome, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", badHome)
	t.Setenv("USERPROFILE", badHome)

	err := ws.SwitchProject(target)
	if err == nil {
		t.Fatal("SwitchProject succeeded when workspace persistence could not initialize")
	}
	if !strings.Contains(err.Error(), "prepare workspace directory") {
		t.Fatalf("SwitchProject error = %q, want persistence preparation context", err)
	}
	assertSwitchPreflightStateIntact(t, ws, before)
}

func TestSwitchProjectCollaborationFailureLeavesOldWorkspaceIntact(t *testing.T) {
	ws, before := newSwitchPreflightWorkspace(t)
	target := t.TempDir()
	targetWorkspace := NewWorkspace(target, "model", "user", ttypes.Settings{}, switchPreflightLogger{})
	if targetWorkspace.Collaboration == nil {
		targetWorkspace.Close()
		t.Fatal("target collaboration runtime did not initialize")
	}
	_, err := targetWorkspace.WorkspaceDB.DB.Exec(
		`UPDATE collaboration_workspace SET schema_version = ?`, collaboration.SchemaVersion+1,
	)
	targetWorkspace.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = ws.SwitchProject(target)
	if err == nil {
		t.Fatal("switch succeeded with an unusable target collaboration runtime")
	}
	if !strings.Contains(err.Error(), "prepare collaboration") {
		t.Fatalf("switch error = %q, want collaboration preparation context", err)
	}
	assertSwitchPreflightStateIntact(t, ws, before)
}
