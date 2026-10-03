package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/localhistory"
)

func TestReviewedGitDiscardRequiresCurrentBytesAndRecoverableSafety(test *testing.T) {
	_, _, service, root := reviewRepository(test, true)
	store, err := localhistory.Open(filepath.Join(test.TempDir(), "history"), root)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner := &Workspace{RootPath: root, Git: service, LocalHistory: store}
	change := GitChange{Path: "file.txt", AbsolutePath: filepath.Join(root, "file.txt"), Area: GitAreaWorking}
	reviewWrite(test, root, "file.txt", "reviewed working bytes\n")
	preview, err := owner.PreviewGitDiscard(test.Context(), change)
	if err != nil {
		test.Fatal(err)
	}
	reviewWrite(test, root, "file.txt", "later external bytes\n")
	if _, err := owner.DiscardGitReviewed(test.Context(), preview); !errors.Is(err, ErrGitReviewStale) {
		test.Fatalf("stale discard: %v", err)
	}
	preview, err = owner.PreviewGitDiscard(test.Context(), change)
	if err != nil {
		test.Fatal(err)
	}
	safety, err := owner.DiscardGitReviewed(test.Context(), preview)
	if err != nil || safety == "" {
		test.Fatalf("discard: %s %v", safety, err)
	}
	contents, err := os.ReadFile(change.AbsolutePath)
	if err != nil || string(contents) != "HEAD\n" {
		test.Fatalf("discarded bytes: %q %v", contents, err)
	}
	plan, err := store.PlanRestorePaths(context.Background(), safety, localhistory.SnapshotOptions{}, []string{change.Path})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.ApplyRestore(test.Context(), plan.ID, []string{change.Path}); err != nil {
		test.Fatal(err)
	}
	contents, err = os.ReadFile(change.AbsolutePath)
	if err != nil || string(contents) != "later external bytes\n" {
		test.Fatalf("recovery: %q %v", contents, err)
	}
	change.Area = GitAreaUntracked
	if _, err := owner.PreviewGitDiscard(test.Context(), change); err == nil {
		test.Fatal("untracked discard offered")
	}
}
