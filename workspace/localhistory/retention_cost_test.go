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

// The index keeps only which paths each snapshot contains, clustered by
// snapshot. A full copy of every entry per snapshot grew a real store to
// 787 MB and made retention hold the store's only connection for 16 s or more.
// Many snapshots of a large tree must stay small, and pruning must remove
// their index rows.
func TestSnapshotIndexStaysProportionalToSnapshots(t *testing.T) {
	store, workspace := newTestStore(t)
	ctx := context.Background()
	const files, snapshots = 2000, 30
	for i := range files {
		writeTestFile(t, workspace, fmt.Sprintf("tree/dir-%02d/file-with-a-realistic-name-%04d.go", i%40, i), []byte("same\n"), 0o644)
	}
	for i := range snapshots {
		writeTestFile(t, workspace, "tree/changing.txt", []byte(fmt.Sprintf("revision %d\n", i)), 0o644)
		if _, err := store.Snapshot(ctx, SnapshotOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	metadataBytes := func() int64 {
		t.Helper()
		var pages, free, size int64
		if err := store.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`PRAGMA page_size`).Scan(&size); err != nil {
			t.Fatal(err)
		}
		return (pages - free) * size
	}
	var links int64
	if err := store.db.QueryRow(`SELECT count(*) FROM history_snapshot_paths`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if want := int64(snapshots * (files + 1)); links < want {
		t.Fatalf("indexed %d snapshot paths, want at least %d", links, want)
	}
	// About 60,000 snapshot paths. Full entry rows took about 550 bytes per
	// snapshot path in the real store: over 30 MiB for this history.
	if used := metadataBytes(); used > 3<<20 {
		t.Fatalf("snapshot index uses %d bytes for %d snapshot paths", used, links)
	}
	records, err := store.FileHistory("tree/changing.txt", 100)
	if err != nil || len(records) != snapshots {
		t.Fatalf("file history = %d records, %v; want %d", len(records), err, snapshots)
	}
	if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM history_snapshot_paths`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	latest, err := store.Timeline(0)
	if err != nil || len(latest) != 1 {
		t.Fatalf("retention kept %d snapshots, %v; want 1", len(latest), err)
	}
	manifest, err := store.LoadManifest(latest[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if links != int64(len(manifest.Entries)) {
		t.Fatalf("pruned snapshots left %d index rows; the kept snapshot has %d paths", links, len(manifest.Entries))
	}
	if records, err := store.FileHistory("tree/changing.txt", 100); err != nil || len(records) != 1 {
		t.Fatalf("file history after retention = %d records, %v; want 1", len(records), err)
	}
}
