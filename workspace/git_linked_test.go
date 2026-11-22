package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// A refresh reuses immutable history, not operation state, and the
	// returned history belongs to the caller. The main checkout's marker
	// must not describe the linked worktree.
	snapshot.RecentCommits[0].Subject = "caller edit"
	mainMarker := filepath.Join(root, ".git", "MERGE_HEAD")
	if err := os.WriteFile(mainMarker, []byte(snapshot.Head+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if again, err := service.Snapshot(t.Context()); err != nil || again.Operation != "" || len(again.RecentCommits) != 1 || again.RecentCommits[0].Subject != "base" {
		t.Fatalf("linked worktree inherited foreign state: %+v, %v", again, err)
	}
	if err := os.Remove(mainMarker); err != nil {
		t.Fatal(err)
	}
	linkedDir := strings.TrimSpace(git(linked, "rev-parse", "--absolute-git-dir"))
	linkedMarker := filepath.Join(linkedDir, "CHERRY_PICK_HEAD")
	if err := os.WriteFile(linkedMarker, []byte(snapshot.Head+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if op, err := service.operation(t.Context()); err != nil || op != GitOperationCherryPick {
		t.Fatalf("linked operation = %q, %v", op, err)
	}
	if err := os.Remove(linkedMarker); err != nil {
		t.Fatal(err)
	}
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
