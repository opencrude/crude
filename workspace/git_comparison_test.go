package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func reviewRepository(t *testing.T, initial bool) (*git.Repository, *git.Worktree, *GitService, string) {
	t.Helper()
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name, cfg.User.Email = "Review Test", "review@example.test"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if initial {
		reviewWrite(t, root, "file.txt", "HEAD\n")
		if _, err := wt.Add("file.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Review Test", Email: "review@example.test", When: time.Unix(100, 0)}}); err != nil {
			t.Fatal(err)
		}
	}
	return repo, wt, NewGitService(root), root
}

func reviewWrite(t *testing.T, root, name, text string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func reviewCompare(t *testing.T, service *GitService, name string, area GitChangeArea) GitComparison {
	t.Helper()
	comparison, err := service.Compare(context.Background(), GitChange{Path: name, Area: area})
	if err != nil {
		t.Fatal(err)
	}
	return comparison
}

func TestGitComparisonSeparatesHeadIndexAndDisk(t *testing.T) {
	_, wt, service, root := reviewRepository(t, true)
	reviewWrite(t, root, "file.txt", "Index\n")
	if _, err := wt.Add("file.txt"); err != nil {
		t.Fatal(err)
	}
	reviewWrite(t, root, "file.txt", "Disk\n")
	staged := reviewCompare(t, service, "file.txt", GitAreaStaged)
	working := reviewCompare(t, service, "file.txt", GitAreaWorking)
	if staged.Before != "HEAD\n" || staged.After != "Index\n" || staged.BaseLabel != "HEAD" || staged.TargetLabel != "Index (staged)" {
		t.Fatalf("staged comparison=%+v", staged)
	}
	if working.Before != "Index\n" || working.After != "Disk\n" || working.TargetLabel != "Working tree (disk)" {
		t.Fatalf("working comparison=%+v", working)
	}
	if staged.Fingerprint == "" || staged.Fingerprint == working.Fingerprint {
		t.Fatal("comparison identities must distinguish the compared areas")
	}
	if err := service.StageReviewed(context.Background(), working); err != nil {
		t.Fatal(err)
	}
	restaged := reviewCompare(t, service, "file.txt", GitAreaStaged)
	if restaged.After != "Disk\n" {
		t.Fatalf("staged unseen bytes: %q", restaged.After)
	}
	if err := service.UnstageReviewed(context.Background(), restaged); err != nil {
		t.Fatal(err)
	}
	unstaged := reviewCompare(t, service, "file.txt", GitAreaWorking)
	if unstaged.Before != "HEAD\n" || unstaged.After != "Disk\n" {
		t.Fatalf("unstage changed disk content or failed to reset index: %+v", unstaged)
	}
}

func TestGitComparisonStagingRejectsStaleDiskIndexAndHead(t *testing.T) {
	for _, mutation := range []string{"disk", "index", "head", "preview"} {
		t.Run(mutation, func(t *testing.T) {
			repo, wt, service, root := reviewRepository(t, true)
			reviewWrite(t, root, "file.txt", "Reviewed\n")
			comparison := reviewCompare(t, service, "file.txt", GitAreaWorking)
			switch mutation {
			case "disk":
				reviewWrite(t, root, "file.txt", "Later\n")
			case "index":
				reviewWrite(t, root, "other.txt", "unseen staged file")
				if _, err := wt.Add("other.txt"); err != nil {
					t.Fatal(err)
				}
			case "head":
				head, err := repo.Head()
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("same-tree"), head.Hash())); err != nil {
					t.Fatal(err)
				}
				if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("same-tree"))); err != nil {
					t.Fatal(err)
				}
			case "preview":
				comparison.After = "not the reviewed content"
			}
			before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			if err := service.StageReviewed(context.Background(), comparison); !errors.Is(err, ErrGitReviewStale) {
				t.Fatalf("stale stage error=%v", err)
			}
			after, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("stale review modified the index")
			}
			if _, err := os.Stat(filepath.Join(root, ".git", "index.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned lock was not released: %v", err)
			}
		})
	}
}

func TestGitComparisonUntrackedDeletedBinaryAndUnborn(t *testing.T) {
	_, _, service, root := reviewRepository(t, false)
	reviewWrite(t, root, "empty.txt", "")
	empty := reviewCompare(t, service, "empty.txt", GitAreaUntracked)
	if !empty.Untracked || empty.BeforePresent || !empty.AfterPresent {
		t.Fatalf("empty untracked comparison=%+v", empty)
	}
	if err := service.StageReviewed(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	staged := reviewCompare(t, service, "empty.txt", GitAreaStaged)
	if err := service.UnstageReviewed(context.Background(), staged); err != nil {
		t.Fatal(err)
	}
	reviewWrite(t, root, "binary.dat", "a\x00b")
	binary := reviewCompare(t, service, "binary.dat", GitAreaUntracked)
	if !binary.Binary || binary.After != "a\x00b" {
		t.Fatalf("binary comparison=%+v", binary)
	}
	if err := service.StageReviewed(context.Background(), binary); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewCommit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Comparisons) != 1 || review.Head != "" {
		t.Fatalf("unborn commit review=%+v", review)
	}
	if err := service.CommitStaged(context.Background(), []string{"binary.dat"}, "initial\n\nExact reviewed binary"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "binary.dat")); err != nil {
		t.Fatal(err)
	}
	deleted := reviewCompare(t, service, "binary.dat", GitAreaWorking)
	if !deleted.Deleted || deleted.AfterPresent || !deleted.BeforePresent {
		t.Fatalf("deleted comparison=%+v", deleted)
	}
	if err := service.StageReviewed(context.Background(), deleted); err != nil {
		t.Fatal(err)
	}
	stagedDeletion := reviewCompare(t, service, "binary.dat", GitAreaStaged)
	if err := service.UnstageReviewed(context.Background(), stagedDeletion); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "binary.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unstaging a deletion must not restore the working file")
	}
}

