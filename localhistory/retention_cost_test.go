package localhistory

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Retention runs after every capture and on every reconcile tick while it
// holds the gate that turns and file edits wait on. Re-reading every protected
// snapshot's content on each pass held that gate for over a minute in a real
// store; one proof per Store must serve later passes.
func TestRetentionDoesNotRereadProtectedEvidenceEachPrune(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that deny the test user reads")
	}
	store, _ := newTestStore(t)
	_, after := retentionExecutionScope(t, store, "execution_apply", "committed")
	retentionSnapshot(t, store, "latest\n")
	policy := RetentionPolicy{KeepHours: 24, MaxSnapshots: 500}
	if _, err := store.Prune(context.Background(), policy); err != nil {
		t.Fatal(err)
	}

	object, err := store.readFileObject(after.Entries["execution.txt"].Hash)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := objectPath(store.root, "chunks", object.Chunks[0].Hash, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(chunk, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(chunk, 0o600) })

	if _, err := store.Prune(context.Background(), policy); err != nil {
		t.Fatalf("a later prune re-read protected content it already verified: %v", err)
	}
	if err := store.Verify(context.Background(), after.ID); err == nil {
		t.Fatal("explicit Verify trusted a cached proof instead of reading content")
	}

	// A removed manifest still fails closed instead of being trusted.
	if err := os.Chmod(chunk, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.root, "manifests", after.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prune(context.Background(), policy); err == nil {
		t.Fatal("prune accepted protected evidence whose manifest disappeared")
	}
}

// Pruned snapshots leave reusable SQLite free pages and a WAL behind. Counting
// them against the storage budget kept a store whose history fit its budget
// permanently over it, and each prune then discarded all ordinary history.
func TestRetentionBudgetIgnoresReusableMetadataSpace(t *testing.T) {
	store, workspace := newTestStore(t)
	ctx := context.Background()
	for i := range 400 {
		writeTestFile(t, workspace, fmt.Sprintf("tree/file-%03d.txt", i), []byte("same\n"), 0o644)
	}
	for i := range 30 {
		writeTestFile(t, workspace, "tree/changing.txt", []byte(fmt.Sprintf("revision %d\n", i)), 0o644)
		if _, err := store.Snapshot(ctx, SnapshotOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1}); err != nil {
		t.Fatal(err)
	}
	var freePages int
	if err := store.db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
		t.Fatal(err)
	}
	if freePages == 0 {
		t.Fatal("fixture left no reusable metadata space")
	}

	// Recent ordinary history the user can still restore.
	var recent []Manifest
	for i := range 3 {
		writeTestFile(t, workspace, "tree/changing.txt", []byte(fmt.Sprintf("kept %d\n", i)), 0o644)
		manifest, err := store.Snapshot(ctx, SnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		recent = append(recent, manifest)
	}
	var onDisk int64
	if err := filepath.WalkDir(store.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err == nil {
			onDisk += info.Size()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.PhysicalBytes >= onDisk {
		t.Fatalf("stored bytes %d count reusable metadata space (on disk %d)", stats.PhysicalBytes, onDisk)
	}

	// The budget admits the retained content, not the free pages around it.
	result, err := store.Prune(ctx, RetentionPolicy{KeepHours: 24, BudgetBytes: onDisk - 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedSnapshots != 0 {
		t.Fatalf("budget pruned %d snapshots of history that fit it", result.RemovedSnapshots)
	}
	for _, manifest := range recent {
		if _, err := store.LoadManifest(manifest.ID); err != nil {
			t.Fatalf("recent history %s was pruned: %v", manifest.ID, err)
		}
	}
}
