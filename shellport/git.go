package shellport

import (
	"context"

	"crude/workspace"
)

// SourceControl is the Git surface of a workspace.
type SourceControl interface {
	// Git is the source-control service for the workspace root; nil until EnsureGit.
	Git() GitService
	// EnsureGit creates the source-control service on first use.
	EnsureGit() GitService
	PreviewGitDiscard(ctx context.Context, change workspace.GitChange) (workspace.GitDiscardPreview, error)
	DiscardGitReviewed(ctx context.Context, preview workspace.GitDiscardPreview) (string, error)
	// DiscardGitHunk reverts one hunk of a tracked working-tree file on disk after
	// capturing a mandatory Local History snapshot. Any newer change aborts.
	DiscardGitHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) (string, error)
	// ResolveGitConflict writes one choice per conflict region of a conflicted
	// file after a mandatory Local History snapshot, then marks it resolved.
	// A file that changed since fingerprint was read is left alone.
	ResolveGitConflict(ctx context.Context, path, fingerprint string, choices []workspace.GitConflictChoice) (string, error)
	// PrepareCommitMessageWriter captures a writer without performing I/O. A
	// connected writer resolves the core-owned Aeron configuration in Write.
	PrepareCommitMessageWriter() (CommitMessageWriter, error)
}

// CommitMessageWriter performs repository reads and model generation off the
// shell's owner thread. Its implementation never transfers a runtime handle.
type CommitMessageWriter interface {
	Write(ctx context.Context, stageAll bool, emit func(string)) (string, error)
}

// GitService owns source-control reads and mutations.
type GitService interface {
	// AnnotateFile reads blame and HEAD-vs-content line edits for absPath.
	// content is the editor buffer (unsaved text included). Results are
	// presentation-neutral; the TUI owns gutter paint and hover.
	AnnotateFile(ctx context.Context, absPath, content string) (workspace.GitFileAnnotation, error)
	// Checkout switches to an existing local branch, or creates and switches to
	// a local tracking branch when given a remote-tracking name (origin/foo).
	// This is the go-git equivalent of `git switch <branch>`: passing "origin/x"
	// creates local "x" tracking "origin/x", while passing plain "x" switches to
	Checkout(ctx context.Context, branch string) error
	// CommitStaged commits the index with Git itself, so hooks, signing and
	// commit templates behave exactly as they do in a terminal. It fails closed
	// with ErrGitReviewStale when the staged path set differs from expected,
	// the set the user was shown; nothing is committed in that case.
	CommitStaged(ctx context.Context, expected []string, message string) error
	// CompareChanges reads repository state once and compares every requested
	// change against it, so a multi-file review is one consistent snapshot rather
	// than N independently timed reads.
	CompareChanges(ctx context.Context, changes []workspace.GitChange) ([]workspace.GitChangeComparison, error)
	InspectCommit(ctx context.Context, hash string) (workspace.GitCommitEvidence, error)
	// Snapshot reads status, branch, and recent history without mutating the repo.
	Snapshot(ctx context.Context) (workspace.GitSnapshot, error)
	// Stage adds the provided repository-relative paths to the index.
	Stage(ctx context.Context, paths ...string) error
	// StageHunk records one hunk of a working-tree comparison in the index; the
	// rest of the file keeps its index bytes. Fails closed on any newer change.
	StageHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) error
	StageReviewed(ctx context.Context, comparison workspace.GitComparison) error
	// Unstage restores the provided paths in the index while preserving working
	// tree edits. The reset fallback supports repositories without restore.
	Unstage(ctx context.Context, paths ...string) error
	// UnstageHunk restores one hunk of a staged comparison to its HEAD bytes in
	// the index; the working tree is untouched.
	UnstageHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) error
	UnstageReviewed(ctx context.Context, comparison workspace.GitComparison) error
	// Fetch updates remote-tracking branches; Pull merges the upstream (never
	// rebases) and Merge merges a ref, both reporting conflicts as a result.
	// Push never forces and publishes a new branch to its only remote.
	Fetch(ctx context.Context) error
	Pull(ctx context.Context) (workspace.GitSyncResult, error)
	Merge(ctx context.Context, ref string) (workspace.GitSyncResult, error)
	Push(ctx context.Context) error
	// AbortOperation and ContinueOperation end the merge, rebase, cherry-pick
	// or revert waiting for conflict resolution.
	AbortOperation(ctx context.Context) error
	ContinueOperation(ctx context.Context) error
	// ConflictFile parses one conflicted working file for resolution.
	ConflictFile(ctx context.Context, path string) (workspace.GitConflictFile, error)
}
