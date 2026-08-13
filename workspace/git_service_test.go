package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitServiceSnapshotAndIndexOperations(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required for mutation coverage")
	}
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name = "Crude Test"
	cfg.User.Email = "crude@example.test"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(root, "main.go")
	if err := os.WriteFile(tracked, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{
		Name: "Crude Test", Email: "crude@example.test", When: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(tracked, []byte("package main\nvar changed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewGitService(root)
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RepositoryRoot != root || len(snapshot.RecentCommits) != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	assertGitChange(t, snapshot.Changes, "main.go", GitAreaWorking)
	assertGitChange(t, snapshot.Changes, "new.txt", GitAreaUntracked)

	if err := service.Stage(context.Background(), "main.go"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertGitChange(t, snapshot.Changes, "main.go", GitAreaStaged)

	if err := service.Unstage(context.Background(), "main.go"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertGitChange(t, snapshot.Changes, "main.go", GitAreaWorking)

	if err := service.StageAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "source control test"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Changes) != 0 {
		t.Fatalf("changes after commit=%#v", snapshot.Changes)
	}
	if len(snapshot.RecentCommits) < 2 || snapshot.RecentCommits[0].Subject != "source control test" {
		t.Fatalf("recent commits=%#v", snapshot.RecentCommits)
	}
}

func assertGitChange(t *testing.T, changes []GitChange, path string, area GitChangeArea) {
	t.Helper()
	for _, change := range changes {
		if change.Path == path && change.Area == area {
			return
		}
	}
	t.Fatalf("missing change path=%q area=%v in %#v", path, area, changes)
}
