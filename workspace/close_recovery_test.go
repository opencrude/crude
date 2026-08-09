package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"crude/sessiondb"
)

// TestCloseAgentLeavesListableResumableRegistryRecord covers the recovery
// contract: close stamps the registry (registering if needed) and ListSessions
// returns the row. Uses HOME isolation so ~/.crude is never touched.
func TestCloseAgentLeavesListableResumableRegistryRecord(t *testing.T) {
	setTestHome(t)

	project := t.TempDir()
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatalf("OpenWorkspaceDB: %v", err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	const sid = "2026-07-30_recovery_test"
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatalf("WorkspaceDir: %v", err)
	}
	sessRoot := filepath.Join(wsDir, "sessions")
	if err := os.MkdirAll(sessRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID:          sid,
		ProjectPath: project,
		ProjectName: filepath.Base(project),
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1000,
	}); err != nil {
		t.Fatalf("sessiondb.Create: %v", err)
	}
	if err := sessiondb.UpdateFirstPrompt(sdb, sid, "hello recovery probe"); err != nil {
		t.Fatalf("UpdateFirstPrompt: %v", err)
	}

	mgr := NewAgentManager()
	cancelCh := make(chan struct{}, 1)
	mgr.AddOrchestrator("agent", nil, nil, cancelCh, nil, &AgentState{})
	sess := &Session{SessionID: sid, AgentID: "agent", DB: sdb}
	ws := &Workspace{
		RootPath:        project,
		WorkspaceDir:    wsDir,
		WorkspaceDB:     wdb,
		AgentMgr:        mgr,
		Sessions:        []*Session{sess},
		SessionsByID:    map[string]*Session{sid: sess},
		SessionsByAgent: map[string]*Session{"agent": sess},
		Logger:          nopLogger{},
	}

	ws.CloseAgent("agent")

	listed := ws.ListSessions()
	if len(listed) != 1 || listed[0].ID != sid {
		t.Fatalf("ListSessions after close: %+v", listed)
	}
	var closedAt int64
	if err := wdb.DB.QueryRow(`SELECT closed_at FROM workspace_sessions WHERE session_id = ?`, sid).Scan(&closedAt); err != nil {
		t.Fatalf("closed_at: %v", err)
	}
	if closedAt == 0 {
		t.Fatal("expected closed_at stamped")
	}
	if fp := listed[0].FirstPrompt; !fp.Valid || fp.String != "hello recovery probe" {
		t.Fatalf("first_prompt after sync: %+v", fp)
	}
}
