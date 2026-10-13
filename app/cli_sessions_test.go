package app

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/sessiondb"
	"crude/workspace"
)

func createCLISessionFixture(t *testing.T, projectDir, id string, createdAt int64, closed bool) string {
	t.Helper()
	wdb, err := workspace.OpenWorkspaceDB(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wdb.RegisterSession(id, projectDir, filepath.Base(projectDir), "test-model", "test-provider"); err != nil {
		t.Fatal(err)
	}
	if _, err := wdb.DB.Exec(`UPDATE workspace_sessions SET created_at = ?, turn_count = 2, message_count = 4, first_prompt = 'hello' WHERE session_id = ?`, createdAt, id); err != nil {
		t.Fatal(err)
	}
	if closed {
		if err := wdb.CloseSession(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := wdb.Close(); err != nil {
		t.Fatal(err)
	}
	dbPath, err := workspace.SessionDBPath(projectDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: id, ProjectPath: projectDir, ProjectName: filepath.Base(projectDir), Model: "test-model", Provider: "test-provider", CreatedAt: createdAt, TurnCount: 2, MessageCount: 4, FirstPrompt: sql.NullString{String: "hello", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestCLISessionsAreScopedToSelectedWorkspace(t *testing.T) {
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	projectA := t.TempDir()
	projectB := t.TempDir()
	createCLISessionFixture(t, projectA, "session-a", time.Now().Add(-time.Hour).UnixNano(), true)
	createCLISessionFixture(t, projectB, "session-b", time.Now().UnixNano(), true)

	metas, err := listCLISessions(home, projectA)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != "session-a" {
		t.Fatalf("workspace A sessions = %+v, want only session-a", metas)
	}
	if got, err := ResolveResumeID(home, projectA, "last"); err != nil || got != "session-a" {
		t.Fatalf("ResolveResumeID(last) = %q, %v", got, err)
	}
	if _, err := ResolveResumeID(home, projectA, "session-b"); err == nil {
		t.Fatal("cross-workspace explicit resume unexpectedly succeeded")
	}
}

func TestDeleteSessionRemovesExactClosedWorkspaceSession(t *testing.T) {
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	projectA := t.TempDir()
	projectB := t.TempDir()
	dbA := createCLISessionFixture(t, projectA, "same-id", 1, true)
	dbB := createCLISessionFixture(t, projectB, "same-id", 2, true)
	sessionDir, err := workspace.SessionDir(projectA, "same-id")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "draft.txt"), []byte("draft"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := DeleteSession(home, projectA, "same-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbA); !os.IsNotExist(err) {
		t.Fatalf("deleted workspace DB still exists: %v", err)
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("deleted workspace session directory still exists: %v", err)
	}
	if _, err := os.Stat(dbB); err != nil {
		t.Fatalf("other workspace session was touched: %v", err)
	}
	metas, err := listCLISessions(home, projectA)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("deleted registry row remains: %+v", metas)
	}
}

func TestDeleteSessionFailsClosed(t *testing.T) {
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	dbPath := createCLISessionFixture(t, project, "active", 1, false)

	if err := DeleteSession(home, project, "active"); err == nil {
		t.Fatal("active session deletion unexpectedly succeeded")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("active session storage changed: %v", err)
	}
	if err := DeleteSession(home, project, "../active"); err == nil {
		t.Fatal("unsafe session ID unexpectedly accepted")
	}
	if err := DeleteSession(home, project, "unknown"); err == nil {
		t.Fatal("unknown session deletion unexpectedly succeeded")
	}
}

func TestDeleteSessionRefusesLiveCoreAndRetainsRollbackBytes(t *testing.T) {
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	path := createCLISessionFixture(t, project, "rollback", 1, true)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := listenCore(context.Background(), DataDir(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(home, project, "rollback"); err == nil {
		_ = listener.Close()
		t.Fatal("live endpoint owner was bypassed")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	wdb, err := workspace.OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wdb.DB.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON workspace_sessions BEGIN SELECT RAISE(ABORT,'registry unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := wdb.Close(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(home, project, "rollback"); err == nil {
		t.Fatal("failed registry commit reported success")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, actual) {
		t.Fatal("failed delete lost original database")
	}
	metas, err := listCLISessions(home, project)
	if err != nil || len(metas) != 1 {
		t.Fatal("failed delete lost registry")
	}
}

func TestDeleteSessionKeepsRecoverableDatabaseAndSidecars(t *testing.T) {
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	path := createCLISessionFixture(t, project, "recoverable", 1, true)
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(path+suffix, []byte("sidecar"+suffix), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteSession(home, project, "recoverable"); err != nil {
		t.Fatal(err)
	}
	store, err := resolveCLISessionStore(home, project)
	if err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(store.sessionsDir), ".session-delete-*"))
	if err != nil || len(backups) != 1 {
		t.Fatal("missing recovery copy")
	}
	for _, name := range []string{"recovery.json", "recoverable.db", "recoverable.db-wal", "recoverable.db-shm"} {
		if _, err := os.Stat(filepath.Join(backups[0], name)); err != nil {
			t.Fatal(err)
		}
	}
}
