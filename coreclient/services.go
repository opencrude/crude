package coreclient

import (
	"context"
	"crude/coreprotocol"
	"crude/memory"
	"crude/workspace"
)

// GitService is a remote facade; it owns no repository or runtime state.
type GitService struct {
	client      *Client
	workspaceID string
}

func NewGitService(client *Client, id string) *GitService {
	return &GitService{client: client, workspaceID: id}
}

func (s *GitService) Snapshot(ctx context.Context) (workspace.GitSnapshot, error) {
	var out coreprotocol.GitSnapshotResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitSnapshot, coreprotocol.GitSnapshotRequest{}, &out)
	return out.Snapshot, err
}

func (s *GitService) Compare(ctx context.Context, change workspace.GitChange) (workspace.GitComparison, error) {
	var out coreprotocol.GitCompareResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitCompare, coreprotocol.GitCompareRequest{Change: change}, &out)
	return out.Comparison, err
}

func (s *GitService) CompareChanges(ctx context.Context, changes []workspace.GitChange) ([]workspace.GitChangeComparison, error) {
	var out coreprotocol.GitCompareChangesResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitCompareChanges, coreprotocol.GitCompareChangesRequest{Changes: changes}, &out)
	items := make([]workspace.GitChangeComparison, len(out.Comparisons))
	for i, item := range out.Comparisons {
		items[i] = workspace.GitChangeComparison{Change: item.Change, Comparison: item.Comparison}
		if item.Error != nil {
			items[i].Err = item.Error
		}
	}
	return items, err
}

func (s *GitService) AnnotateFile(ctx context.Context, path string, content string) (workspace.GitFileAnnotation, error) {
	var out coreprotocol.GitAnnotateFileResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitAnnotateFile, coreprotocol.GitAnnotateFileRequest{Path: path, Content: content}, &out)
	return out.Annotation, err
}

func (s *GitService) StageReviewed(ctx context.Context, comparison workspace.GitComparison) error {
	var out coreprotocol.GitStageReviewedResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitStageReviewed, coreprotocol.GitStageReviewedRequest{Comparison: comparison}, &out)
	return err
}

func (s *GitService) UnstageReviewed(ctx context.Context, comparison workspace.GitComparison) error {
	var out coreprotocol.GitUnstageReviewedResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitUnstageReviewed, coreprotocol.GitUnstageReviewedRequest{Comparison: comparison}, &out)
	return err
}

func (s *GitService) StageHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) error {
	var out coreprotocol.GitStageHunkResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitStageHunk, coreprotocol.GitStageHunkRequest{Comparison: comparison, Hunk: hunk}, &out)
	return err
}

func (s *GitService) UnstageHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) error {
	var out coreprotocol.GitUnstageHunkResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitUnstageHunk, coreprotocol.GitUnstageHunkRequest{Comparison: comparison, Hunk: hunk}, &out)
	return err
}

func (s *GitService) CommitStaged(ctx context.Context, expected []string, message string) error {
	var out coreprotocol.GitCommitStagedResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitCommitStaged, coreprotocol.GitCommitStagedRequest{Expected: expected, Message: message}, &out)
	return err
}

func (s *GitService) InspectCommit(ctx context.Context, hash string) (workspace.GitCommitEvidence, error) {
	var out coreprotocol.GitInspectCommitResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitInspectCommit, coreprotocol.GitInspectCommitRequest{Hash: hash}, &out)
	return out.Evidence, err
}

func (s *GitService) Checkout(ctx context.Context, branch string) error {
	var out coreprotocol.GitCheckoutResult
	err := s.client.Call(ctx, s.workspaceID, coreprotocol.GitCheckout, coreprotocol.GitCheckoutRequest{Branch: branch}, &out)
	return err
}

func (v *WorkspaceView) PreviewGitDiscard(ctx context.Context, change workspace.GitChange) (workspace.GitDiscardPreview, error) {
	var out coreprotocol.PreviewGitDiscardResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.PreviewGitDiscard, coreprotocol.PreviewGitDiscardRequest{Change: change}, &out)
	return out.Preview, err
}

func (v *WorkspaceView) DiscardGitReviewed(ctx context.Context, preview workspace.GitDiscardPreview) (string, error) {
	var out coreprotocol.DiscardGitReviewedResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.DiscardGitReviewed, coreprotocol.DiscardGitReviewedRequest{Preview: preview}, &out)
	return out.SafetyID, err
}

func (v *WorkspaceView) DiscardGitHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) (string, error) {
	var out coreprotocol.DiscardGitHunkResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.DiscardGitHunk, coreprotocol.DiscardGitHunkRequest{Comparison: comparison, Hunk: hunk}, &out)
	return out.SafetyID, err
}

func (v *WorkspaceView) SearchMemories(ctx context.Context, query memory.ListQuery) ([]memory.ScoredRecord, error) {
	var out coreprotocol.SearchMemoriesResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SearchMemories, coreprotocol.SearchMemoriesRequest{Query: query}, &out)
	return out.Records, err
}

func (v *WorkspaceView) SearchMemoryEpisodes(ctx context.Context, query string, limit int) ([]memory.Episode, error) {
	var out coreprotocol.SearchMemoryEpisodesResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.SearchMemoryEpisodes, coreprotocol.SearchMemoryEpisodesRequest{Query: query, Limit: limit}, &out)
	return out.Episodes, err
}

func (v *WorkspaceView) InspectMemory(ctx context.Context, iD string) (workspace.MemoryDetail, error) {
	var out coreprotocol.InspectMemoryResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.InspectMemory, coreprotocol.InspectMemoryRequest{ID: iD}, &out)
	return out.Detail, err
}

func (v *WorkspaceView) PinMemory(ctx context.Context, iD string, expectedRevision int64, pinned bool) error {
	var out coreprotocol.PinMemoryResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.PinMemory, coreprotocol.PinMemoryRequest{ID: iD, ExpectedRevision: expectedRevision, Pinned: pinned}, &out)
	return err
}

func (v *WorkspaceView) ForgetMemoryRecord(ctx context.Context, iD string, expectedRevision int64) error {
	var out coreprotocol.ForgetMemoryRecordResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.ForgetMemoryRecord, coreprotocol.ForgetMemoryRecordRequest{ID: iD, ExpectedRevision: expectedRevision}, &out)
	return err
}

func (v *WorkspaceView) CorrectMemory(ctx context.Context, replacement memory.Record) (memory.Record, error) {
	var out coreprotocol.CorrectMemoryResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.CorrectMemory, coreprotocol.CorrectMemoryRequest{Replacement: replacement}, &out)
	return out.Record, err
}

func (v *WorkspaceView) BoardSnapshot(ctx context.Context, query string, thread string, since int64, snapshot int64) (workspace.BoardSnapshot, error) {
	var out coreprotocol.BoardSnapshotResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.BoardSnapshot, coreprotocol.BoardSnapshotRequest{Query: query, Thread: thread, Since: since, Snapshot: snapshot}, &out)
	return out.Snapshot, err
}

func (v *WorkspaceView) MarkBoardRead(ctx context.Context, through int64) error {
	var out coreprotocol.MarkBoardReadResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.MarkBoardRead, coreprotocol.MarkBoardReadRequest{Through: through}, &out)
	return err
}
