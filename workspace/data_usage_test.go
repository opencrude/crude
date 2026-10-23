package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/metrics"
)

func waitDataUsage(t *testing.T, u *DataUsage) *metrics.DiskUsage {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := u.Usage()
		if !got.Measuring && !got.MeasuredAt.IsZero() {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatal("data usage never finished measuring")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The data folder is measured in the background: per part, per workspace
// (sessions, history, worktrees) and the workspaces whose project folder is
// gone. Usage never blocks; a fresh measurement is reused until it is stale.
func TestDataUsageMeasuresPartsWorkspacesAndDeletedProjects(t *testing.T) {
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, buildinfo.DataDir())
	write := func(path string, size int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kept, gone := t.TempDir(), filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{kept, gone} {
		db, err := OpenWorkspaceDB(project)
		if err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
	keptDir := filepath.Join(dataDir, "workspaces", WorkspaceID(kept))
	write(filepath.Join(keptDir, "sessions", "a.db"), 6000)
	write(filepath.Join(keptDir, "local-history", "objects", "x"), 3000)
	write(filepath.Join(keptDir, "executions", "wt", "file"), 2000)
	write(filepath.Join(dataDir, "workspaces", WorkspaceID(gone), "sessions", "b.db"), 1000)
	write(filepath.Join(dataDir, "backups", "old.db"), 500)
	write(filepath.Join(dataDir, "config.json"), 10)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	usage := NewDataUsage(dataDir)
	if first := usage.Usage(); !first.Measuring || !first.MeasuredAt.IsZero() {
		t.Fatalf("first read should start measuring without blocking: %+v", first)
	}
	got := waitDataUsage(t, usage)
	if got.WorkspaceCount != 2 || len(got.Workspaces) != 2 || got.Parts[0].Name != "Workspaces" {
		t.Fatalf("usage = %+v", got)
	}
	largest := got.Workspaces[0]
	if largest.Project != kept || largest.Missing || largest.Sessions < 6000 || largest.History != 3000 || largest.Worktrees != 2000 {
		t.Fatalf("kept workspace = %+v", largest)
	}
	if !got.Workspaces[1].Missing || got.Orphaned != 1 || got.OrphanedBytes != got.Workspaces[1].Bytes {
		t.Fatalf("deleted project not reported: %+v orphaned=%d/%d", got.Workspaces[1], got.Orphaned, got.OrphanedBytes)
	}
	var parts int64
	for _, part := range got.Parts {
		parts += part.Bytes
	}
	if parts != got.Total || got.DeviceTotal <= 0 {
		t.Fatalf("parts %d != total %d, device %d", parts, got.Total, got.DeviceTotal)
	}
	if again := usage.Usage(); again.Measuring || !again.MeasuredAt.Equal(got.MeasuredAt) {
		t.Fatalf("a fresh measurement was not reused: %+v", again)
	}
}
