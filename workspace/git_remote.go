package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"crude/fileio"
	"crude/localhistory"

	"github.com/go-git/go-git/v5"
)

// Repository operations that stop for conflicts. Each has its own abort and
// continue commands; the snapshot names the one in progress.
const (
	GitOperationMerge      = "merge"
	GitOperationRebase     = "rebase"
	GitOperationCherryPick = "cherry-pick"
	GitOperationRevert     = "revert"
)

// GitSyncResult reports a pull or merge. Conflicts are a result, not an
// error: the repository is mid-merge and waits for resolution.
type GitSyncResult struct {
	Summary   string
	Conflicts []string
}

// ErrGitAuthentication marks a remote that asked for credentials. Crude never
// answers credential prompts; it explains how to configure them instead.
var ErrGitAuthentication = errors.New("the remote needs credentials Crude cannot supply interactively")

func (s *GitSnapshot) readOperation(ctx context.Context) {
	if s.RepositoryRoot == "" {
		return
	}
	out, err := exec.CommandContext(ctx, "git", "-C", s.RepositoryRoot, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return
	}
	dir := strings.TrimSpace(string(out))
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge") || exists("rebase-apply"):
		s.Operation = GitOperationRebase
	case exists("MERGE_HEAD"):
		s.Operation = GitOperationMerge
		if data, err := os.ReadFile(filepath.Join(dir, "MERGE_MSG")); err == nil {
			s.OperationDetail = strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
		}
	case exists("CHERRY_PICK_HEAD"):
		s.Operation = GitOperationCherryPick
	case exists("REVERT_HEAD"):
		s.Operation = GitOperationRevert
	}
}

