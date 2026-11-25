package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/workspace"
)

func TestGitTopicWaitDoesNotUseTheReadDeadline(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "core.hooksPath", ".git/hooks")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	gitDir := filepath.Join(root, ".git")
	hook := "#!/bin/sh\nset -eu\n: > .git/commit-hook-start\nwhile [ ! -f .git/commit-hook-release ]; do sleep 0.05; done\n"
	if err := os.WriteFile(filepath.Join(gitDir, "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	release := func() {
		if err := os.WriteFile(filepath.Join(gitDir, "commit-hook-release"), nil, 0o644); err != nil {
			t.Error(err)
		}
	}
	previous := stateTopicReadTimeout
	stateTopicReadTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { stateTopicReadTimeout = previous })
	ctx, cancel := context.WithCancel(t.Context())
	service := workspace.NewGitService(root)
	a := &actor{ctx: ctx, ws: &workspace.Workspace{Git: service}}
	a.startStateTopics()
	defer func() {
		cancel()
		a.states.wg.Wait()
	}()
	commit := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		commit <- service.CommitStaged(ctx, []string{"tracked.txt"}, "first")
	}()
	defer func() {
		release()
		select {
		case <-joined:
		case <-time.After(10 * time.Second):
			t.Error("commit did not join after releasing the hook")
		}
	}()
	wait := time.NewTimer(10 * time.Second)
	defer wait.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(gitDir, "commit-hook-start")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-commit:
			t.Fatalf("commit ended before the hook acquired the gate: %v", err)
		case <-wait.C:
			t.Fatal("pre-commit hook did not start")
		case <-tick.C:
		}
	}

	slot := a.states.slots[coreprotocol.TopicGit]
	_, wake := slot.hub.latest()
	a.queueState(coreprotocol.TopicGit)
	observation := time.NewTimer(2 * stateTopicReadTimeout)
	defer observation.Stop()
	select {
	case <-wake:
		entry, _ := slot.hub.latest()
		var state coreprotocol.WorkspaceState
		if err := coreprotocol.Decode(entry.body, &state); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("Git topic published before the repository lane was released: fault=%v", state.Fault)
	case <-observation.C:
	}
	// An invalidation burst while the gate is closed must not launch a farm
	// of readers or publish an obsolete generation.
	for range 5 {
		a.queueState(coreprotocol.TopicGit)
	}
	release()
	select {
	case err := <-commit:
		if err != nil {
			t.Fatalf("commit after releasing its hook: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("commit did not finish after releasing its hook")
	}
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("Git topic did not publish after the repository lane was released")
	}
	entry, _ := slot.hub.latest()
	var state coreprotocol.WorkspaceState
	if err := coreprotocol.Decode(entry.body, &state); err != nil {
		t.Fatal(err)
	}
	if state.Fault != nil || state.Git == nil || state.Git.Snapshot.Branch != "main" || state.Git.Snapshot.Head == "" {
		t.Fatalf("wanted the new Git snapshot after the lane cleared, got %+v (fault %v)", state.Git, state.Fault)
	}
	if state.Git.Snapshot.Sequence > 2 {
		t.Fatalf("queued refreshes did not coalesce: %d snapshot reads", state.Git.Snapshot.Sequence)
	}
}
