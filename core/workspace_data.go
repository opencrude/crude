package core

import (
	"context"
	"errors"
	"fmt"

	"crude/agent/worktree"
	"crude/core/coreprotocol"
	"crude/workspace"
	"crude/workspace/localhistory"
)

// prepareWorkspaceData serves the Workspace panel's Overview and Storage
// tabs. Everything the worker needs is captured here, on the actor; the
// worker never reads workspace maps.
func (a *actor) prepareWorkspaceData(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.WorkspaceUsage, coreprotocol.WorkspaceStorage, coreprotocol.WorkspaceCleanup:
	default:
		return nil, false
	}
	dir := a.ws.WorkspaceDir
	open := make(map[string]bool, len(a.ws.Sessions))
	for _, session := range a.ws.Sessions {
		open[session.SessionID] = true
	}
	if a.usage == nil || a.usageDir != dir {
		a.usage, a.usageDir = workspace.NewUsageReader(dir), dir
	}
	usage := a.usage
	var cleanup coreprotocol.WorkspaceCleanupRequest
	if request.Method == coreprotocol.WorkspaceCleanup {
		if err := decodeRequest(request, &cleanup); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
	}
	store, settings := a.ws.LocalHistory, a.ws.LoadAppSettings().LocalHistory
	executions := a.ws.Executions
	var retention func(context.Context, worktree.Owner, string, string) error
	if executions != nil {
		retention = a.executionRetentionCheck()
	}
	return func(ctx context.Context) coreprotocol.Response {
		if dir == "" {
			return fault(request, "unavailable", "this workspace has no saved data folder")
		}
		switch request.Method {
		case coreprotocol.WorkspaceUsage:
			report, err := usage.Report(ctx)
			if err != nil {
				return fault(request, "workspace_data_failed", err.Error())
			}
			return response(request, coreprotocol.WorkspaceUsageResult{Report: report})
		case coreprotocol.WorkspaceStorage:
			report, err := workspace.MeasureStorage(ctx, dir, open)
			if err != nil {
				return fault(request, "workspace_data_failed", err.Error())
			}
			if executions != nil {
				report.SetItems(workspace.StorageWorktrees, separateCheckouts(executions.List()))
			}
			return response(request, coreprotocol.WorkspaceStorageResult{Report: report})
		}
		var result workspace.CleanupResult
		var err error
		switch cleanup.Kind {
		case workspace.StorageTemporary:
			result, err = workspace.RemoveTemporaryFiles(ctx, dir, open)
		case workspace.StorageHistory:
			result, err = pruneHistory(ctx, store, settings.RetentionKeepHours, settings.RetentionKeepDaily, settings.RetentionMaxSnapshots, settings.StorageBudgetMB)
		case workspace.StorageWorktrees:
			result, err = removeFinishedWorktrees(ctx, executions, retention)
		default:
			return fault(request, "invalid_request", fmt.Sprintf("unknown cleanup %q", cleanup.Kind))
		}
		if err != nil {
			return fault(request, "workspace_cleanup_failed", err.Error())
		}
		return response(request, coreprotocol.WorkspaceCleanupResult{Result: result})
	}, true
}

// pruneHistory applies the configured retention now, as the idle background
// cleanup does. Snapshots that protect a reviewed edit or a restore stay.
func pruneHistory(ctx context.Context, store *localhistory.Store, keepHours, keepDaily, maxSnapshots, budgetMB int) (workspace.CleanupResult, error) {
	result := workspace.CleanupResult{Kind: workspace.StorageHistory}
	if store == nil {
		return result, errors.New("local history is off for this workspace")
	}
	pruned, err := store.Prune(ctx, localhistory.RetentionPolicy{KeepHours: keepHours, KeepDaily: keepDaily, MaxSnapshots: maxSnapshots, BudgetBytes: int64(budgetMB) * 1024 * 1024})
	result.Removed, result.FreedBytes = pruned.RemovedSnapshots, pruned.FreedBytes
	return result, err
}

// separateCheckouts counts the executions that keep their own checkout.
func separateCheckouts(contexts []worktree.Context) int {
	count := 0
	for _, c := range contexts {
		if !c.Removed && c.Mode == worktree.Isolated {
			count++
		}
	}
	return count
}

// removeFinishedWorktrees removes the checkouts that worktree cleanup accepts:
// settled, not kept, and integrated or unchanged. Each refusal is kept with
// its reason, so a checkout with work in it is never lost.
func removeFinishedWorktrees(ctx context.Context, service *workspace.ExecutionService, retention func(context.Context, worktree.Owner, string, string) error) (workspace.CleanupResult, error) {
	result := workspace.CleanupResult{Kind: workspace.StorageWorktrees}
	if service == nil || retention == nil {
		return result, errors.New("worktrees are not available in this workspace")
	}
	for _, summary := range service.List() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if summary.Removed {
			continue
		}
		c, err := service.InspectSummary(summary.ID)
		if err != nil {
			result.Keep(err.Error())
			continue
		}
		switch {
		case c.Removed:
			continue
		case c.Kept:
			result.Keep("Kept on purpose")
			continue
		case c.Investigation != nil:
			result.Keep("A plan investigation closes it")
			continue
		}
		if len(c.Pins) != 0 {
			reconciled, err := reconcileExecution(ctx, service.Service, c.ID, retention)
			if err != nil || len(reconciled.Retained) != 0 {
				result.Keep("A running agent or command uses it")
				continue
			}
		}
		size := int64(0)
		if c.Mode != worktree.Shared {
			size = workspace.DirectorySize(c.Root)
		}
		owner, id, root := c.Owner, c.ID, c.Root
		err = service.Cleanup(ctx, id, owner, worktree.CleanupOptions{Check: func(ctx context.Context) error { return retention(ctx, owner, id, root) }})
		if err != nil {
			result.Keep(err.Error())
			continue
		}
		result.Removed++
		result.FreedBytes += size
	}
	return result, nil
}
