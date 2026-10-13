package workspace

import (
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

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
