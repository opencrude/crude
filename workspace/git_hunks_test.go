package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/localhistory"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func hunkText(changed map[int]string) string {
	var text strings.Builder
	for line := 1; line <= 30; line++ {
		if value, ok := changed[line]; ok {
			text.WriteString(value + "\n")
			continue
		}
		fmt.Fprintf(&text, "line %d\n", line)
	}
	return text.String()
}

func TestGitHunkStageAndUnstageTouchOnlyThatHunk(t *testing.T) {
	_, wt, service, root := reviewRepository(t, true)
	reviewWrite(t, root, "file.txt", hunkText(nil))
	if _, err := wt.Add("file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("lines", &git.CommitOptions{Author: &object.Signature{Name: "T", Email: "t@example.test", When: time.Unix(200, 0)}}); err != nil {
		t.Fatal(err)
	}
	reviewWrite(t, root, "file.txt", hunkText(map[int]string{2: "two", 20: "twenty"}))
	working := reviewCompare(t, service, "file.txt", GitAreaWorking)
	first := GitHunkRange{OldStart: 0, OldEnd: 5, NewStart: 0, NewEnd: 5}
	if err := service.StageHunk(t.Context(), working, first); err != nil {
		t.Fatal(err)
	}
	staged := reviewCompare(t, service, "file.txt", GitAreaStaged)
	if staged.After != hunkText(map[int]string{2: "two"}) {
		t.Fatalf("index after staging the first hunk:\n%s", staged.After)
	}
	if remaining := reviewCompare(t, service, "file.txt", GitAreaWorking); remaining.Before != staged.After || !strings.Contains(remaining.After, "twenty") {
		t.Fatal("working comparison did not keep the second hunk unstaged")
	}
	if err := service.StageHunk(t.Context(), working, first); !errors.Is(err, ErrGitReviewStale) {
		t.Fatalf("stale hunk accepted: %v", err)
	}
	if err := service.UnstageHunk(t.Context(), staged, GitHunkRange{OldStart: 0, OldEnd: 5, NewStart: 0, NewEnd: 5}); err != nil {
		t.Fatal(err)
	}
	if snapshot, _ := service.Snapshot(t.Context()); len(snapshot.StagedPaths()) != 0 {
		t.Fatalf("unstaging the only hunk left staged paths: %v", snapshot.StagedPaths())
	}
	if contents, _ := os.ReadFile(filepath.Join(root, "file.txt")); !strings.Contains(string(contents), "two") {
		t.Fatal("unstage touched the working tree")
	}
	if err := service.StageHunk(t.Context(), reviewCompare(t, service, "file.txt", GitAreaWorking), GitHunkRange{OldStart: 0, OldEnd: 99}); err == nil {
		t.Fatal("out-of-range hunk accepted")
	}
}

func TestGitHunkDiscardRevertsOnlyThatHunkWithRecovery(t *testing.T) {
	_, wt, service, root := reviewRepository(t, true)
	reviewWrite(t, root, "file.txt", hunkText(nil))
	if _, err := wt.Add("file.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("lines", &git.CommitOptions{Author: &object.Signature{Name: "T", Email: "t@example.test", When: time.Unix(200, 0)}}); err != nil {
		t.Fatal(err)
	}
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner := &Workspace{RootPath: root, Git: service, LocalHistory: store}
	reviewWrite(t, root, "file.txt", hunkText(map[int]string{2: "two", 20: "twenty"}))
	working := reviewCompare(t, service, "file.txt", GitAreaWorking)
	safety, err := owner.DiscardGitHunk(t.Context(), working, GitHunkRange{OldStart: 16, OldEnd: 23, NewStart: 16, NewEnd: 23})
	if err != nil || safety == "" {
		t.Fatalf("discard: %q %v", safety, err)
	}
	contents, _ := os.ReadFile(filepath.Join(root, "file.txt"))
	if string(contents) != hunkText(map[int]string{2: "two"}) {
		t.Fatalf("disk after hunk discard:\n%s", contents)
	}
	if _, err := owner.DiscardGitHunk(t.Context(), working, GitHunkRange{OldStart: 0, OldEnd: 5, NewStart: 0, NewEnd: 5}); !errors.Is(err, ErrGitReviewStale) {
		t.Fatalf("stale hunk discard: %v", err)
	}
}
