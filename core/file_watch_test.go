package core

import (
	"context"
	"crude/coreprotocol"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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

func TestCoreFileWatchPopulatedMoveSignalsGapAndWatchesSubtree(t *testing.T) {
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
	select {
	case v := <-gaps:
		if v {
			t.Fatal("gap marked reliable")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing move-in gap")
	}
	// Wait for the directory event, which follows subtree registration.
	for {
		select {
		case v := <-changes:
			if v.path == dest {
				goto registered
			}
		case <-time.After(3 * time.Second):
			t.Fatal("directory not registered")
		}
	}
registered:
	path = filepath.Join(dest, "nested", "file")
	if err := os.WriteFile(path, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case v := <-changes:
			if v.path == path && v.kind == 2 {
				goto observed
			}
		case <-time.After(3 * time.Second):
			t.Fatal("nested change not observed")
		}
	}
observed:
	if err := os.Rename(dest, src); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case v := <-gaps:
			if !v {
				return
			}
		case <-timer.C:
			t.Fatal("missing move-out gap")
		}
	}
}

func TestCoreFileWatchAdmissionFailureStaysUnreliable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	states := make(chan bool, 8)
	w, err := startCoreFileWatch(root, nil, nil, func(v bool) { states <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.ready
	w.Close()
	close(states)
	for state := range states {
		if state {
			t.Fatal("failed admission claimed restored observation")
		}
	}
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
