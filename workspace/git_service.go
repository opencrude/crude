package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// GitChangeArea identifies the source-control section that owns a change.
type GitChangeArea uint8

const (
	GitAreaConflict GitChangeArea = iota
	GitAreaStaged
	GitAreaWorking
	GitAreaUntracked
)

// GitChange is presentation-neutral status for one repository path. A file
// with staged and unstaged edits appears twice, once in each area, matching
// the source-control model used by mature IDEs.
type GitChange struct {
	Path         string
	AbsolutePath string
	Area         GitChangeArea
	Code         string
	Description  string
}

// GitCommitSummary is compact history data for the source-control sidebar.
type GitCommitSummary struct {
	Hash      string
	ShortHash string
	Subject   string
	Author    string
	When      time.Time
}

// GitBranchSummary is one branch, local or remote-tracking.
type GitBranchSummary struct {
	Name    string
	Current bool
	Remote  bool
}

// GitSnapshot is an immutable source-control projection.
type GitSnapshot struct {
	RepositoryRoot string
	Branch         string
	Upstream       string
	Head           string
	Ahead          int
	Behind         int
	Changes        []GitChange
	RecentCommits  []GitCommitSummary
	Branches       []GitBranchSummary
	Remotes        []string
	// Operation is the merge, rebase, cherry-pick or revert waiting for
	// conflict resolution; OperationDetail is Git's description of it.
	Operation       string
	OperationDetail string
}

// GitService owns repository reads and mutations for one workspace. The TUI
// displays snapshots and emits intents; it never shells out to Git itself.
type GitService struct {
	root string
	mu   sync.Mutex
}

func NewGitService(root string) *GitService {
	return &GitService{root: root}
}

