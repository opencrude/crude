// Package filetree provides the file tree sidebar widget.
//
// This file implements git status resolution using the go-git library,
// providing per-file git status information for the file tree sidebar.
package filetree

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-git/go-git/v5"
)

// ---------------------------------------------------------------------------
// GitStatus enum
// ---------------------------------------------------------------------------

// GitStatus represents the git status of a file in the working tree.
type GitStatus int

const (
	// GitNone indicates the file is not tracked or has no interesting status.
	GitNone GitStatus = iota
	// GitUntracked indicates a new file not yet in the index (??).
	GitUntracked
	// GitModified indicates the file has been modified in the working tree (M).
	GitModified
	// GitStaged indicates the file has been added to the index / staging area (A).
	GitStaged
	// GitDeleted indicates the file has been deleted (D).
	GitDeleted
	// GitConflicted indicates the file has a merge conflict (UU).
	GitConflicted
	// GitRenamed indicates the file has been renamed (R).
	GitRenamed
)

// ---------------------------------------------------------------------------
// GitStatusResolver
// ---------------------------------------------------------------------------

// GitStatusResolver resolves git status for files in a repository.
// It maintains a thread-safe cache of path → status mappings and refreshes
// them from the git working tree. When operating outside a git repository
// (nil repo), all queries return GitNone.
type GitStatusResolver struct {
	repoPath string
	statuses map[string]GitStatus
	mu       sync.RWMutex
	repo     *git.Repository
}

// NewGitStatusResolver creates a new GitStatusResolver for the given repo path.
//
// repoPath should be the absolute path to the git repository root.
// If the path is not a valid git repository, the resolver operates in no-op
// mode — all Resolve calls return GitNone and Refresh is a no-op that returns
// an error. The initial status scan is performed on creation when the repo
// is valid.
func NewGitStatusResolver(repoPath string) *GitStatusResolver {
	r := &GitStatusResolver{
		repoPath: repoPath,
		statuses: make(map[string]GitStatus),
	}

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		// Not a git repo or cannot open — no-op mode.
		return r
	}
	r.repo = repo

	// Perform initial status scan. Errors are non-fatal; the resolver will
	// simply operate with an empty cache until the next successful Refresh.
	_ = r.Refresh()

	return r
}

// Resolve returns the cached git status for the given absolute file path.
// Returns GitNone if the path is not in the status cache, the resolver is
// in no-op mode, or the path is outside the repository root.
func (r *GitStatusResolver) Resolve(path string) GitStatus {
	if r.repo == nil {
		return GitNone
	}

	rel, err := filepath.Rel(r.repoPath, path)
	if err != nil {
		return GitNone
	}
	// Normalise path separators to forward slashes, matching go-git's
	// internal representation on all platforms.
	rel = filepath.ToSlash(rel)

	r.mu.RLock()
	defer r.mu.RUnlock()

	if status, ok := r.statuses[rel]; ok {
		return status
	}
	return GitNone
}

// Refresh rescans the git working tree and updates the internal status cache.
// Returns an error if the repository is not available (no-op mode) or if the
// underlying git operations fail. The cache is atomically replaced with the
// new results on success.
func (r *GitStatusResolver) Refresh() error {
	if r.repo == nil {
		return fmt.Errorf("not a git repository: %s", r.repoPath)
	}

	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("failed to get working tree status: %w", err)
	}

	// Build a new map and atomically swap it in so readers never see a
	// partially-populated map.
	newStatuses := make(map[string]GitStatus, len(status))
	for path, fileStatus := range status {
		newStatuses[path] = mapGitStatus(fileStatus.Staging, fileStatus.Worktree)
	}

	r.mu.Lock()
	r.statuses = newStatuses
	r.mu.Unlock()

	return nil
}

