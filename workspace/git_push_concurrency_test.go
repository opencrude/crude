package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitPushFixture(t *testing.T, hook string) (root, origin string) {
	t.Helper()
	base := t.TempDir()
	root, origin = filepath.Join(base, "local"), filepath.Join(base, "origin.git")
	gitMust(t, base, "init", "--bare", "-b", "main", origin)
	gitMust(t, base, "init", "-b", "main", root)
	gitMust(t, root, "config", "core.hooksPath", ".git/hooks")
	gitMust(t, root, "remote", "add", "origin", origin)
	gitMust(t, root, "commit", "--allow-empty", "-m", "first")
	gitMust(t, root, "push", "-u", "origin", "main")
	gitMust(t, root, "branch", "other")
	gitMust(t, root, "commit", "--allow-empty", "-m", "publish me")
	if err := os.WriteFile(filepath.Join(root, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\n"+hook), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, origin
}

const blockGitPushHook = `set -eu
: > .git/push-hook-start
while [ ! -f .git/push-hook-release ]; do sleep 0.05; done
: > .git/push-hook-finished
`

func startGitPushHook(t *testing.T, root string, service *GitService, ctx context.Context) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- service.Push(ctx)
	}()
	t.Cleanup(func() {
		if err := os.WriteFile(filepath.Join(root, ".git", "push-hook-release"), nil, 0o644); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("push did not join after releasing the hook")
		}
	})
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(root, ".git", "push-hook-start")); err == nil {
			return result
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			t.Fatalf("push returned before the hook started: %v", err)
		case <-deadline.C:
			t.Fatal("pre-push hook did not start")
		case <-tick.C:
		}
	}
}

func TestGitSnapshotWhilePrePushHookRuns(t *testing.T) {
	root, origin := gitPushFixture(t, blockGitPushHook)
	service := NewGitService(root)
	result := startGitPushHook(t, root, service, t.Context())
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	snapshot, err := service.Snapshot(ctx)
	if err != nil || snapshot.RepositoryRoot == "" || snapshot.Branch != "main" || snapshot.Ahead != 1 {
		t.Fatalf("snapshot while the pre-push hook is blocked: branch=%q ahead=%d root=%q err=%v", snapshot.Branch, snapshot.Ahead, snapshot.RepositoryRoot, err)
	}
	wait, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stop()
	if err := service.Checkout(wait, "other"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("checkout changed the branch while push owned it: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "push-hook-release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("push after releasing its hook: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("push did not finish after releasing the hook")
	}
	gitMust(t, origin, "cat-file", "-e", snapshot.Head+"^{commit}")
}

func TestGitStopEndsPrePushHook(t *testing.T) {
	root, origin := gitPushFixture(t, blockGitPushHook)
	service := NewGitService(root)
	before, err := service.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := startGitPushHook(t, root, service, ctx)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stop returned %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not join the push and its pre-push hook")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "push-hook-finished")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the hook reached its release path: %v", err)
	}
	if err := runGit(t, origin, "cat-file", "-e", before.Head+"^{commit}"); err == nil {
		t.Fatal("the stopped push sent the commit to the remote")
	}
}

func TestGitPushReportsARefusalFromThePrePushHook(t *testing.T) {
	root, _ := gitPushFixture(t, "echo 'pre-push: fix the gate' >&2\nexit 1\n")
	err := NewGitService(root).Push(t.Context())
	if err == nil || !strings.Contains(err.Error(), "Push failed.") || !strings.Contains(err.Error(), "pre-push: fix the gate") {
		t.Fatalf("push hook refusal did not retain its output: %v", err)
	}
}
