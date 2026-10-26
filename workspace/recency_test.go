package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWorkspaceCatalogBriefBusyPreservesCommittedMetadata(t *testing.T) {
	setTestHome(t)
	project := t.TempDir()
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := wdb.RecordAccess(t.Context(), time.Unix(0, 123)); err != nil {
		t.Fatal(err)
	}
	if err := wdb.RegisterSession("committed-session", project, "project", "", ""); err != nil {
		t.Fatal(err)
	}
	path := wdb.Path
	if err := wdb.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", fileURI(path)+"?_pragma=journal_mode(DELETE)&_pragma=locking_mode(EXCLUSIVE)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if _, err := writer.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = writer.Exec("ROLLBACK") }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	read := make(chan *WorkspaceInfo, 1)
	go func() { read <- readWorkspaceMetaBounded(ctx, path, 150*time.Millisecond) }()
	select {
	case info := <-read:
		t.Fatalf("brief SQLite contention erased committed catalog metadata: %+v", info)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := writer.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	// Exclusive connection mode retains the lock until the owner closes.
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case info := <-read:
		if info == nil || info.ProjectDir != canonicalizePath(project) || info.LastAccessed != 123 || info.SessionCount != 1 {
			t.Fatalf("catalog lost identity, recency or count after a bounded read-only retry: %+v", info)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func denseCatalogFixture(tb testing.TB) []string {
	tb.Helper()
	tb.Setenv("CRUDE_HOME", tb.TempDir())
	project := tb.TempDir()
	db, err := OpenWorkspaceDB(project)
	if err != nil {
		tb.Fatal(err)
	}
	path := db.Path
	if err := db.RegisterSession("catalog-session", project, "catalog project", "", ""); err != nil {
		tb.Fatal(err)
	}
	if err := db.Close(); err != nil {
		tb.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	projects := []string{canonicalizePath(project)}
	// Independent saved projects exercise read contention; no one fast database
	// may stand in for the identities whose bounded reads were lost.
	for i := range 31 {
		path := filepath.Join(filepath.Dir(filepath.Dir(path)), fmt.Sprintf("%016x", i), "workspace.db")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			tb.Fatal(err)
		}
		project := filepath.Join(project, fmt.Sprint(i))
		if err := os.MkdirAll(project, 0700); err != nil {
			tb.Fatal(err)
		}
		db, err := sql.Open("sqlite", fileURI(path))
		if err != nil {
			tb.Fatal(err)
		}
		_, err = db.Exec(`UPDATE workspace_metadata SET value = ? WHERE key = 'project_dir'`, project)
		closeErr := db.Close()
		if err != nil || closeErr != nil {
			tb.Fatalf("prepare catalog identity: %v %v", err, closeErr)
		}
		projects = append(projects, project)
	}
	return projects
}

func TestWorkspaceCatalogDenseReadDoesNotHideKnownProjects(t *testing.T) {
	projects := denseCatalogFixture(t)
	infos, err := ListWorkspaceInfos()
	if err != nil || len(infos) != len(projects) {
		t.Fatalf("bounded parallel reads hid committed catalog metadata: %+v, %v", infos, err)
	}
	found := make(map[string]bool, len(infos))
	for _, info := range infos {
		if info.SessionCount != 1 {
			t.Fatalf("catalog lost committed session count: %+v", info)
		}
		found[info.ProjectDir] = true
	}
	for _, project := range projects {
		if !found[project] {
			t.Fatalf("catalog hid %s", project)
		}
	}
}

func BenchmarkWorkspaceCatalogDenseRead(b *testing.B) {
	projects := denseCatalogFixture(b)
	b.ResetTimer()
	for b.Loop() {
		infos, err := ListWorkspaceInfos()
		if err != nil || len(infos) != len(projects) {
			b.Fatalf("catalog lost committed project: %+v, %v", infos, err)
		}
	}
}

func TestWorkspaceCatalogEscapesProfilePathWithoutWriting(t *testing.T) {
	names := []string{"profile#literal", "profile%20 + spaced"}
	if runtime.GOOS != "windows" {
		names = append(names, "profile?literal")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CRUDE_HOME", filepath.Join(t.TempDir(), name))
			project := t.TempDir()
			db, err := OpenWorkspaceDB(project)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.RecordAccess(t.Context(), time.Unix(0, 123)); err != nil {
				t.Fatal(err)
			}
			path := db.Path
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, read := range []func() *WorkspaceInfo{
				func() *WorkspaceInfo { return readWorkspaceMeta(t.Context(), path) },
				func() *WorkspaceInfo { return readWorkspaceMetaLight(t.Context(), path) },
			} {
				info := read()
				if info == nil || info.ProjectDir != canonicalizePath(project) || info.LastAccessed != 123 {
					t.Fatalf("catalog lost literal-path identity/recency: %+v", info)
				}
			}
			infos, err := ListWorkspaceInfos()
			if err != nil || len(infos) != 1 || infos[0].ProjectDir != canonicalizePath(project) {
				t.Fatalf("catalog omitted saved workspace: %+v, %v", infos, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("read-only catalog changed workspace database: %v", err)
			}
		})
	}
}

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
