package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitServiceLinkedWorktreeKeepsDistinctIndex(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git(root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(root, "add", ".")
	git(root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base")
	linked := filepath.Join(t.TempDir(), "linked checkout")
	git(root, "worktree", "add", "--detach", linked)
	if err := os.WriteFile(filepath.Join(linked, "file.txt"), []byte("worker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewGitService(linked)
	snapshot, err := service.Snapshot(t.Context())
	if err != nil || snapshot.RepositoryRoot != linked || len(snapshot.RecentCommits) != 1 {
		t.Fatalf("linked snapshot=%#v err=%v", snapshot, err)
	}
	assertGitChange(t, snapshot.Changes, "file.txt", GitAreaWorking)
	if err := service.Stage(t.Context(), "file.txt"); err != nil {
		t.Fatal(err)
	}
	if got := git(root, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("worker stage altered logical index: %q", got)
	}
	if got := git(linked, "diff", "--cached", "--name-only"); got != "file.txt\n" {
		t.Fatalf("worker index=%q", got)
	}
}
