// Package filetree provides the file tree sidebar widget.
//
// This file implements git status resolution using the go-git library,
// providing per-file git status information for the file tree sidebar.
package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	repoPath  string
	gitDirs   []string
	statuses  map[string]GitStatus
	mu        sync.RWMutex
	refreshMu sync.Mutex
	repo      *git.Repository
}

// NewGitStatusResolver creates a new GitStatusResolver for the given repo path.
//
// repoPath should be the absolute path to the git repository root.
// If the path is not a valid git repository, the resolver operates in no-op
// mode: all Resolve calls return GitNone and Refresh is a no-op that returns
// an error. The initial status scan is performed on creation when the repo
// is valid.
func NewGitStatusResolver(repoPath string) *GitStatusResolver {
	r := &GitStatusResolver{
		repoPath: repoPath,
		statuses: make(map[string]GitStatus),
	}

	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		// Not a git repo or cannot open: no-op mode.
		return r
	}
	wt, err := repo.Worktree()
	if err != nil {
		return r
	}
	r.repo = repo
	root := filepath.Clean(wt.Filesystem.Root())
	if abs, absErr := filepath.Abs(root); absErr == nil {
		root = abs
	}
	r.repoPath = root
	r.gitDirs = repositoryMetadataDirs(r.repoPath)

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
	statuses, err := r.scan()
	if err != nil {
		return err
	}
	r.replaceStatuses(statuses)
	return nil
}

// scan reads one exact working-tree status snapshot without publishing it.
// Model commands carry this snapshot back to Bubble Tea so a later scan cannot
// overwrite a newer result before the corresponding tree projection applies.
func (r *GitStatusResolver) scan() (map[string]GitStatus, error) {
	if r == nil {
		return nil, fmt.Errorf("git status resolver is unavailable")
	}
	if r.repo == nil {
		return nil, fmt.Errorf("not a git repository: %s", r.repoPath)
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	wt, err := r.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get working tree status: %w", err)
	}

	result := make(map[string]GitStatus, len(status))
	for path, fileStatus := range status {
		result[filepath.ToSlash(path)] = mapGitStatus(fileStatus.Staging, fileStatus.Worktree)
	}
	return result, nil
}

func (r *GitStatusResolver) replaceStatuses(statuses map[string]GitStatus) {
	next := make(map[string]GitStatus, len(statuses))
	for path, status := range statuses {
		next[filepath.ToSlash(path)] = status
	}
	r.mu.Lock()
	r.statuses = next
	r.mu.Unlock()
}

// mapGitStatus maps go-git's Staging and Worktree StatusCode values to our
// GitStatus enum. The two-dimensional status (staging × worktree) is collapsed
// into a single value using the following priority order (highest first):
//
//  1. GitConflicted: merge conflict
//  2. GitModified: working tree or staging has modifications
//  3. GitStaged: added to staging (clean working tree or also added)
//  4. GitDeleted: file is deleted
//  5. GitRenamed: file is renamed
//  6. GitUntracked: new file not in index
//  7. GitNone: unmodified / no status
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

// gitStatusPriority is the semantic severity used when a directory collapses
// many descendant states into one marker. Enum declaration order is not a
// severity contract (for example, conflicts outrank renames).
func gitStatusPriority(status GitStatus) int {
	switch status {
	case GitConflicted:
		return 6
	case GitModified:
		return 5
	case GitStaged:
		return 4
	case GitDeleted:
		return 3
	case GitRenamed:
		return 2
	case GitUntracked:
		return 1
	default:
		return 0
	}
}

// GitStatusFromCode projects the neutral core status code into tree semantics.
func GitStatusFromCode(code string) GitStatus {
	switch code {
	case "!":
		return GitConflicted
	case "M":
		return GitModified
	case "A", "C":
		return GitStaged
	case "D":
		return GitDeleted
	case "R":
		return GitRenamed
	case "U", "?":
		return GitUntracked
	default:
		return GitNone
	}
}

// DominantGitStatus uses the same precedence as local repository projection.
func DominantGitStatus(a, b GitStatus) GitStatus {
	if gitStatusPriority(b) > gitStatusPriority(a) {
		return b
	}
	return a
}

// RepoPath returns the absolute path of the git repository root.
func (r *GitStatusResolver) RepoPath() string {
	return r.repoPath
}

// Available reports whether the workspace belongs to a readable worktree.
func (r *GitStatusResolver) Available() bool {
	return r != nil && r.repo != nil
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

// WatchForChanges watches repository metadata directories and invokes onChange
// after a short debounce. It deliberately watches directories rather than the
// index file: Git commonly writes index.lock and atomically renames it over the
// index, which silently invalidates a file watch. Current-worktree and common
// refs directories are watched recursively so commits and branch operations
// are detected for normal and linked worktrees.
//
// The returned stop function should be called to shut down the watcher
// goroutine. If the resolver is in no-op mode (nil repo), WatchForChanges
// returns nil without starting anything.
func (r *GitStatusResolver) WatchForChanges(onChange func()) (stop func()) {
	if r == nil || r.repo == nil || onChange == nil || len(r.gitDirs) == 0 {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	watched := make(map[string]struct{})
	addDir := func(path string) {
		path = filepath.Clean(path)
		if _, ok := watched[path]; ok {
			return
		}
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			return
		}
		if addErr := watcher.Add(path); addErr == nil {
			watched[path] = struct{}{}
		}
	}
	for _, dir := range r.gitDirs {
		addDir(dir)
		refs := filepath.Join(dir, "refs")
		_ = filepath.Walk(refs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr == nil && info.IsDir() {
				addDir(path)
			}
			return nil
		})
	}
	if len(watched) == 0 {
		_ = watcher.Close()
		return nil
	}

	stopCh := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		defer func() { _ = watcher.Close() }()

		var debounce *time.Timer
		var debounceC <-chan time.Time
		const debounceDur = 100 * time.Millisecond
		resetDebounce := func() {
			if debounce == nil {
				debounce = time.NewTimer(debounceDur)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(debounceDur)
			}
			debounceC = debounce.C
		}

		for {
			select {
			case <-stopCh:
				if debounce != nil {
					debounce.Stop()
				}
				return

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				// A newly-created refs subdirectory must join the recursive watch.
				if event.Op&fsnotify.Create != 0 {
					addDir(event.Name)
				}
				resetDebounce()

			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
				// Watcher errors are benign (e.g. removed watch target);
				// continue running.

			case <-debounceC:
				debounceC = nil
				onChange()
			}
		}
	}()

	return func() {
		stopOnce.Do(func() { close(stopCh) })
	}
}

func repositoryMetadataDirs(repoRoot string) []string {
	gitDir := resolveGitDir(repoRoot)
	if gitDir == "" {
		return nil
	}
	dirs := []string{gitDir}
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err == nil {
		common := strings.TrimSpace(string(data))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			common = filepath.Clean(common)
			if common != gitDir {
				dirs = append(dirs, common)
			}
		}
	}
	return dirs
}

func resolveGitDir(repoRoot string) string {
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Clean(dotGit)
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(strings.ToLower(line), prefix) {
		return ""
	}
	dir := strings.TrimSpace(line[len(prefix):])
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot, dir)
	}
	return filepath.Clean(dir)
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
	_, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	return err == nil
}
