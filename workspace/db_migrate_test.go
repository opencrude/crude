package workspace

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateWorkspaceSchemaUpgradesLegacyRegistry builds a pre-enrichment
// workspace_sessions table (missing project_name / turn_count / prompts),
// runs migrateWorkspaceSchema, and asserts RegisterSession succeeds.
func TestMigrateWorkspaceSchemaUpgradesLegacyRegistry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "workspace.db")

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	// Legacy schema from before project_name / turn_count / first_prompt /
	// last_prompt were added to CREATE TABLE (CREATE IF NOT EXISTS left old
	// tables untouched).
	_, err = raw.Exec(`
		CREATE TABLE workspace_sessions (
			session_id    TEXT PRIMARY KEY,
			project_dir   TEXT NOT NULL,
			model         TEXT NOT NULL DEFAULT '',
			provider      TEXT NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			closed_at     INTEGER NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			total_cost    REAL NOT NULL DEFAULT 0.0
		)
	`)
	if err != nil {
		_ = raw.Close()
		t.Fatalf("create legacy table: %v", err)
	}
	_ = raw.Close()

	wdb, err := openWorkspaceDBAt(dbPath)
	if err != nil {
		t.Fatalf("openWorkspaceDBAt (migrate): %v", err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	cols, err := tableColumns(wdb.DB, "workspace_sessions")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	for _, want := range []string{
		"project_name", "turn_count", "first_prompt", "last_prompt",
		"model", "provider", "closed_at", "message_count", "total_cost",
		"db_missing",
	} {
		if !cols[want] {
			t.Fatalf("missing column after migration: %s (have %v)", want, cols)
		}
	}

	if err := wdb.RegisterSession("legacy-sess-1", "/tmp/proj", "proj", "m", "p"); err != nil {
		t.Fatalf("RegisterSession after migration: %v", err)
	}
	var name string
	if err := wdb.DB.QueryRow(
		`SELECT project_name FROM workspace_sessions WHERE session_id = ?`, "legacy-sess-1",
	).Scan(&name); err != nil {
		t.Fatalf("read project_name: %v", err)
	}
	if name != "proj" {
		t.Fatalf("project_name = %q, want proj", name)
	}
}

func TestMigrateWorkspaceSchemaIdempotentOnCurrentAndFresh(t *testing.T) {
	// Fresh DB: open twice; second open must be a no-op.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "workspace.db")
	wdb, err := openWorkspaceDBAt(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := wdb.RegisterSession("s1", "/p", "p", "m", "prov"); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	_ = wdb.Close()

	wdb2, err := openWorkspaceDBAt(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = wdb2.Close() })
	metas, err := wdb2.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "s1" {
		t.Fatalf("ListSessions after re-open: %+v", metas)
	}
}
