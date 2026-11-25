package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/types/setting"
	"crude/workspace"
)

func coreGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.test", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// Verify the effect at the destination, not just the shell's push message.
func TestConnectedPushHonorsHookAndStop(t *testing.T) {
	base := t.TempDir()
	origin, root := filepath.Join(base, "origin.git"), filepath.Join(base, "work")
	coreGit(t, base, "init", "--bare", "-b", "main", origin)
	coreGit(t, base, "init", "-b", "main", root)
	coreGit(t, root, "config", "core.hooksPath", ".git/hooks")
	coreGit(t, root, "remote", "add", "origin", origin)
	coreGit(t, root, "commit", "--allow-empty", "-m", "base")
	coreGit(t, root, "push", "-u", "origin", "main")
	ref := func(dir string) string {
		t.Helper()
		output, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "--verify", "refs/heads/main").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	before := ref(origin)
	coreGit(t, root, "commit", "--allow-empty", "-m", "checked")
	want := ref(root)
	gitDir := filepath.Join(root, ".git")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(gitDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("hooks/pre-push", `#!/bin/sh
set -eu
: > .git/hook-entered
while [ ! -f .git/hook-release ]; do sleep 0.05; done
if [ ! -f .git/check-passed ]; then echo "the local checks failed" >&2; exit 1; fi
`)
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", setting.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(base, "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	client := controlWireClient(t, r, "push-hook")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	git := coreclient.NewGitService(client, attached.WorkspaceID)
	push, stop := context.WithCancel(ctx)
	defer stop()
	t.Cleanup(func() { write("hook-release", "") })
	done := make(chan error, 1)
	go func() { done <- git.Push(push) }()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(gitDir, "hook-entered")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("push ended before the hook ran: %v", err)
		case <-ctx.Done():
			t.Fatal("hook did not start")
		case <-tick.C:
		}
	}
	if snap, err := git.Snapshot(ctx); err != nil || snap.Ahead != 1 || snap.Head != want {
		t.Fatalf("cannot read the pending commit while the hook runs: %+v, %v", snap, err)
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) || ref(origin) != before {
		t.Fatalf("Stop did not cancel the push before publication: %v", err)
	}
	write("hook-release", "")
	if err := git.Push(ctx); err == nil || !strings.Contains(err.Error(), "the local checks failed") || ref(origin) != before {
		t.Fatalf("expected the hook to reject the push: %v", err)
	}
	write("check-passed", "")
	if err := git.Push(ctx); err != nil || ref(origin) != want {
		t.Fatalf("ref did not reach the checked commit: %v", err)
	}
}

// A connected shell resolves a pull conflict end to end through the core's
// service RPCs; every repository-changing call is journaled as a mutation.
func TestGitRemoteAndConflictServicesOverCore(t *testing.T) {
	base := t.TempDir()
	origin, theirs, ours := filepath.Join(base, "origin.git"), filepath.Join(base, "theirs"), filepath.Join(base, "ours")
	coreGit(t, base, "init", "--bare", "-b", "main", origin)
	coreGit(t, base, "clone", origin, theirs)
	write := func(dir, text string) {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(theirs, "x\n")
	coreGit(t, theirs, "add", ".")
	coreGit(t, theirs, "commit", "-m", "base")
	coreGit(t, theirs, "push", "-u", "origin", "main")
	coreGit(t, base, "clone", origin, ours)
	for _, dir := range []string{theirs, ours} {
		coreGit(t, dir, "config", "user.name", "T")
		coreGit(t, dir, "config", "user.email", "t@example.test")
	}
	write(theirs, "theirs\n")
	coreGit(t, theirs, "commit", "-am", "theirs")
	coreGit(t, theirs, "push")
	write(ours, "ours\n")
	coreGit(t, ours, "commit", "-am", "ours")

	ws := workspace.NewWorkspace(ours, "", "tester", setting.Settings{}, coreTestLogger{})
	defer ws.Close()
	if ws.Git == nil {
		ws.Git = workspace.NewGitService(ours)
	}
	if ws.LocalHistory == nil {
		t.Skip("Local History is not configured for this workspace")
	}
	a := &actor{ws: ws}
	call := func(method string, in, out any) {
		t.Helper()
		raw, _ := testPayload(in)
		run, ok := a.prepareService(coreprotocol.Request{ID: method, Method: method, Payload: raw})
		if !ok {
			t.Fatalf("%s is not routed", method)
		}
		response := run(context.Background())
		if response.Error != nil {
			t.Fatalf("%s: %+v", method, response.Error)
		}
		if out != nil {
			if err := testDecode(response.Payload, out); err != nil {
				t.Fatal(err)
			}
		}
		if method != coreprotocol.GitConflictFile && !serviceMutation(method) {
			t.Fatalf("%s changes the repository but is not journaled", method)
		}
	}
	var pulled coreprotocol.GitRemoteResult
	call(coreprotocol.GitPull, coreprotocol.GitRemoteRequest{}, &pulled)
	if len(pulled.Sync.Conflicts) != 1 {
		t.Fatalf("pull = %+v", pulled)
	}
	var file coreprotocol.GitConflictFileResult
	call(coreprotocol.GitConflictFile, coreprotocol.GitConflictFileRequest{Path: "a.txt"}, &file)
	call(coreprotocol.ResolveGitConflict, coreprotocol.ResolveGitConflictRequest{Path: "a.txt", Fingerprint: file.File.Fingerprint, Choices: []workspace.GitConflictChoice{workspace.GitChooseIncoming}}, nil)
	call(coreprotocol.GitContinueOperation, coreprotocol.GitRemoteRequest{}, nil)
	call(coreprotocol.GitPush, coreprotocol.GitRemoteRequest{}, nil)
	if data, _ := os.ReadFile(filepath.Join(ours, "a.txt")); strings.TrimSpace(string(data)) != "theirs" {
		t.Fatalf("resolved = %q", data)
	}
}