// mapGitStatus maps go-git's Staging and Worktree StatusCode values to our
// GitStatus enum. The two-dimensional status (staging × worktree) is collapsed
// into a single value using the following priority order (highest first):
//
//  1. GitConflicted  — merge conflict
//  2. GitModified    — working tree or staging has modifications
//  3. GitStaged      — added to staging (clean working tree or also added)
//  4. GitDeleted     — file is deleted
//  5. GitRenamed     — file is renamed
//  6. GitUntracked   — new file not in index
//  7. GitNone        — unmodified / no status
func mapGitStatus(staging, worktree git.StatusCode) GitStatus {
	switch {
	case staging == git.UpdatedButUnmerged || worktree == git.UpdatedButUnmerged:
		return GitConflicted
	case staging == git.Modified || worktree == git.Modified:
		return GitModified
	case staging == git.Added || worktree == git.Added:
		return GitStaged
	case staging == git.Deleted || worktree == git.Deleted:
		return GitDeleted
	case staging == git.Renamed || worktree == git.Renamed:
		return GitRenamed
	case staging == git.Untracked || worktree == git.Untracked:
		return GitUntracked
	default:
		return GitNone
	}
}

// RepoPath returns the absolute path of the git repository root.
func (r *GitStatusResolver) RepoPath() string {
	return r.repoPath
}

// AllStatuses returns a copy of the full status map (relative path → status).
// Returns nil when the resolver is in no-op mode.
func (r *GitStatusResolver) AllStatuses() map[string]GitStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]GitStatus, len(r.statuses))
	for k, v := range r.statuses {
		result[k] = v
	}
	return result
}

// WatchForChanges starts a background goroutine that watches .git/index (or
// the .git directory as a fallback) for file system events using fsnotify.
// When a write or create event is detected, it triggers a Refresh after a
// short debounce (100 ms) to batch rapid changes.
//
// The returned stop function should be called to shut down the watcher
// goroutine. If the resolver is in no-op mode (nil repo), WatchForChanges
// returns nil without starting anything.
func (r *GitStatusResolver) WatchForChanges() (stop func()) {
	if r.repo == nil {
		return nil
	}

	gitDir := filepath.Join(r.repoPath, ".git")
	indexPath := filepath.Join(gitDir, "index")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}

	// Try to watch the index file directly; if that fails (e.g. index does not
	// exist yet in a bare repo), fall back to watching the entire .git directory.
	if err := watcher.Add(indexPath); err != nil {
		if err := watcher.Add(gitDir); err != nil {
			_ = watcher.Close()
			return nil
		}
	}

	stopCh := make(chan struct{})

	go func() {
		defer func() { _ = watcher.Close() }()

		var debounce *time.Timer
		const debounceDur = 100 * time.Millisecond

		for {
			select {
			case <-stopCh:
				if debounce != nil {
					debounce.Stop()
				}
				return

			case event := <-watcher.Events:
				// Only react to write or create events on the index file.
				if filepath.Base(event.Name) == "index" &&
					event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					if debounce != nil {
						debounce.Stop()
					}
					debounce = time.AfterFunc(debounceDur, func() {
						_ = r.Refresh()
					})
				}

			case <-watcher.Errors:
				// Watcher errors are benign (e.g. removed watch target);
				// continue running.
			}
		}
	}()

	return func() {
		close(stopCh)
	}
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// StatusString returns a short single-character indicator for the given
// GitStatus, suitable for display next to a file name in the tree.
//
// Mapping:
//
//	GitNone       → ""
//	GitModified   → "M"
//	GitStaged     → "A"
//	GitDeleted    → "D"
//	GitUntracked  → "U"
//	GitConflicted → "!"
//	GitRenamed    → "R"
func StatusString(s GitStatus) string {
	switch s {
	case GitNone:
		return ""
	case GitModified:
		return "M"
	case GitStaged:
		return "A"
	case GitDeleted:
		return "D"
	case GitUntracked:
		return "U"
	case GitConflicted:
		return "!"
	case GitRenamed:
		return "R"
	default:
		return ""
	}
}

// IsGitRepo checks whether the given path is (or is inside) a git repository.
// It attempts to open the repository with go-git's PlainOpen, which searches
// upward from the given path for a .git directory. Returns true if a valid
// git repository is found.
func IsGitRepo(path string) bool {
	_, err := git.PlainOpen(path)
	return err == nil
}
