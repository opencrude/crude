package localhistory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Like Git, an ignore file inside an ignored directory cannot bring its
// contents back, and the walk that reads ignore files does not enter such a
// directory. Ignore files of visited directories still apply, deepest last,
// and Crude's own patterns override them.
func TestIgnoreFilesInsideIgnoredDirectoriesAreNotRead(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, ".gitignore", []byte("generated/\n"), 0o644)
	writeTestFile(t, workspace, "generated/.gitignore", []byte("!keep.txt\n"), 0o644)
	writeTestFile(t, workspace, "generated/keep.txt", []byte("derived"), 0o644)
	writeTestFile(t, workspace, "src/.gitignore", []byte("*.log\n!important.log\n"), 0o644)
	writeTestFile(t, workspace, "src/main.go", []byte("package main"), 0o644)
	writeTestFile(t, workspace, "src/debug.log", []byte("noise"), 0o644)
	writeTestFile(t, workspace, "src/important.log", []byte("evidence"), 0o644)
	writeTestFile(t, workspace, ".crudehistoryignore", []byte("src/important.log\n"), 0o644)
	for _, rel := range []string{"generated", "generated/keep.txt", "src/debug.log", "src/important.log"} {
		if ignored, err := store.ShouldIgnore(rel, rel == "generated", SnapshotOptions{}); err != nil || !ignored {
			t.Fatalf("%s ignored=%v err=%v, want ignored", rel, ignored, err)
		}
	}
	manifest, err := store.Snapshot(context.Background(), SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"generated/keep.txt", "src/debug.log", "src/important.log"} {
		if _, ok := manifest.Entries[rel]; ok {
			t.Fatalf("snapshot captured ignored %s", rel)
		}
	}
	if _, ok := manifest.Entries["src/main.go"]; !ok {
		t.Fatal("snapshot lost a visible file")
	}
}

// Captures that wait while the store is busy are served by one later scan,
// and none is lost when the caller that holds the store stops waiting.
func TestWaitingCapturesShareOneLaterScan(t *testing.T) {
	store, workspace := newTestStore(t)
	agedFile(t, workspace, "a.txt", "alpha", time.Hour)
	if _, _, err := store.SnapshotIfChanged(context.Background(), SnapshotOptions{}); err != nil {
		t.Fatal(err)
	}
	agedFile(t, workspace, "b.txt", "beta", time.Hour)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	type result struct {
		manifest Manifest
		changed  bool
		err      error
	}
	results := make([]result, 4)
	store.mu.Lock() // another capture holds the store
	var wg sync.WaitGroup
	for i := range results {
		ctx := context.Background()
		if i == 0 {
			ctx = cancelled
		}
		wg.Go(func() {
			manifest, changed, err := store.SnapshotIfChanged(ctx, SnapshotOptions{Label: fmt.Sprint("request ", i)})
			results[i] = result{manifest, changed, err}
		})
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		store.capturesMu.Lock()
		waiting := len(store.captures)
		store.capturesMu.Unlock()
		if waiting == len(results) {
			break
		}
		if time.Now().After(deadline) {
			store.mu.Unlock()
			t.Fatalf("%d of %d captures queued", waiting, len(results))
		}
		time.Sleep(time.Millisecond)
	}
	store.mu.Unlock()
	wg.Wait()

	var id string
	changed := 0
	for i, got := range results[1:] {
		if got.err != nil {
			t.Fatalf("request %d failed: %v", i+1, got.err)
		}
		if _, ok := got.manifest.Entries["b.txt"]; !ok {
			t.Fatalf("request %d missed a change made before it asked", i+1)
		}
		if id == "" {
			id = got.manifest.ID
		} else if got.manifest.ID != id {
			t.Fatalf("waiting requests got different snapshots %s and %s", id, got.manifest.ID)
		}
		if got.changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("%d requests recorded the change, want exactly one", changed)
	}
	if results[0].err == nil && results[0].manifest.ID != id {
		t.Fatal("the stopped request got a different snapshot")
	}
	records, err := store.Timeline(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("timeline has %d snapshots, want the first and one shared capture", len(records))
	}
	store.capturesMu.Lock()
	defer store.capturesMu.Unlock()
	if len(store.captures) != 0 {
		t.Fatalf("%d captures were left waiting", len(store.captures))
	}
}
