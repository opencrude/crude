package core

import (
	"context"
	"crude/core/coreprotocol"
	"crude/insight/textindex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// Exercise the production feed with separate test sinks for delivery and LSP.
func testFileWatchFeed(publish func(coreprotocol.FileChange), notify func(context.Context, string, int), reliable func(bool)) fileWatchFeed {
	return fileWatchFeed{publish: publish, notify: notify, coverage: func(c watchCoverage) {
		if reliable != nil {
			reliable(c.complete())
		}
	}}
}
func startCoreFileWatch(root string, publish func(coreprotocol.FileChange), notify func(context.Context, string, int), reliable func(bool)) (*coreFileWatch, error) {
	return testFileWatchFeed(publish, notify, reliable).start(root, false, nil, nil)
}
func startDirectoryScope(root string, publish func(coreprotocol.FileChange), notify func(context.Context, string, int), reliable func(bool), analysis <-chan struct{}, index *textindex.Index) (*coreFileWatch, error) {
	return testFileWatchFeed(publish, notify, reliable).start(root, true, analysis, index)
}

func testWatchRoot(t *testing.T, path string) watchRoot {
	t.Helper()
	pin, err := pinWatchRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pin.handle.Close() })
	return pin
}

func TestCoreWatchWalkPreservesOrderAndSkip(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"branch", "ignored"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"branch/child", "ignored/child", "z"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var visited []string
	err := walkCoreWatch(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visited = append(visited, filepath.ToSlash(rel))
		if entry.IsDir() && rel == "ignored" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "branch", "branch/child", "ignored", "z"}
	if !slices.Equal(visited, want) {
		t.Fatalf("walk = %q, want %q", visited, want)
	}
}

func TestCoreFileWatchMetadataAndLifetime(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", ".claude/worktrees/ignored", "nested"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	events := make(chan coreprotocol.FileChange, 64)
	w, err := startCoreFileWatch(root, func(change coreprotocol.FileChange) { events <- change }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	select {
	case <-w.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("watch startup did not complete")
	}
	git := filepath.Join(root, ".git", "HEAD")
	if err := os.WriteFile(git, []byte("ref: refs/heads/main"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Path == git {
				if !event.Git {
					t.Fatal("git metadata lost")
				}
				w.Close()
				w.Close()
				return
			}
		case <-deadline.C:
			t.Fatal("no Git metadata feed")
		}
	}
}

func TestCoreFileWatchConcurrentCloseJoinsCallbacks(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{}, 1)
	w, err := startCoreFileWatch(root, nil, func(ctx context.Context, _ string, _ int) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-w.ready
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not start")
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Go(w.Close)
	}
	done := make(chan struct{})
	go func() { group.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent close did not join")
	}
	select {
	case <-entered:
		t.Fatal("callback ran after close")
	default:
	}
}

func TestCoreFileWatchSkipsWorktreesNotGit(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		path string
		skip bool
	}{{".claude/worktrees", true}, {".claude/worktrees/a/b", true}, {".claude/settings.json", false}, {".git/refs/heads", false}, {"worktrees/project", false}} {
		if got := skipCoreWatchPath(root, filepath.Join(root, test.path)); got != test.skip {
			t.Errorf("%s skip=%v", test.path, got)
		}
	}
}

func TestCoreFileWatchOrderedChangesAndCooperativeShutdown(t *testing.T) {
	root := t.TempDir()
	got := make(chan watchNotification, 32)
	started := make(chan struct{}, 1)
	w, err := startCoreFileWatch(root, nil, func(ctx context.Context, path string, kind int) {
		got <- watchNotification{path, kind}
		if kind == 3 {
			started <- struct{}{}
			<-ctx.Done()
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	wait := func(kind int) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case v := <-got:
				if v.path == path && v.kind == kind {
					return
				}
			case <-timer.C:
				t.Fatalf("missing kind %d", kind)
			}
		}
	}
	wait(1)
	if err := os.WriteFile(path, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(2)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	wait(3)
	<-started
	done := make(chan struct{})
	go func() { w.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel callback")
	}
}

// A populated directory moved into the project is observed without a gap:
// its watch is added and its files are reported as created. Moving it out is
// one delete. A gap would recheck the whole project and restart its servers.
func TestCoreFileWatchPopulatedMoveReportsSubtreeWithoutGap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(outside, "tree")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, "nested", "file")
	if err := os.WriteFile(path, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	gaps := make(chan bool, 16)
	changes := make(chan watchNotification, 64)
	w, err := startCoreFileWatch(root, nil, func(_ context.Context, p string, k int) { changes <- watchNotification{p, k} }, func(v bool) { gaps <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	if first, second := <-gaps, <-gaps; first || !second {
		t.Fatalf("startup reliability = %v, %v", first, second)
	}
	dest := filepath.Join(root, "tree")
	if err := os.Rename(src, dest); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dest, "nested", "file")
	awaitWatchChange(t, changes, gaps, moved, 1)
	if err := os.WriteFile(moved, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitWatchChange(t, changes, gaps, moved, 2)
	if err := os.Rename(dest, src); err != nil {
		t.Fatal(err)
	}
	awaitWatchChange(t, changes, gaps, dest, 3)
}

// awaitWatchChange waits for one notification and fails on any gap.
func awaitWatchChange(t *testing.T, changes <-chan watchNotification, gaps <-chan bool, path string, kind int) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case v := <-changes:
			if v.path == path && v.kind == kind {
				return
			}
		case v := <-gaps:
			if !v {
				t.Fatalf("an orderly directory change made a gap while waiting for %s", path)
			}
		case <-timer.C:
			t.Fatalf("no change %d for %s", kind, path)
		}
	}
}