func gitRemotes(repo *git.Repository) []string {
	cfg, err := repo.Config()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ConflictedPaths lists repository paths still marked as conflicted.
func (s GitSnapshot) ConflictedPaths() []string {
	var out []string
	for _, change := range s.Changes {
		if change.Area == GitAreaConflict {
			out = append(out, change.Path)
		}
	}
	return out
}

// runRemote runs a command that may talk to a remote. Credential prompts are
// disabled so a missing credential fails promptly instead of waiting on a
// terminal that does not exist; an askpass helper or credential manager the
// user configured still works.
func (s *GitService) runRemote(ctx context.Context, args ...string) (string, error) {
	repo, root, err := s.open()
	if err != nil {
		return "", err
	}
	closeGitRepository(repo)
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err == nil {
		return text, nil
	}
	if ctx.Err() != nil {
		return text, fmt.Errorf("git %s stopped: %w", args[0], ctx.Err())
	}
	if gitNeedsCredentials(text) {
		return text, fmt.Errorf("%w: %s. Set up an SSH key in your agent or a credential helper (for GitHub: gh auth setup-git), then retry", ErrGitAuthentication, lastLine(text))
	}
	if text == "" {
		text = err.Error()
	}
	return text, fmt.Errorf("git %s: %s", args[0], text)
}

func gitNeedsCredentials(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{"terminal prompts disabled", "could not read username", "could not read password", "authentication failed", "permission denied (publickey", "host key verification failed"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Fetch updates every remote-tracking branch and prunes deleted ones.
func (s *GitService) Fetch(ctx context.Context) error {
	if s == nil {
		return errors.New("git service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.runRemote(ctx, "fetch", "--all", "--prune")
	return err
}

// Pull merges the upstream into the current branch. Merge (not rebase) is
// used regardless of pull.rebase so a conflict always leaves the same
// resolvable state; conflicts are reported in the result.
func (s *GitService) Pull(ctx context.Context) (GitSyncResult, error) {
	if s == nil {
		return GitSyncResult{}, errors.New("git service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.runRemote(ctx, "pull", "--no-rebase", "--no-edit")
	return s.syncResult(ctx, out, err)
}

// Merge merges ref into the current branch.
func (s *GitService) Merge(ctx context.Context, ref string) (GitSyncResult, error) {
	if s == nil {
		return GitSyncResult{}, errors.New("git service is unavailable")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "-") {
		return GitSyncResult{}, errors.New("merge needs a branch name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.runRemote(ctx, "merge", "--no-edit", ref)
	return s.syncResult(ctx, out, err)
}

// syncResult turns a stopped merge into a conflict result and anything else
// into an error.
func (s *GitService) syncResult(ctx context.Context, out string, err error) (GitSyncResult, error) {
	if err == nil {
		return GitSyncResult{Summary: lastLine(out)}, nil
	}
	conflicts, listErr := s.conflictedPaths(ctx)
	if listErr == nil && len(conflicts) > 0 {
		return GitSyncResult{Summary: "Merge stopped with conflicts", Conflicts: conflicts}, nil
	}
	return GitSyncResult{}, err
}

func (s *GitService) conflictedPaths(ctx context.Context) ([]string, error) {
	repo, root, err := s.open()
	if err != nil {
		return nil, err
	}
	closeGitRepository(repo)
	out, err := exec.CommandContext(ctx, "git", "-C", root, "diff", "--name-only", "--diff-filter=U", "-z").Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// Push publishes the current branch. A branch without an upstream is pushed
// to its only (or "origin") remote and tracked. Pushes are never forced.
func (s *GitService) Push(ctx context.Context) error {
	if s == nil {
		return errors.New("git service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, _, err := s.open()
	if err != nil {
		return err
	}
	var snapshot GitSnapshot
	snapshot.readHead(repo)
	remotes := gitRemotes(repo)
	closeGitRepository(repo)
	if strings.HasPrefix(snapshot.Branch, "detached@") || snapshot.Branch == "No commits" {
		return errors.New("push needs a checked-out branch with commits")
	}
	if snapshot.Upstream != "" {
		_, err = s.runRemote(ctx, "push")
		return err
	}
	remote := ""
	switch {
	case len(remotes) == 1:
		remote = remotes[0]
	case containsString(remotes, "origin"):
		remote = "origin"
	case len(remotes) == 0:
		return errors.New("this repository has no remote to push to")
	default:
		return fmt.Errorf("%s has no upstream and the repository has several remotes (%s); set one with git push -u <remote> %s", snapshot.Branch, strings.Join(remotes, ", "), snapshot.Branch)
	}
	_, err = s.runRemote(ctx, "push", "--set-upstream", remote, "HEAD")
	return err
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// AbortOperation abandons the merge, rebase, cherry-pick or revert in
// progress and restores the branch as it was before it started.
func (s *GitService) AbortOperation(ctx context.Context) error {
	operation, err := s.operation(ctx)
	if err != nil {
		return err
	}
	if operation == "" {
		return errors.New("no merge is in progress")
	}
	return s.mutate(ctx, operation, "--abort")
}

// ContinueOperation completes the operation once every conflict is resolved,
// keeping Git's prepared message (no editor is opened).
func (s *GitService) ContinueOperation(ctx context.Context) error {
	operation, err := s.operation(ctx)
	if err != nil {
		return err
	}
	if operation == "" {
		return errors.New("no merge is in progress")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conflicts, err := s.conflictedPaths(ctx)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%d file(s) still have conflicts: %s", len(conflicts), strings.Join(conflicts, ", "))
	}
	repo, root, err := s.open()
	if err != nil {
		return err
	}
	closeGitRepository(repo)
	args := []string{operation, "--continue"}
	if operation == GitOperationMerge {
		args = []string{"commit", "--no-edit"}
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *GitService) operation(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("git service is unavailable")
	}
	repo, root, err := s.open()
	if err != nil {
		return "", err
	}
	closeGitRepository(repo)
	snapshot := GitSnapshot{RepositoryRoot: root}
	snapshot.readOperation(ctx)
	return snapshot.Operation, nil
}

// ConflictFile reads and parses one conflicted working file.
func (s *GitService) ConflictFile(ctx context.Context, path string) (GitConflictFile, error) {
	if s == nil {
		return GitConflictFile{}, errors.New("git service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, root, err := s.open()
	if err != nil {
		return GitConflictFile{}, err
	}
	closeGitRepository(repo)
	clean := cleanGitPaths([]string{path})
	if len(clean) != 1 || strings.HasPrefix(clean[0], "../") {
		return GitConflictFile{}, fmt.Errorf("%q is not a repository path", path)
	}
	conflicts, err := s.conflictedPaths(ctx)
	if err != nil {
		return GitConflictFile{}, err
	}
	if !containsString(conflicts, clean[0]) {
		return GitConflictFile{}, fmt.Errorf("%s is not conflicted", clean[0])
	}
	absolute := filepath.Join(root, filepath.FromSlash(clean[0]))
	data, err := os.ReadFile(absolute)
	if err != nil {
		return GitConflictFile{}, err
	}
	if !utf8.Valid(data) {
		return GitConflictFile{}, fmt.Errorf("%s is binary; resolve it in a terminal with git checkout --ours/--theirs", clean[0])
	}
	segments, err := ParseGitConflicts(string(data))
	if err != nil {
		return GitConflictFile{}, fmt.Errorf("%s: %w", clean[0], err)
	}
	return GitConflictFile{Path: clean[0], AbsolutePath: absolute, Fingerprint: gitContentFingerprint(string(data)), Segments: segments}, nil
}

// ResolveGitConflict writes the resolved text of a conflicted file and marks
// it resolved, after a mandatory Local History snapshot of the conflicted
// bytes. A file that changed since it was read is left alone.
func (workspace *Workspace) ResolveGitConflict(ctx context.Context, path, fingerprint string, choices []GitConflictChoice) (string, error) {
	if workspace.LocalHistory == nil || workspace.Git == nil {
		return "", errors.New("resolving conflicts requires the repository and the Local History recovery store")
	}
	file, err := workspace.Git.ConflictFile(ctx, path)
	if err != nil {
		return "", err
	}
	if file.Fingerprint != fingerprint {
		return "", ErrGitReviewStale
	}
	resolved, err := ResolveGitConflicts(file.Segments, choices)
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
		data, err := os.ReadFile(file.AbsolutePath)
		if err != nil {
			return err
		}
		if gitContentFingerprint(string(data)) != fingerprint {
			return ErrGitReviewStale
		}
		return nil
	}
	if err := verify(); err != nil {
		return "", err
	}
	safety, err := workspace.LocalHistory.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before resolving Git conflict: " + file.Path, IncludePaths: []string{file.Path}, DisableGitIgnore: true, MaxFileSize: gitComparisonLimit})
	if err != nil {
		return "", err
	}
	if entry, exists := safety.Entries[file.Path]; !exists || entry.Type != localhistory.EntryFile {
		return "", errors.New("mandatory recovery did not capture the file; resolution aborted")
	}
	info, err := os.Stat(file.AbsolutePath)
	if err != nil {
		return safety.ID, err
	}
	if err := fileio.WriteAtomicChecked(file.AbsolutePath, []byte(resolved), info.Mode().Perm(), verify); err != nil {
		return safety.ID, err
	}
	return safety.ID, service.run(ctx, "add", "--", file.Path)
}
