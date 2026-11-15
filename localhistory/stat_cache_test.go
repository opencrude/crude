package localhistory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func agedFile(t testing.TB, root, rel, content string, age time.Duration) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func snapshotEntry(t *testing.T, store *Store, rel string) Entry {
	t.Helper()
	manifest, _, err := store.SnapshotIfChanged(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Entries[rel]
}

// The stat cache must never hide a content change: a rewrite that changes
// metadata, a same-size rewrite in the racy window and (on Linux) a rewrite
// that restores the old modification time are all observed.
func TestStatCacheObservesEveryContentChange(t *testing.T) {
	store, workspace := newTestStore(t)
	agedFile(t, workspace, "a.txt", "alpha", time.Hour)
	first := snapshotEntry(t, store, "a.txt")

	agedFile(t, workspace, "a.txt", "omega", 30*time.Minute) // same size, new mtime
	if got := snapshotEntry(t, store, "a.txt"); got.Hash == first.Hash {
		t.Fatal("same-size rewrite with a new modification time was missed")
	}

	path := filepath.Join(workspace, "a.txt")
	if err := os.WriteFile(path, []byte("gamma"), 0o644); err != nil { // racy: just written
		t.Fatal(err)
	}
	racy := snapshotEntry(t, store, "a.txt")
	if err := os.WriteFile(path, []byte("delta"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := snapshotEntry(t, store, "a.txt"); got.Hash == racy.Hash {
		t.Fatal("rewrite inside the racy window was missed")
	}

	if runtime.GOOS == "linux" {
		agedFile(t, workspace, "b.txt", "one", time.Hour)
		before := snapshotEntry(t, store, "b.txt")
		info, err := os.Stat(filepath.Join(workspace, "b.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "b.txt"), []byte("two"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(workspace, "b.txt"), info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		if got := snapshotEntry(t, store, "b.txt"); got.Hash == before.Hash {
			t.Fatal("mtime-preserving rewrite was missed")
		}
	}
}

// A cached identity is trusted only while its object still exists; a pruned
// or damaged store is repaired by re-reading the file.
func TestStatCacheRestoresMissingObject(t *testing.T) {
	store, workspace := newTestStore(t)
	agedFile(t, workspace, "keep.txt", "durable content", time.Hour)
	entry := snapshotEntry(t, store, "keep.txt")
	path, err := objectPath(store.root, "files", entry.Hash, ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	agedFile(t, workspace, "other.txt", "forces a new manifest", time.Hour)
	_ = snapshotEntry(t, store, "keep.txt")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("object was not stored again: %v", err)
	}
}

func BenchmarkSnapshotIfChangedUnchangedTree(b *testing.B) {
	workspace := b.TempDir()
	store, err := Open(b.TempDir(), workspace)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 2000; i++ {
		agedFile(b, workspace, fmt.Sprintf("src/%02d/%04d.go", i%40, i), fmt.Sprintf("package p\n// %d %s\n", i, string(make([]byte, 4096))), time.Hour)
	}
	if _, _, err := store.SnapshotIfChanged(context.Background(), SnapshotOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, changed, err := store.SnapshotIfChanged(context.Background(), SnapshotOptions{}); err != nil || changed {
			b.Fatalf("changed=%v err=%v", changed, err)
		}
	}
}

// The stat cache outlives a core restart, so the first capture of the next
// Store reads only changed files. A change made while no Store was open is
// still observed, and a damaged cache file is ignored.
func TestStatCacheSurvivesReopenWithoutHidingChanges(t *testing.T) {
	storeRoot, workspace := t.TempDir(), t.TempDir()
	store, err := Open(storeRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	agedFile(t, workspace, "same.txt", "unchanged", time.Hour)
	agedFile(t, workspace, "edit.txt", "before", time.Hour)
	first := snapshotEntry(t, store, "edit.txt")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	agedFile(t, workspace, "edit.txt", "after!", 30*time.Minute)

	store, err = Open(storeRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "same.txt")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := fileIdentityKey(path, info)
	if _, cached := store.cachedEntry("same.txt", info, identity, true); !cached {
		t.Fatal("the reopened store must reuse the saved entry of an unchanged file")
	}
	if got := snapshotEntry(t, store, "edit.txt"); got.Hash == first.Hash {
		t.Fatal("a change made while the store was closed was missed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(storeRoot, statCacheFile), []byte("{damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err = Open(storeRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, cached := store.cachedEntry("same.txt", info, identity, true); cached {
		t.Fatal("a damaged cache file was trusted")
	}
	if got := snapshotEntry(t, store, "same.txt"); got.Hash == "" {
		t.Fatal("capture failed after a damaged cache file")
	}
}
