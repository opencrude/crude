package shellport

import (
	"context"

	"crude/localhistory"
)

// History is the local-history surface of a workspace.
type History interface {
	// LocalHistory is the immutable snapshot store; nil when unavailable.
	LocalHistory() LocalHistoryStore
	// HistoryService is the restore-planning service; nil when unavailable.
	HistoryService() LocalHistoryService
	AppendHistoryRestoreEvent(result localhistory.RestoreResult) error
	ApplyHistoryRestoreChoices(ctx context.Context, planID string, choices []localhistory.RestoreChoice) (localhistory.RestoreResult, error)
}

// LocalHistoryStore reads immutable project snapshots and plans restores.
type LocalHistoryStore interface {
	FileHistory(path string, limit int) ([]localhistory.SnapshotRecord, error)
	PlanRestore(ctx context.Context, targetSnapshotID string, opts localhistory.SnapshotOptions) (localhistory.RestorePlan, error)
	PlanRestorePaths(ctx context.Context, targetSnapshotID string, opts localhistory.SnapshotOptions, paths []string) (localhistory.RestorePlan, error)
	Prune(ctx context.Context, policy localhistory.RetentionPolicy) (localhistory.PruneResult, error)
	Root() string
	SnapshotIfChanged(ctx context.Context, opts localhistory.SnapshotOptions) (localhistory.Manifest, bool, error)
	Stats() (localhistory.Stats, error)
	Timeline(limit int) ([]localhistory.SnapshotRecord, error)
	VerifyAll(ctx context.Context) error
}

// LocalHistoryService plans agent- and message-scoped restores.
type LocalHistoryService interface {
	AgentUndoableScopes(sessionID, agentID string) ([]localhistory.MessageScope, error)
	PlanAgentRestore(ctx context.Context, sessionID, messageID, agentID string) (localhistory.RestorePlan, error)
	PlanMessageRestore(ctx context.Context, sessionID, messageID string) (localhistory.RestorePlan, error)
}
