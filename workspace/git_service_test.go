package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if len(snapshot.Branches) != 1 || snapshot.Branches[0].Name == "" || snapshot.Branches[0].Remote {
		t.Fatalf("local-only branches=%#v", snapshot.Branches)
	}

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

func TestGitServiceListsRemoteBranchesAndChecksThemOut(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required for mutation coverage")
	}
	origin := t.TempDir()
	originRepo, err := git.PlainInit(origin, false)
	if err != nil {
		t.Fatal(err)
	}
	setGitIdentity(t, originRepo)
	wt, err := originRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "main.go"), []byte("package main\n"), 0o600); err != nil {
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
	if err := runGit(t, origin, "checkout", "-b", "feature/remote"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(t, origin, "checkout", "master"); err != nil {
		// Fresh repos may default to master or main; resolve and retry.
		def := defaultBranch(t, origin)
		if err := runGit(t, origin, "checkout", def); err != nil {
			t.Fatal(err)
		}
	}

	clone := t.TempDir()
	if err := runGit(t, "", "clone", origin, clone); err != nil {
		t.Fatal(err)
	}
	service := NewGitService(clone)
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var local, remote []GitBranchSummary
	for _, branch := range snapshot.Branches {
		if branch.Remote {
			remote = append(remote, branch)
		} else {
			local = append(local, branch)
		}
	}
	if len(local) == 0 || len(remote) == 0 {
		t.Fatalf("want local+remote branches, got %#v", snapshot.Branches)
	}
	var remoteFeature string
	for _, branch := range remote {
		if strings.HasSuffix(branch.Name, "feature/remote") {
			remoteFeature = branch.Name
		}
	}
	if remoteFeature == "" {
		t.Fatalf("remote branches=%#v, want feature/remote", remote)
	}
	for _, branch := range remote {
		if branch.Name == "HEAD" || strings.HasSuffix(branch.Name, "/HEAD") {
			t.Fatalf("remote HEAD alias must be skipped: %#v", remote)
		}
	}
	// Checking out a remote-tracking name creates a local tracking branch.
	if err := service.Checkout(context.Background(), remoteFeature); err != nil {
		t.Fatal(err)
	}
	after, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Branch != "feature/remote" {
		t.Fatalf("branch=%q want feature/remote after remote checkout", after.Branch)
	}
}

func setGitIdentity(t *testing.T, repo *git.Repository) {
	t.Helper()
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name = "Crude Test"
	cfg.User.Email = "crude@example.test"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) error {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		return err
	}
	return nil
}

func defaultBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
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
