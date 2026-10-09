package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/memory"
	"crude/workspace"
)

// serviceCall is a typed adapter, not a dynamic dispatcher. Preparation and decoding
// happen on the actor; the captured operation alone runs on its joined worker.
func serviceCall[I, O any](request coreprotocol.Request, run func(context.Context, I) (O, error)) (func(context.Context) coreprotocol.Response, bool) {
	var input I
	if err := decode(request.Payload, &input); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		out, err := run(ctx, input)
		result := response(request, out)
		if err != nil {
			result.Error = &coreprotocol.Fault{Code: "service_failed", Message: err.Error()}
		}
		return result
	}, true
}

// Workspace/service lifetimes remain fixed until the actor joins all workers.
func (a *actor) prepareService(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	ws, git := a.ws, a.ws.Git
	var store *memory.Store
	if ws.WorkspaceDB != nil {
		store = ws.WorkspaceDB.Memory
	}
	board := ws.Collaboration
	switch request.Method {
	case coreprotocol.GitSnapshot:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitSnapshotRequest) (coreprotocol.GitSnapshotResult, error) {
			if git == nil {
				return coreprotocol.GitSnapshotResult{}, errors.New("git service is unavailable")
			}
			value, err := git.Snapshot(ctx)
			return coreprotocol.GitSnapshotResult{Snapshot: value}, err
		})
	case coreprotocol.GitCompare:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitCompareRequest) (coreprotocol.GitCompareResult, error) {
			if git == nil {
				return coreprotocol.GitCompareResult{}, errors.New("git service is unavailable")
			}
			value, err := git.Compare(ctx, in.Change)
			return coreprotocol.GitCompareResult{Comparison: value}, err
		})
	case coreprotocol.GitCompareChanges:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitCompareChangesRequest) (coreprotocol.GitCompareChangesResult, error) {
			if git == nil {
				return coreprotocol.GitCompareChangesResult{}, errors.New("git service is unavailable")
			}
			value, err := git.CompareChanges(ctx, in.Changes)
			items := make([]coreprotocol.GitChangeComparison, len(value))
			for i, item := range value {
				items[i] = coreprotocol.GitChangeComparison{Change: item.Change, Comparison: item.Comparison}
				if item.Err != nil {
					items[i].Error = &coreprotocol.Fault{Code: "git_compare_failed", Message: item.Err.Error()}
				}
			}
			return coreprotocol.GitCompareChangesResult{Comparisons: items}, err
		})
	case coreprotocol.GitAnnotateFile:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitAnnotateFileRequest) (coreprotocol.GitAnnotateFileResult, error) {
			if git == nil {
				return coreprotocol.GitAnnotateFileResult{}, errors.New("git service is unavailable")
			}
			value, err := git.AnnotateFile(ctx, in.Path, in.Content)
			return coreprotocol.GitAnnotateFileResult{Annotation: value}, err
		})
	case coreprotocol.GitStageReviewed:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitStageReviewedRequest) (coreprotocol.GitStageReviewedResult, error) {
			if git == nil {
				return coreprotocol.GitStageReviewedResult{}, errors.New("git service is unavailable")
			}
			return coreprotocol.GitStageReviewedResult{}, git.StageReviewed(ctx, in.Comparison)
		})
	case coreprotocol.GitUnstageReviewed:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitUnstageReviewedRequest) (coreprotocol.GitUnstageReviewedResult, error) {
			if git == nil {
				return coreprotocol.GitUnstageReviewedResult{}, errors.New("git service is unavailable")
			}
			return coreprotocol.GitUnstageReviewedResult{}, git.UnstageReviewed(ctx, in.Comparison)
		})
	case coreprotocol.GitStageHunk:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitStageHunkRequest) (coreprotocol.GitStageHunkResult, error) {
			if git == nil {
				return coreprotocol.GitStageHunkResult{}, errors.New("git service is unavailable")
			}
			return coreprotocol.GitStageHunkResult{}, git.StageHunk(ctx, in.Comparison, in.Hunk)
		})
	case coreprotocol.GitUnstageHunk:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitUnstageHunkRequest) (coreprotocol.GitUnstageHunkResult, error) {
			if git == nil {
				return coreprotocol.GitUnstageHunkResult{}, errors.New("git service is unavailable")
			}
			return coreprotocol.GitUnstageHunkResult{}, git.UnstageHunk(ctx, in.Comparison, in.Hunk)
		})
	case coreprotocol.GitCommitStaged:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitCommitStagedRequest) (coreprotocol.GitCommitStagedResult, error) {
			if git == nil {
				return coreprotocol.GitCommitStagedResult{}, errors.New("git service is unavailable")
			}
			return coreprotocol.GitCommitStagedResult{}, git.CommitStaged(ctx, in.Expected, in.Message)
		})
	case coreprotocol.GitInspectCommit:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitInspectCommitRequest) (coreprotocol.GitInspectCommitResult, error) {
			if git == nil {
				return coreprotocol.GitInspectCommitResult{}, errors.New("git service is unavailable")
			}
			value, err := git.InspectCommit(ctx, in.Hash)
			return coreprotocol.GitInspectCommitResult{Evidence: value}, err
		})
	case coreprotocol.GitCheckout:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.GitCheckoutRequest) (coreprotocol.GitCheckoutResult, error) {
			if git == nil {
				return coreprotocol.GitCheckoutResult{}, errors.New("git service is unavailable")
			}
			return coreprotocol.GitCheckoutResult{}, git.Checkout(ctx, in.Branch)
		})
	case coreprotocol.PreviewGitDiscard:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.PreviewGitDiscardRequest) (coreprotocol.PreviewGitDiscardResult, error) {
			value, err := ws.PreviewGitDiscard(ctx, in.Change)
			return coreprotocol.PreviewGitDiscardResult{Preview: value}, err
		})
	case coreprotocol.DiscardGitReviewed:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.DiscardGitReviewedRequest) (coreprotocol.DiscardGitReviewedResult, error) {
			value, err := ws.DiscardGitReviewed(ctx, in.Preview)
			return coreprotocol.DiscardGitReviewedResult{SafetyID: value}, err
		})
	case coreprotocol.DiscardGitHunk:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.DiscardGitHunkRequest) (coreprotocol.DiscardGitHunkResult, error) {
			value, err := ws.DiscardGitHunk(ctx, in.Comparison, in.Hunk)
			return coreprotocol.DiscardGitHunkResult{SafetyID: value}, err
		})
	case coreprotocol.SearchMemories:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.SearchMemoriesRequest) (coreprotocol.SearchMemoriesResult, error) {
			var out coreprotocol.SearchMemoriesResult
			if store == nil {
				return out, workspace.ErrMemoryUnavailable
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			records, err := store.List(ctx, in.Query)
			out.Records = records
			return out, err
		})
	case coreprotocol.SearchMemoryEpisodes:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.SearchMemoryEpisodesRequest) (coreprotocol.SearchMemoryEpisodesResult, error) {
			var out coreprotocol.SearchMemoryEpisodesResult
			if store == nil {
				return out, workspace.ErrMemoryUnavailable
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			episodes, err := store.Episodes(ctx, "", in.Limit)
			if err != nil {
				return out, err
			}
			query := strings.ToLower(strings.TrimSpace(in.Query))
			for _, episode := range episodes {
				text := strings.Join([]string{episode.ID, episode.SessionID, episode.Situation, episode.Method, episode.Outcome, episode.Conditions}, " ")
				for _, evidence := range episode.Evidence {
					text += " " + evidence.Summary + " " + evidence.MessageID + " " + evidence.ToolCallID
				}
				if query == "" || strings.Contains(strings.ToLower(text), query) {
					out.Episodes = append(out.Episodes, episode)
				}
			}
			return out, nil
		})
	case coreprotocol.InspectMemory:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.InspectMemoryRequest) (coreprotocol.InspectMemoryResult, error) {
			var out coreprotocol.InspectMemoryResult
			if store == nil {
				return out, workspace.ErrMemoryUnavailable
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var err error
			if out.Detail.Record, err = store.Get(ctx, in.ID); err != nil {
				return out, err
			}
			if out.Detail.Revisions, err = store.Revisions(ctx, in.ID); err != nil {
				return out, err
			}
			if out.Detail.Feedback, err = store.FeedbackForMemory(ctx, in.ID, 100); err != nil {
				return out, err
			}
			out.Detail.Exposures, err = store.ExposuresForMemory(ctx, in.ID, 100)
			return out, err
		})
	case coreprotocol.PinMemory:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.PinMemoryRequest) (coreprotocol.PinMemoryResult, error) {
			var out coreprotocol.PinMemoryResult
			if store == nil {
				return out, workspace.ErrMemoryUnavailable
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return out, store.SetPinned(ctx, in.ID, in.ExpectedRevision, in.Pinned, memory.ActorUser)
		})
	case coreprotocol.ForgetMemoryRecord:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.ForgetMemoryRecordRequest) (coreprotocol.ForgetMemoryRecordResult, error) {
			var out coreprotocol.ForgetMemoryRecordResult
			if store == nil {
				return out, workspace.ErrMemoryUnavailable
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return out, store.Supersede(ctx, in.ID, in.ExpectedRevision, "", memory.ActorUser, "")
		})
	case coreprotocol.CorrectMemory:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.CorrectMemoryRequest) (coreprotocol.CorrectMemoryResult, error) {
			var out coreprotocol.CorrectMemoryResult
			if store == nil {
				return out, workspace.ErrMemoryUnavailable
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			in.Replacement.Source = memory.SourceUser
			record, err := store.Update(ctx, in.Replacement.ID, in.Replacement, memory.ActorUser, "")
			out.Record = record
			return out, err
		})
	case coreprotocol.BoardSnapshot:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.BoardSnapshotRequest) (coreprotocol.BoardSnapshotResult, error) {
			var out coreprotocol.BoardSnapshotResult
			if board == nil {
				return out, errors.New("workspace collaboration runtime is unavailable")
			}
			page, err := board.Page(ctx, collaboration.PageQuery{Since: in.Since, Snapshot: in.Snapshot, ThreadID: in.Thread, Search: in.Query, Limit: collaboration.DefaultPageLimit})
			if err != nil {
				return out, err
			}
			agents, err := board.ListAgents(ctx, collaboration.AgentQuery{IncludeClosed: true, Limit: collaboration.MaxAgentLimit})
			if err != nil {
				return out, err
			}
			unread, err := board.HumanUnread(ctx)
			if err != nil {
				return out, err
			}
			out.Snapshot = workspace.BoardSnapshot{WorkspaceID: board.WorkspaceID(), Page: page, Unread: unread}
			for _, agent := range agents {
				out.Snapshot.Participants = append(out.Snapshot.Participants, workspace.BoardParticipant{Agent: agent})
			}
			return out, nil
		})
	case coreprotocol.MarkBoardRead:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.MarkBoardReadRequest) (coreprotocol.MarkBoardReadResult, error) {
			var out coreprotocol.MarkBoardReadResult
			if board == nil {
				return out, errors.New("workspace collaboration runtime is unavailable")
			}
			if in.Through <= 0 {
				return out, errors.New("only a stable inspected snapshot can be marked read")
			}
			return out, board.MarkHumanRead(ctx, in.Through)
		})
	}
	return nil, false
}

func serviceMutation(method string) bool {
	switch method {
	case coreprotocol.GitStageReviewed, coreprotocol.GitUnstageReviewed, coreprotocol.GitStageHunk, coreprotocol.GitUnstageHunk, coreprotocol.GitCommitStaged, coreprotocol.GitCheckout, coreprotocol.DiscardGitReviewed, coreprotocol.DiscardGitHunk, coreprotocol.PinMemory, coreprotocol.ForgetMemoryRecord, coreprotocol.CorrectMemory, coreprotocol.MarkBoardRead:
		return true
	}
	return false
}

// Called only on the actor after worker completion, never from the worker.
func (a *actor) applyService(request coreprotocol.Request, result coreprotocol.Response) {
	if result.Error == nil && serviceMutation(request.Method) {
		a.changed(nil)
	}
}