// Found live: Python creating __pycache__, cargo creating target/ and a
// developer's mkdir each made a gap, and each gap rechecked every closed file
// and restarted the project's servers. Files written into a new directory
// before its watch was added are reported, not lost, and nothing is a gap.
func TestCoreFileWatchNewDirectoryIsNotAGap(t *testing.T) {
	root := t.TempDir()
	gaps := make(chan bool, 16)
	changes := make(chan watchNotification, 256)
	w, err := startCoreFileWatch(root, nil, func(_ context.Context, p string, k int) { changes <- watchNotification{p, k} }, func(v bool) { gaps <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	if first, second := <-gaps, <-gaps; first || !second {
		t.Fatalf("startup reliability = %v, %v", first, second)
	}
	dir := filepath.Join(root, "__pycache__", "deep")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(dir, "mod.pyc")
	if err := os.WriteFile(inner, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitWatchChange(t, changes, gaps, inner, 1)
	if err := os.RemoveAll(filepath.Join(root, "__pycache__")); err != nil {
		t.Fatal(err)
	}
	awaitWatchChange(t, changes, gaps, filepath.Join(root, "__pycache__"), 3)
}

// A directory that arrives with more entries than the bound (an unpacked
// archive or a package install) is still a gap: the producers reindex.
func TestCoreFileWatchLargeNewSubtreeIsAGap(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "big")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= coreWatchSubtreeLimit; i++ {
		if err := os.WriteFile(filepath.Join(outside, fmt.Sprintf("f-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	gaps := make(chan bool, 16)
	w, err := startCoreFileWatch(root, nil, func(context.Context, string, int) {}, func(v bool) { gaps <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	if first, second := <-gaps, <-gaps; first || !second {
		t.Fatalf("startup reliability = %v, %v", first, second)
	}
	if err := os.Rename(outside, filepath.Join(root, "big")); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-gaps:
		if v {
			t.Fatal("a large subtree restored observation without a gap first")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a subtree larger than the bound made no gap")
	}
}

func TestCoreFileWatchAdmissionFailureStaysUnreliable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	states := make(chan bool, 8)
	w, err := startCoreFileWatch(root, nil, nil, func(v bool) { states <- v })
	if err == nil {
		w.Close()
		t.Fatal("missing workspace admitted")
	}

	close(states)
	for state := range states {
		if state {
			t.Fatal("failed admission claimed restored observation")
		}
	}
}

// A build or package manager can remove a directory right after creating it,
// before its watch is added. That is not loss of the workspace: observation
// stays reliable and the next source change arrives.
func TestCoreFileWatchVanishedDirectoryKeepsObservation(t *testing.T) {
	root := t.TempDir()
	gaps := make(chan bool, 64)
	changes := make(chan watchNotification, 256)
	w, err := startCoreFileWatch(root, nil, func(_ context.Context, path string, kind int) {
		changes <- watchNotification{path, kind}
	}, func(reliable bool) { gaps <- reliable })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	if first, second := <-gaps, <-gaps; first || !second {
		t.Fatalf("startup reliability = %v, %v", first, second)
	}
	for i := 0; i < 20; i++ {
		transient := filepath.Join(root, fmt.Sprintf("transient-%d", i))
		if err := os.Mkdir(transient, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(transient); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "later.go")
	if err := os.WriteFile(path, []byte("package later\n"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitWatchChange(t, changes, gaps, path, 1)
}

func TestCoreFileWatchQueueSaturationRequestsRecovery(t *testing.T) {
	root := t.TempDir()
	states := make(chan bool, 8192)
	started := make(chan struct{}, 1)
	w, err := startCoreFileWatch(root, nil, func(ctx context.Context, _ string, _ int) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
	}, func(v bool) { states <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	if first, second := <-states, <-states; first || !second {
		t.Fatalf("startup states: %v %v", first, second)
	}
	for i := 0; i < coreWatchQueueSize*3; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f-%04d", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("notification did not start")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	falseCount := 0
	for {
		select {
		case v := <-states:
			if !v {
				falseCount++
			} else if falseCount >= 2 {
				return
			}
		case <-deadline.C:
			t.Fatalf("only %d saturated gaps recovered", falseCount)
		}
	}
}
