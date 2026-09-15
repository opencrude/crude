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
	if w == nil {
		return nil
	}
	frontend := w.ActiveSession
	if result.SessionID != "" {
		frontend = w.SessionsByID[result.SessionID]
	}
	if frontend == nil {
		return fmt.Errorf("workspace history restore owner is unavailable")
	}
	display := fmt.Sprintf("Restored %d file(s) from Local History · safety snapshot %s", len(result.Paths), shortHistory(result.SafetySnapshotID))
	hidden := fmt.Sprintf("<workspace_history_event>\nRestored %d paths to snapshot %s. Restore plan: %s. Safety snapshot: %s.\n</workspace_history_event>", len(result.Paths), result.TargetSnapshotID, result.PlanID, result.SafetySnapshotID)
	agentState := frontend.AgentState
	childPersisted := false
	if frontend.backend != nil {
		if err := frontend.backend.AppendWorkspaceHistoryEventWithID("history-restore-"+result.PlanID, hidden, display); err != nil {
			return err
		}
		if child, err := frontend.backend.AppendSubAgentHistoryStatus(result.AgentID, "history-status-"+result.PlanID, display); err != nil {
			return err
		} else if child {
			childPersisted = true
		}
	}
	if result.AgentID != "" && w.AgentMgr != nil {
		if node := w.AgentMgr.Get(result.AgentID); node != nil && node.State != nil {
			agentState = node.State
		} else if childPersisted {
			agentState = nil
		}
	}
	if agentState != nil && agentState.Messages != nil {
		statusID := "history-status-" + result.PlanID
		found := false
		for _, message := range agentState.Messages.Messages() {
			if message.InternalID == statusID {
				found = true
				break
			}
		}
		if !found {
			agentState.Messages.AppendMessage(Message{InternalID: statusID, Role: MessageRoleStatus, Content: display, Time: time.Now()})
		}
	}
	if w.HistoryService != nil {
		if err := w.HistoryService.MarkRestoreNotified(result.PlanID); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) deliverPendingHistoryNotifications(sessionID string) {
	if w == nil || w.HistoryService == nil || sessionID == "" {
		return
	}
	if err := w.retryPendingHistoryNotifications(sessionID); err != nil {
		w.Logger.Warn("HISTORY", "notification_delivery_failed", "session_id", sessionID, "error", err.Error())
	}
}

func (w *Workspace) retryPendingHistoryNotifications(sessionID string) error {
	if w == nil || w.HistoryService == nil || sessionID == "" {
		return nil
	}
	results, err := w.HistoryService.PendingRestoreNotifications(sessionID)
	if err != nil {
		return err
	}
	for _, result := range results {
		if err := w.AppendHistoryRestoreEvent(result); err != nil {
			return fmt.Errorf("deliver restore notification %s: %w", result.PlanID, err)
		}
	}
	return nil
}

func shortHistory(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
