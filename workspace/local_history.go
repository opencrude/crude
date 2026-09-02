package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"crude/localhistory"
)

type HistoryRestored struct {
	Result localhistory.RestoreResult
}

func openLocalHistory(projectDir, workspaceDir string) (*localhistory.Store, error) {
	if workspaceDir == "" {
		var err error
		workspaceDir, err = WorkspaceDir(projectDir)
		if err != nil {
			return nil, err
		}
	}
	return localhistory.Open(filepath.Join(workspaceDir, "local-history"), projectDir)
}

func (w *Workspace) CaptureHistory(ctx context.Context, opts localhistory.SnapshotOptions) (localhistory.Manifest, error) {
	if w == nil || w.LocalHistory == nil {
		return localhistory.Manifest{}, errors.New("workspace local history is unavailable")
	}
	return w.LocalHistory.Snapshot(ctx, opts)
}

func (w *Workspace) PreviewHistoryRestore(ctx context.Context, snapshotID string, opts localhistory.SnapshotOptions) (localhistory.RestorePlan, error) {
	if w == nil || w.LocalHistory == nil {
		return localhistory.RestorePlan{}, errors.New("workspace local history is unavailable")
	}
	return w.LocalHistory.PlanRestore(ctx, snapshotID, opts)
}

func (w *Workspace) ApplyHistoryRestore(ctx context.Context, planID string, selected []string) (localhistory.RestoreResult, error) {
	if w == nil || w.LocalHistory == nil {
		return localhistory.RestoreResult{}, errors.New("workspace local history is unavailable")
	}
	if w.historyTurnActive() {
		return localhistory.RestoreResult{}, errors.New("stop active agent turns before restoring workspace history")
	}
	var result localhistory.RestoreResult
	var err error
	if w.HistoryService != nil {
		result, err = w.HistoryService.ApplyRestore(ctx, planID, selected)
	} else {
		result, err = w.LocalHistory.ApplyRestore(ctx, planID, selected)
	}
	if err != nil {
		return localhistory.RestoreResult{}, err
	}
	if w.eventBus != nil {
		w.eventBus.Send(HistoryRestored{Result: result})
	}
	return result, nil
}

func (w *Workspace) ApplyHistoryRestoreChoices(ctx context.Context, planID string, choices []localhistory.RestoreChoice) (localhistory.RestoreResult, error) {
	if w == nil || w.LocalHistory == nil {
		return localhistory.RestoreResult{}, errors.New("workspace local history is unavailable")
	}
	if w.historyTurnActive() {
		return localhistory.RestoreResult{}, errors.New("stop active agent turns before restoring workspace history")
	}
	var result localhistory.RestoreResult
	var err error
	if w.HistoryService != nil {
		result, err = w.HistoryService.ApplyRestoreChoices(ctx, planID, choices)
	} else {
		result, err = w.LocalHistory.ApplyRestoreChoices(ctx, planID, choices)
	}
	if err == nil && w.eventBus != nil {
		w.eventBus.Send(HistoryRestored{Result: result})
	}
	return result, err
}

func (w *Workspace) historyTurnActive() bool {
	for _, frontend := range w.Sessions {
		if frontend != nil && frontend.backend != nil && frontend.backend.HasActiveTurns() {
			return true
		}
	}
	return false
}

func (w *Workspace) AppendHistoryRestoreEvent(result localhistory.RestoreResult) error {
	if w == nil || w.ActiveSession == nil {
		return nil
	}
	display := fmt.Sprintf("Restored %d file(s) from Local History · safety snapshot %s", len(result.Paths), shortHistory(result.SafetySnapshotID))
	hidden := fmt.Sprintf("<workspace_history_event>\nRestored %d paths to snapshot %s. Restore plan: %s. Safety snapshot: %s.\n</workspace_history_event>", len(result.Paths), result.TargetSnapshotID, result.PlanID, result.SafetySnapshotID)
	if w.ActiveSession.backend != nil {
		if err := w.ActiveSession.backend.AppendWorkspaceHistoryEvent(hidden, display); err != nil {
			return err
		}
	}
	if w.ActiveSession.AgentState != nil && w.ActiveSession.AgentState.Messages != nil {
		w.ActiveSession.AgentState.Messages.AppendMessage(Message{InternalID: fmt.Sprintf("history-status-%d", time.Now().UnixNano()), Role: MessageRoleStatus, Content: display, Time: time.Now()})
	}
	return nil
}

func shortHistory(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