// Snapshot reads status, branch, and recent history without mutating the repo.
func (s *GitService) Snapshot(ctx context.Context) (GitSnapshot, error) {
	if s == nil {
		return GitSnapshot{}, errors.New("git service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	repo, root, err := s.open()
	if err != nil {
		return GitSnapshot{}, err
	}
	defer closeGitRepository(repo)
	wt, err := repo.Worktree()
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("open Git worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("read Git status: %w", err)
	}

	snapshot := GitSnapshot{RepositoryRoot: root}
	snapshot.readHead(repo)
	snapshot.readTracking(ctx)
	snapshot.Changes = projectGitChanges(root, status)
	snapshot.RecentCommits = recentGitCommits(repo, 8)
	snapshot.Branches = gitBranches(repo, snapshot.Branch)
	snapshot.Remotes = gitRemotes(repo)
	snapshot.readOperation(ctx)
	if snapshot.Operation != "" {
		// go-git does not report unmerged index stages; Git itself does.
		if conflicts, err := s.conflictedPaths(ctx); err == nil {
			snapshot.Changes = markGitConflicts(root, snapshot.Changes, conflicts)
		}
	}
	return snapshot, nil
}

func (s *GitSnapshot) readTracking(ctx context.Context) {
	if s.Upstream == "" || s.RepositoryRoot == "" {
		return
	}
	cmd := exec.CommandContext(ctx, "git", "-C", s.RepositoryRoot,
		"rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	out, err := cmd.Output()
	if err != nil {
		return
	}
	s.Ahead, s.Behind = parseAheadBehind(string(out))
}

// closeGitRepository releases go-git's retained object and packfile handles.
// Windows refuses to delete or replace files a leaked handle still holds.
func closeGitRepository(repo *git.Repository) {
	if repo == nil {
		return
	}
	if closer, ok := repo.Storer.(io.Closer); ok {
		_ = closer.Close()
	}
}

// open returns a repository the caller must release with closeGitRepository.
func (s *GitService) open() (*git.Repository, string, error) {
	root := s.root
	if root == "" {
		root = "."
	}
	repo, err := openGitRepository(root)
	if err != nil {
		return nil, "", fmt.Errorf("open Git repository: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, "", fmt.Errorf("open Git worktree: %w", err)
	}
	repoRoot := filepath.Clean(wt.Filesystem.Root())
	if abs, absErr := filepath.Abs(repoRoot); absErr == nil {
		repoRoot = abs
	}
	return repo, repoRoot, nil
}

func (s *GitService) run(ctx context.Context, args ...string) error {
	repo, root, err := s.open()
	if err != nil {
		return err
	}
	closeGitRepository(repo)
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return nil
}

// Stage adds the provided repository-relative paths to the index.
func (s *GitService) Stage(ctx context.Context, paths ...string) error {
	clean := cleanGitPaths(paths)
	if len(clean) == 0 {
		return nil
	}
	return s.mutate(ctx, append([]string{"add", "--"}, clean...)...)
}

// StageAll stages tracked, untracked, and deleted files.
func (s *GitService) StageAll(ctx context.Context) error {
	return s.mutate(ctx, "add", "-A")
}

// Unstage restores the provided paths in the index while preserving working
// tree edits. The reset fallback supports repositories without restore.
func (s *GitService) Unstage(ctx context.Context, paths ...string) error {
	clean := cleanGitPaths(paths)
	if len(clean) == 0 {
		return nil
	}
	args := append([]string{"restore", "--staged", "--"}, clean...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.run(ctx, args...); err == nil {
		return nil
	}
	return s.run(ctx, append([]string{"reset", "--"}, clean...)...)
}

// UnstageAll resets the complete index to HEAD while preserving files.
func (s *GitService) UnstageAll(ctx context.Context) error {
	return s.mutate(ctx, "reset")
}

// Discard restores tracked working-tree changes for paths. Untracked files are
// deliberately not deleted by this operation.
func (s *GitService) Discard(ctx context.Context, paths ...string) error {
	clean := cleanGitPaths(paths)
	if len(clean) == 0 {
		return nil
	}
	return s.mutate(ctx, append([]string{"restore", "--worktree", "--"}, clean...)...)
}

// Commit creates one commit from the current index.
func (s *GitService) Commit(ctx context.Context, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("commit message is empty")
	}
	return s.mutate(ctx, "commit", "-m", message)
}

// Checkout switches to an existing local branch, or creates and switches to
// a local tracking branch when given a remote-tracking name (origin/foo).
// This is the go-git equivalent of `git switch <branch>`: passing "origin/x"
// creates local "x" tracking "origin/x", while passing plain "x" switches to
// the existing local branch.
func (s *GitService) Checkout(ctx context.Context, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch is empty")
	}
	if strings.Contains(branch, "/") {
		if err := s.mutate(ctx, "switch", "-c", shortRemoteBranch(branch), "--track", branch); err == nil {
			return nil
		}
	}
	return s.mutate(ctx, "switch", branch)
}

// shortRemoteBranch strips the remote prefix ("origin/foo" -> "foo"). A
// nested branch name keeps everything after the first slash ("origin/a/b" ->
// "a/b"): only the first component is the remote name, matching `git switch
// <guess>` DWIM behavior.
func shortRemoteBranch(branch string) string {
	if index := strings.Index(branch, "/"); index >= 0 && index+1 < len(branch) {
		return branch[index+1:]
	}
	return branch
}

func (s *GitService) mutate(ctx context.Context, args ...string) error {
	if s == nil {
		return errors.New("git service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run(ctx, args...)
}

func cleanGitPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func (s *GitSnapshot) readHead(repo *git.Repository) {
	head, err := repo.Head()
	if err != nil {
		s.Branch = "No commits"
		return
	}
	s.Head = head.Hash().String()
	if head.Name().IsBranch() {
		s.Branch = head.Name().Short()
	} else {
		s.Branch = "detached@" + shortGitHash(s.Head)
	}

	cfg, err := repo.Config()
	if err != nil || !head.Name().IsBranch() {
		return
	}
	branch := cfg.Branches[s.Branch]
	if branch == nil || branch.Merge == "" {
		return
	}
	if branch.Remote != "" && branch.Remote != "." {
		s.Upstream = branch.Remote + "/" + branch.Merge.Short()
	} else {
		s.Upstream = branch.Merge.Short()
	}
}

func projectGitChanges(root string, status git.Status) []GitChange {
	paths := make([]string, 0, len(status))
	for path := range status {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	changes := make([]GitChange, 0, len(paths)*2)
	for _, path := range paths {
		file := status[path]
		abs := filepath.Join(root, filepath.FromSlash(path))
		if file.Staging == git.UpdatedButUnmerged || file.Worktree == git.UpdatedButUnmerged {
			changes = append(changes, newGitChange(path, abs, GitAreaConflict, git.UpdatedButUnmerged))
			continue
		}
		if file.Staging != git.Unmodified && file.Staging != git.Untracked {
			changes = append(changes, newGitChange(path, abs, GitAreaStaged, file.Staging))
		}
		switch file.Worktree {
		case git.Unmodified:
		case git.Untracked:
			changes = append(changes, newGitChange(path, abs, GitAreaUntracked, file.Worktree))
		default:
			changes = append(changes, newGitChange(path, abs, GitAreaWorking, file.Worktree))
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Area != changes[j].Area {
			return changes[i].Area < changes[j].Area
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func newGitChange(path, absolute string, area GitChangeArea, status git.StatusCode) GitChange {
	code, description := gitStatusText(status)
	return GitChange{
		Path:         filepath.ToSlash(path),
		AbsolutePath: absolute,
		Area:         area,
		Code:         code,
		Description:  description,
	}
}

func gitStatusText(status git.StatusCode) (string, string) {
	switch status {
	case git.Added:
		return "A", "Added"
	case git.Deleted:
		return "D", "Deleted"
	case git.Renamed:
		return "R", "Renamed"
	case git.Copied:
		return "C", "Copied"
	case git.Untracked:
		return "U", "Untracked"
	case git.UpdatedButUnmerged:
		return "!", "Conflict"
	default:
		return "M", "Modified"
	}
}

func recentGitCommits(repo *git.Repository, limit int) []GitCommitSummary {
	if limit <= 0 {
		return nil
	}
	head, err := repo.Head()
	if err != nil {
		return nil
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash(), Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil
	}
	defer iter.Close()
	out := make([]GitCommitSummary, 0, limit)
	_ = iter.ForEach(func(commit *object.Commit) error {
		if len(out) >= limit {
			return plumbing.ErrObjectNotFound
		}
		subject := strings.TrimSpace(strings.SplitN(commit.Message, "\n", 2)[0])
		hash := commit.Hash.String()
		out = append(out, GitCommitSummary{
			Hash: hash, ShortHash: shortGitHash(hash), Subject: subject,
			Author: commit.Author.Name, When: commit.Author.When,
		})
		return nil
	})
	return out
}

// gitBranches lists local branches first (current pinned top), then
// remote-tracking branches (origin/foo, excluding the symbolic HEAD aliases
// like origin/HEAD). Symbolic HEAD refs are skipped: they duplicate the
// branch they point at and `git switch` rejects them as checkout targets.
func gitBranches(repo *git.Repository, current string) []GitBranchSummary {
	iter, err := repo.References()
	if err != nil {
		return nil
	}
	defer iter.Close()
	var local, remote []GitBranchSummary
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name()
		switch {
		case name.IsBranch():
			short := name.Short()
			local = append(local, GitBranchSummary{Name: short, Current: short == current})
		case name.IsRemote():
			short := strings.TrimPrefix(string(name), "refs/remotes/")
			// Skip symbolic HEAD aliases ("origin/HEAD -> origin/main").
			if short == "HEAD" || strings.HasSuffix(short, "/HEAD") {
				return nil
			}
			if target, err := repo.Reference(name, false); err == nil && target.Type() == plumbing.SymbolicReference {
				return nil
			}
			remote = append(remote, GitBranchSummary{Name: short, Remote: true})
		}
		return nil
	})
	sort.Slice(local, func(i, j int) bool {
		if local[i].Current != local[j].Current {
			return local[i].Current
		}
		return local[i].Name < local[j].Name
	})
	sort.Slice(remote, func(i, j int) bool { return remote[i].Name < remote[j].Name })
	return append(local, remote...)
}

func shortGitHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

func parseAheadBehind(text string) (ahead, behind int) {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return 0, 0
	}
	ahead, _ = strconv.Atoi(fields[0])
	behind, _ = strconv.Atoi(fields[1])
	return ahead, behind
}
