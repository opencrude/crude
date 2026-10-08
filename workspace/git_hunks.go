package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"crude/fileio"
	"crude/localhistory"
	"crude/textdiff"

	"github.com/go-git/go-git/v5/plumbing/filemode"
)

// GitHunkRange is one displayed hunk of a comparison: zero-based, end-exclusive
// line ranges on the Before and After sides. Unchanged context inside the range
// is identical on both sides, so either side's lines can replace the other's.
type GitHunkRange struct {
	OldStart, OldEnd int
	NewStart, NewEnd int
}

// applyGitHunk returns Before with this hunk's After lines in place.
func applyGitHunk(comparison GitComparison, hunk GitHunkRange) (string, error) {
	before, after := textdiff.Lines(comparison.Before), textdiff.Lines(comparison.After)
	if err := hunk.validate(len(before), len(after)); err != nil {
		return "", err
	}
	return strings.Join(before[:hunk.OldStart], "") + strings.Join(after[hunk.NewStart:hunk.NewEnd], "") + strings.Join(before[hunk.OldEnd:], ""), nil
}

// revertGitHunk returns After with this hunk's Before lines restored.
func revertGitHunk(comparison GitComparison, hunk GitHunkRange) (string, error) {
	before, after := textdiff.Lines(comparison.Before), textdiff.Lines(comparison.After)
	if err := hunk.validate(len(before), len(after)); err != nil {
		return "", err
	}
	return strings.Join(after[:hunk.NewStart], "") + strings.Join(before[hunk.OldStart:hunk.OldEnd], "") + strings.Join(after[hunk.NewEnd:], ""), nil
}

func (hunk GitHunkRange) validate(beforeN, afterN int) error {
	if hunk.OldStart < 0 || hunk.OldStart > hunk.OldEnd || hunk.OldEnd > beforeN ||
		hunk.NewStart < 0 || hunk.NewStart > hunk.NewEnd || hunk.NewEnd > afterN {
		return errors.New("hunk no longer matches the compared versions; refresh the review")
	}
	return nil
}

func gitHunkCapable(comparison GitComparison) error {
	if comparison.Binary {
		return errors.New("binary files are staged as a whole")
	}
	return nil
}

// StageHunk records one hunk of a working-tree comparison in the index; the
// rest of the file keeps its index bytes. Fails closed on any newer change.
func (s *GitService) StageHunk(ctx context.Context, comparison GitComparison, hunk GitHunkRange) error {
	if err := gitHunkCapable(comparison); err != nil {
		return err
	}
	return s.mutateReviewed(ctx, comparison, true, func() (string, filemode.FileMode, bool, error) {
		text, err := applyGitHunk(comparison, hunk)
		if err != nil {
			return "", 0, false, err
		}
		if text == comparison.After {
			return comparison.After, filemode.FileMode(comparison.AfterMode), comparison.AfterPresent, nil
		}
		mode := filemode.FileMode(comparison.BeforeMode)
		if !comparison.BeforePresent {
			mode = filemode.FileMode(comparison.AfterMode)
		}
		return text, mode, true, nil
	})
}

// UnstageHunk restores one hunk of a staged comparison to its HEAD bytes in
// the index; the working tree is untouched.
func (s *GitService) UnstageHunk(ctx context.Context, comparison GitComparison, hunk GitHunkRange) error {
	if err := gitHunkCapable(comparison); err != nil {
		return err
	}
	return s.mutateReviewed(ctx, comparison, false, func() (string, filemode.FileMode, bool, error) {
		text, err := revertGitHunk(comparison, hunk)
		if err != nil {
			return "", 0, false, err
		}
		if text == comparison.Before {
			return comparison.Before, filemode.FileMode(comparison.BeforeMode), comparison.BeforePresent, nil
		}
		mode := filemode.FileMode(comparison.AfterMode)
		if !comparison.AfterPresent {
			mode = filemode.FileMode(comparison.BeforeMode)
		}
		return text, mode, true, nil
	})
}

// DiscardGitHunk reverts one hunk of a tracked working-tree file on disk after
// capturing a mandatory Local History snapshot. Any newer change aborts.
func (workspace *Workspace) DiscardGitHunk(ctx context.Context, comparison GitComparison, hunk GitHunkRange) (string, error) {
	if workspace.LocalHistory == nil || workspace.Git == nil || comparison.Fingerprint == "" {
		return "", errors.New("discard requires the repository and the Local History recovery store")
	}
	if comparison.Area != GitAreaWorking || comparison.Historical || !comparison.BeforePresent || !comparison.AfterPresent || comparison.OriginalPath != "" || comparison.Binary {
		return "", errors.New("hunk discard supports changed tracked text files only")
	}
	text, err := revertGitHunk(comparison, hunk)
	if err != nil {
		return "", err
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	service := workspace.Git
	service.mu.Lock()
	defer service.mu.Unlock()
	verify := func() error {
		state, err := service.readReviewState(ctx)
		if err != nil {
			return err
		}
		current, err := state.compare(ctx, GitChange{Path: comparison.Path, AbsolutePath: comparison.AbsolutePath, Area: GitAreaWorking})
		if err != nil {
			return err
		}
		if current.Fingerprint != comparison.Fingerprint || gitComparisonIdentity(current) != gitComparisonIdentity(comparison) {
			return ErrGitReviewStale
		}
		info, err := os.Lstat(comparison.AbsolutePath)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("discard target is no longer a regular file")
		}
		return nil
	}
	if err := verify(); err != nil {
		return "", err
	}
	safety, err := workspace.LocalHistory.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before Git hunk discard: " + comparison.Path, IncludePaths: []string{comparison.Path}, DisableGitIgnore: true, MaxFileSize: gitComparisonLimit})
	if err != nil {
		return "", err
	}
	if entry, exists := safety.Entries[filepath.ToSlash(comparison.Path)]; !exists || entry.Type != localhistory.EntryFile {
		return "", errors.New("mandatory recovery did not capture the file; discard aborted")
	}
	info, err := os.Stat(comparison.AbsolutePath)
	if err != nil {
		return safety.ID, err
	}
	return safety.ID, fileio.WriteAtomicChecked(comparison.AbsolutePath, []byte(text), info.Mode().Perm(), verify)
}
