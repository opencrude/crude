package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"crude/fileio"
	"crude/localhistory"

	"github.com/go-git/go-git/v5/plumbing/filemode"
)

type GitDiscardPreview struct {
	Comparison GitComparison
}

func (workspace *Workspace) PreviewGitDiscard(ctx context.Context, change GitChange) (GitDiscardPreview, error) {
	return PreviewGitDiscardAt(ctx, workspace.Git, workspace.LocalHistory, change)
}

func PreviewGitDiscardAt(ctx context.Context, git *GitService, store *localhistory.Store, change GitChange) (GitDiscardPreview, error) {
	if store == nil || git == nil {
		return GitDiscardPreview{}, errors.New("discard requires available repository and Local History recovery")
	}
	if change.Area != GitAreaWorking {
		return GitDiscardPreview{}, errors.New("discard only applies to reviewed tracked working-tree changes; untracked files are never deleted")
	}
	comparison, err := git.Compare(ctx, change)
	if err != nil {
		return GitDiscardPreview{}, err
	}
	if !comparison.BeforePresent || !comparison.AfterPresent || comparison.OriginalPath != "" || filemode.FileMode(comparison.AfterMode) == filemode.Symlink || filemode.FileMode(comparison.BeforeMode) == filemode.Symlink {
		return GitDiscardPreview{}, errors.New("discard supports tracked regular files; deleted, renamed and symbolic files require an explicit Local History restore")
	}
	return GitDiscardPreview{Comparison: comparison}, nil
}

func (workspace *Workspace) DiscardGitReviewed(ctx context.Context, preview GitDiscardPreview) (string, error) {
	return DiscardGitReviewedAt(ctx, workspace.Git, workspace.LocalHistory, workspace.LiveDocuments, preview)
}

func DiscardGitReviewedAt(ctx context.Context, git *GitService, store *localhistory.Store, documents *DocumentService, preview GitDiscardPreview) (string, error) {
	if store == nil || git == nil || preview.Comparison.Fingerprint == "" {
		return "", errors.New("discard preview or mandatory recovery store is unavailable")
	}
	var safetyID string
	run := func() error {
		var err error
		safetyID, err = discardGitReviewedDisk(ctx, git, store, preview)
		return err
	}
	if documents != nil {
		err := documents.WithHistoryMutation(ctx, []string{preview.Comparison.AbsolutePath}, run)
		return safetyID, err
	}
	err := run()
	return safetyID, err
}

func discardGitReviewedDisk(ctx context.Context, service *GitService, store *localhistory.Store, preview GitDiscardPreview) (string, error) {
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	service.mu.Lock()
	defer service.mu.Unlock()
	comparison := preview.Comparison
	verify := func() error {
		if comparison.RepositoryRoot != service.root {
			return errors.New("discard preview belongs to another repository")
		}
		path, err := integrationPath(service.root, filepath.ToSlash(comparison.Path))
		if err != nil || path != comparison.AbsolutePath {
			return errors.New("discard target path changed or contains a symlink")
		}
		state, err := service.readReviewState(ctx)
		if err != nil {
			return err
		}
		current, err := state.compare(ctx, GitChange{Path: comparison.Path, AbsolutePath: comparison.AbsolutePath, Area: GitAreaWorking})
		if err != nil {
			return err
		}
		if comparison.Area != GitAreaWorking || comparison.Historical || current.Fingerprint != comparison.Fingerprint || gitComparisonIdentity(current) != gitComparisonIdentity(comparison) || !current.BeforePresent || !current.AfterPresent || current.OriginalPath != "" {
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
	safety, err := store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before Git discard: " + comparison.Path, IncludePaths: []string{comparison.Path}, DisableGitIgnore: true, MaxFileSize: gitComparisonLimit})
	if err != nil {
		return "", err
	}
	if entry, exists := safety.Entries[filepath.ToSlash(comparison.Path)]; !exists || entry.Type != localhistory.EntryFile {
		return "", errors.New("mandatory recovery did not capture the reviewed file; discard aborted")
	}
	mode := os.FileMode(0644)
	if filemode.FileMode(comparison.BeforeMode) == filemode.Executable {
		mode = 0755
	}
	if err := fileio.WriteAtomicChecked(comparison.AbsolutePath, []byte(comparison.Before), mode, verify); err != nil {
		return safety.ID, err
	}
	return safety.ID, nil
}
