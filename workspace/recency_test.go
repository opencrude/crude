package workspace

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecentWorkspacesOrderByAccessNotSessionsOrCatalogReads(t *testing.T) {
	setTestHome(t)
	a, b := t.TempDir(), t.TempDir()
	da, err := OpenWorkspaceDB(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = da.Close() })
	db, err := OpenWorkspaceDB(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, step := range []struct {
		db *WorkspaceDB
		at int64
	}{{da, 100}, {db, 200}, {da, 300}, {da, 50}} {
		if err := step.db.RecordAccess(t.Context(), time.Unix(0, step.at)); err != nil {
			t.Fatal(err)
		}
	}
	// New AI work in the other workspace must not make it recently visited.
	if err := db.RegisterSession("new-background-session", b, "background", "", ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		infos, err := ListWorkspaceInfos()
		if err != nil {
			t.Fatal(err)
		}
		if len(infos) != 2 || infos[0].ProjectDir != canonicalizePath(a) || infos[0].LastAccessed != 300 || infos[1].ProjectDir != canonicalizePath(b) || infos[1].LastAccessed != 200 {
			t.Fatalf("wrong access order: %+v", infos)
		}
	}
	light := readWorkspaceMetaLight(t.Context(), filepath.Join(db.Dir, "workspace.db"))
	if light == nil || light.LastAccessed != 200 {
		t.Fatalf("light metadata lost recency: %+v", light)
	}
}