func TestGitCommitStagedUsesExactStagedSetAndInspectsEvidence(t *testing.T) {
	repo, wt, service, root := reviewRepository(t, true)
	reviewWrite(t, root, "nested/feature.txt", "reviewed staged\n")
	if _, err := wt.Add("nested/feature.txt"); err != nil {
		t.Fatal(err)
	}
	reviewWrite(t, root, "nested/feature.txt", "newer working text\n")
	if err := service.CommitStaged(context.Background(), []string{"nested/feature.txt", "other.txt"}, "wrong set"); !errors.Is(err, ErrGitReviewStale) {
		t.Fatalf("commit accepted a staged set the user was not shown: %v", err)
	}
	message := "Feature\n\nMultiline message stays exact.\n"
	if err := service.CommitStaged(context.Background(), []string{"nested/feature.txt"}, message); err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := service.InspectCommit(context.Background(), head.Hash().String())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Message != message || len(evidence.Comparisons) != 1 || evidence.Comparisons[0].After != "reviewed staged\n" || !evidence.Comparisons[0].Historical {
		t.Fatalf("commit evidence=%+v", evidence)
	}
	remaining := reviewCompare(t, service, "nested/feature.txt", GitAreaWorking)
	if remaining.After != "newer working text\n" || remaining.Before != "reviewed staged\n" {
		t.Fatalf("commit consumed working bytes: %+v", remaining)
	}
	if err := service.CommitStaged(context.Background(), []string{"nested/feature.txt"}, "repeat"); !errors.Is(err, ErrGitReviewStale) {
		t.Fatalf("commit of an already committed set accepted: %v", err)
	}
}

// A staged rename is one row in the panel; committing what the panel showed
// must succeed rather than fail as stale.
func TestGitCommitStagedAcceptsTheRenameThePanelShows(t *testing.T) {
	repo, _, service, root := reviewRepository(t, true)
	if err := os.Mkdir(filepath.Join(root, "moved"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runGit(t, root, "mv", "file.txt", "moved/file.txt"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.StagedPaths(); len(got) != 1 || got[0] != "moved/file.txt" {
		t.Fatalf("panel shows staged=%v, want the one rename row", got)
	}
	if err := service.CommitStaged(context.Background(), snapshot.StagedPaths(), "move"); err != nil {
		t.Fatalf("commit of the shown rename failed: %v", err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.File("moved/file.txt"); err != nil {
		t.Fatalf("committed tree lacks the rename destination: %v", err)
	}
	if _, err := tree.File("file.txt"); err == nil {
		t.Fatal("committed tree still holds the rename source")
	}
}

func TestGitComparisonPathCancellationAndExistingLock(t *testing.T) {
	_, _, service, root := reviewRepository(t, true)
	for _, name := range []string{"../outside", ".git/config", "nested/../file.txt", "/file.txt"} {
		if _, err := service.Compare(context.Background(), GitChange{Path: name, Area: GitAreaWorking}); err == nil {
			t.Fatalf("unsafe review path accepted: %q", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Compare(ctx, GitChange{Path: "file.txt", Area: GitAreaWorking}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled comparison=%v", err)
	}
	reviewWrite(t, root, "file.txt", "changed\n")
	comparison := reviewCompare(t, service, "file.txt", GitAreaWorking)
	lockPath := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("external lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.StageReviewed(context.Background(), comparison); err == nil {
		t.Fatal("review stage ignored an existing Git index lock")
	}
	lock, err := os.ReadFile(lockPath)
	if err != nil || string(lock) != "external lock" {
		t.Fatal("review stage changed another operation's lock")
	}
}

func TestGitCompareChangesReadsOneSnapshotAndIsolatesFailures(t *testing.T) {
	_, wt, service, root := reviewRepository(t, true)
	reviewWrite(t, root, "file.txt", "edited\n")
	reviewWrite(t, root, "added.txt", "brand new\n")
	if _, err := wt.Add("added.txt"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changes := append(snapshot.Changes, GitChange{Path: "../escape", Area: GitAreaWorking})
	results, err := service.CompareChanges(context.Background(), changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(changes) {
		t.Fatalf("results=%d changes=%d", len(results), len(changes))
	}
	byPath := map[string]GitChangeComparison{}
	for _, result := range results {
		byPath[result.Change.Path] = result
	}
	if got := byPath["file.txt"]; got.Err != nil || got.Comparison.After != "edited\n" {
		t.Fatalf("working comparison=%+v", got)
	}
	if got := byPath["added.txt"]; got.Err != nil || got.Comparison.Area != GitAreaStaged || got.Comparison.After != "brand new\n" {
		t.Fatalf("staged comparison=%+v", got)
	}
	if byPath["../escape"].Err == nil {
		t.Fatal("unsafe path compared instead of failing on its own")
	}
	if got := snapshot.StagedPaths(); len(got) != 1 || got[0] != "added.txt" {
		t.Fatalf("staged paths=%v", got)
	}
}
