package shellport

import (
	"context"
	"time"

	"crude/metrics"
	"crude/types"
)

// StatusHost exposes the workspace status center.
type StatusHost interface {
	// Status retains operations, issues and notifications; nil when unavailable.
	Status() StatusService
}

// StatusService retains workspace-scoped operations, issues and notification history.
type StatusService interface {
	Agent(id string) (types.AgentStatusSnapshot, bool)
	Agents() []types.AgentStatusSnapshot
	Dismiss(id string) bool
	FinishOperation(id string, state types.OperationState, errText string)
	Hydrate() error
	Issues() []types.IssueSnapshot
	LatestTransient(now time.Time, maxAge time.Duration) (types.NotificationRecord, bool)
	MarkRead(id string, read bool) bool
	Notifications(includeDismissed bool) []types.NotificationRecord
	Notify(n types.NotificationRecord) string
	ResolveIssue(id string)
	SetIssue(issue types.IssueSnapshot)
	SetOperation(op types.OperationSnapshot)
	UnreadCount() int
	Version() uint64
}

// MetricsSource is implemented by shells attached to a separate core
// process; it reads that process's performance history. An in-process core
// records into the shell's own registry instead.
type MetricsSource interface {
	CoreMetrics(ctx context.Context, query metrics.Query) (metrics.Snapshot, error)
}
