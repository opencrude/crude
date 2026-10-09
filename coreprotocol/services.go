// Typed, closed service contracts. No method names or executable callbacks are carried in payloads.
package coreprotocol

import (
	"crude/memory"
	"crude/workspace"
)

const (
	GitSnapshot          = "git.snapshot"
	GitCompare           = "git.compare"
	GitCompareChanges    = "git.compare_changes"
	GitAnnotateFile      = "git.annotate_file"
	GitStageReviewed     = "git.stage_reviewed"
	GitUnstageReviewed   = "git.unstage_reviewed"
	GitStageHunk         = "git.stage_hunk"
	GitUnstageHunk       = "git.unstage_hunk"
	GitCommitStaged      = "git.commit_staged"
	GitInspectCommit     = "git.inspect_commit"
	GitCheckout          = "git.checkout"
	PreviewGitDiscard    = "git.discard_preview"
	DiscardGitReviewed   = "git.discard_reviewed"
	DiscardGitHunk       = "git.discard_hunk"
	SearchMemories       = "memory.search"
	SearchMemoryEpisodes = "memory.episodes"
	InspectMemory        = "memory.inspect"
	PinMemory            = "memory.pin"
	ForgetMemoryRecord   = "memory.forget"
	CorrectMemory        = "memory.correct"
	BoardSnapshot        = "board.snapshot"
	MarkBoardRead        = "board.mark_read"
)

// GitChangeComparison serializes per-file failures explicitly, never as an error interface.
type GitChangeComparison struct {
	Change     workspace.GitChange     `json:"change"`
	Comparison workspace.GitComparison `json:"comparison"`
	Error      *Fault                  `json:"error,omitempty"`
}

type GitSnapshotRequest struct {
}
type GitSnapshotResult struct {
	Snapshot workspace.GitSnapshot `json:"snapshot"`
}

type GitCompareRequest struct {
	Change workspace.GitChange `json:"change"`
}
type GitCompareResult struct {
	Comparison workspace.GitComparison `json:"comparison"`
}

type GitCompareChangesRequest struct {
	Changes []workspace.GitChange `json:"changes"`
}
type GitCompareChangesResult struct {
	Comparisons []GitChangeComparison `json:"comparisons"`
}

type GitAnnotateFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type GitAnnotateFileResult struct {
	Annotation workspace.GitFileAnnotation `json:"annotation"`
}

type GitStageReviewedRequest struct {
	Comparison workspace.GitComparison `json:"comparison"`
}
type GitStageReviewedResult struct {
}

type GitUnstageReviewedRequest struct {
	Comparison workspace.GitComparison `json:"comparison"`
}
type GitUnstageReviewedResult struct {
}

type GitStageHunkRequest struct {
	Comparison workspace.GitComparison `json:"comparison"`
	Hunk       workspace.GitHunkRange  `json:"hunk"`
}
type GitStageHunkResult struct {
}

type GitUnstageHunkRequest struct {
	Comparison workspace.GitComparison `json:"comparison"`
	Hunk       workspace.GitHunkRange  `json:"hunk"`
}
type GitUnstageHunkResult struct {
}

type GitCommitStagedRequest struct {
	Expected []string `json:"expected"`
	Message  string   `json:"message"`
}
type GitCommitStagedResult struct {
}

type GitInspectCommitRequest struct {
	Hash string `json:"hash"`
}
type GitInspectCommitResult struct {
	Evidence workspace.GitCommitEvidence `json:"evidence"`
}

type GitCheckoutRequest struct {
	Branch string `json:"branch"`
}
type GitCheckoutResult struct {
}

type PreviewGitDiscardRequest struct {
	Change workspace.GitChange `json:"change"`
}
type PreviewGitDiscardResult struct {
	Preview workspace.GitDiscardPreview `json:"preview"`
}

type DiscardGitReviewedRequest struct {
	Preview workspace.GitDiscardPreview `json:"preview"`
}
type DiscardGitReviewedResult struct {
	SafetyID string `json:"safety_id"`
}

type DiscardGitHunkRequest struct {
	Comparison workspace.GitComparison `json:"comparison"`
	Hunk       workspace.GitHunkRange  `json:"hunk"`
}
type DiscardGitHunkResult struct {
	SafetyID string `json:"safety_id"`
}

type SearchMemoriesRequest struct {
	Query memory.ListQuery `json:"query"`
}
type SearchMemoriesResult struct {
	Records []memory.ScoredRecord `json:"records"`
}

type SearchMemoryEpisodesRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}
type SearchMemoryEpisodesResult struct {
	Episodes []memory.Episode `json:"episodes"`
}

type InspectMemoryRequest struct {
	ID string `json:"id"`
}
type InspectMemoryResult struct {
	Detail workspace.MemoryDetail `json:"detail"`
}

type PinMemoryRequest struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Pinned           bool   `json:"pinned"`
}
type PinMemoryResult struct {
}

type ForgetMemoryRecordRequest struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type ForgetMemoryRecordResult struct {
}

type CorrectMemoryRequest struct {
	Replacement memory.Record `json:"replacement"`
}
type CorrectMemoryResult struct {
	Record memory.Record `json:"record"`
}

type BoardSnapshotRequest struct {
	Query    string `json:"query"`
	Thread   string `json:"thread"`
	Since    int64  `json:"since"`
	Snapshot int64  `json:"snapshot"`
}
type BoardSnapshotResult struct {
	Snapshot workspace.BoardSnapshot `json:"snapshot"`
}

type MarkBoardReadRequest struct {
	Through int64 `json:"through"`
}
type MarkBoardReadResult struct {
}
