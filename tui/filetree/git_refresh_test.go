package filetree

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitRefreshAppliesAndClearsEveryLoadedNode(t *testing.T) {
	root, _, worktree, tracked := newFileTreeGitRepo(t)
	model := NewFileTreeModel(root)

	if err := os.WriteFile(tracked, []byte("package main\nvar changed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(root, "new.go")
	if err := os.WriteFile(untracked, []byte("package new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model.ApplyLocalMutation(FileChangeEvent{Type: FileCreated, Path: untracked})
	applyGitRefreshCommand(t, model)

	if got := model.NodeByPath(tracked).GitStatus; got != GitModified {
		t.Fatalf("tracked status=%v, want modified", got)
	}
	if got := model.NodeByPath(untracked).GitStatus; got != GitUntracked {
		t.Fatalf("untracked status=%v, want untracked", got)
	}

	if _, err := worktree.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("new.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("commit changes", &git.CommitOptions{Author: testGitSignature()}); err != nil {
		t.Fatal(err)
	}
	applyGitRefreshCommand(t, model)

	for _, path := range []string{tracked, untracked} {
		if got := model.NodeByPath(path).GitStatus; got != GitNone {
			t.Fatalf("status after commit for %s=%v, want none", filepath.Base(path), got)
		}
	}
	if model.Root.AggregateStatus != GitNone {
		t.Fatalf("root aggregate after commit=%v, want none", model.Root.AggregateStatus)
	}
}

func TestGitResolverFindsRepositoryAboveWorkspaceRoot(t *testing.T) {
	root, _, _, _ := newFileTreeGitRepo(t)
	subdir := filepath.Join(root, "pkg")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(subdir, "nested.go")
	if err := os.WriteFile(path, []byte("package pkg\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := NewFileTreeModel(subdir)
	resolver := model.GitResolver()
	if resolver == nil || !resolver.Available() {
		t.Fatal("nested workspace should resolve its containing repository")
	}
	if resolver.RepoPath() != root {
		t.Fatalf("repository root=%q, want %q", resolver.RepoPath(), root)
	}
	applyGitRefreshCommand(t, model)
	if got := model.NodeByPath(path).GitStatus; got != GitUntracked {
		t.Fatalf("nested file status=%v, want untracked", got)
	}
}

func TestGitRefreshCoalescesOneTrailingScan(t *testing.T) {
	root, _, _, _ := newFileTreeGitRepo(t)
	model := NewFileTreeModel(root)
	first := model.RefreshGitStatus()
	if first == nil {
		t.Fatal("first refresh should start a scan")
	}
	if second := model.RefreshGitStatus(); second != nil {
		t.Fatal("overlapping refresh should be coalesced")
	}

	msg, ok := first().(TreeGitRefreshMsg)
	if !ok {
		t.Fatal("first refresh returned the wrong message type")
	}
	trailing := model.Update(msg)
	if trailing == nil {
		t.Fatal("coalesced request must schedule one trailing scan")
	}
	trailingMsg, ok := trailing().(TreeGitRefreshMsg)
	if !ok {
		t.Fatal("trailing refresh returned the wrong message type")
	}
	if next := model.Update(trailingMsg); next != nil {
		t.Fatal("coalesced burst should finish after one trailing scan")
	}
}

func TestGitMetadataWatcherSignalsIndexAndCommitChanges(t *testing.T) {
	root, _, worktree, tracked := newFileTreeGitRepo(t)
	model := NewFileTreeModel(root)
	changed := make(chan struct{}, 4)
	model.SetOnGitChanged(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err := model.StartWatcher(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(model.StopWatcher)

	if err := os.WriteFile(tracked, []byte("package main\nvar staged = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	waitForGitMetadataSignal(t, changed, "index update")
	drainGitSignals(changed)

	if _, err := worktree.Commit("watch commit", &git.CommitOptions{Author: testGitSignature()}); err != nil {
		t.Fatal(err)
	}
	waitForGitMetadataSignal(t, changed, "commit ref update")
}

func newFileTreeGitRepo(t *testing.T) (string, *git.Repository, *git.Worktree, string) {
	t.Helper()
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(root, "main.go")
	if err := os.WriteFile(tracked, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("initial", &git.CommitOptions{Author: testGitSignature()}); err != nil {
		t.Fatal(err)
	}
	return root, repo, worktree, tracked
}

func testGitSignature() *object.Signature {
	return &object.Signature{Name: "Crude Test", Email: "crude@example.test", When: time.Now()}
}

func applyGitRefreshCommand(t *testing.T, model *FileTreeModel) {
	t.Helper()
	cmd := model.RefreshGitStatus()
	if cmd == nil {
		t.Fatal("expected Git refresh command")
	}
	raw := cmd()
	msg, ok := raw.(TreeGitRefreshMsg)
	if !ok {
		t.Fatalf("refresh result type=%T, want TreeGitRefreshMsg", raw)
	}
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	for next := model.Update(msg); next != nil; {
		rawNext := next()
		nextMsg, nextOK := rawNext.(TreeGitRefreshMsg)
		if !nextOK {
			t.Fatalf("coalesced result type=%T, want TreeGitRefreshMsg", rawNext)
		}
		if nextMsg.Err != nil {
			t.Fatal(nextMsg.Err)
		}
		next = model.Update(nextMsg)
	}
}

func waitForGitMetadataSignal(t *testing.T, changed <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatalf("no Git metadata signal after %s", operation)
	}
}

func drainGitSignals(changed <-chan struct{}) {
	for {
		select {
		case <-changed:
		default:
			return
		}
	}
}
