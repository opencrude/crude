package shellport

import (
	"context"
	"time"

	"crude/internal/metrics"
	"crude/types/statuscenter"
)

// StatusHost exposes the workspace status center.
type StatusHost interface {
	// Status retains operations, issues and notifications; nil when unavailable.
	Status() StatusService
}

// StatusService retains workspace-scoped operations, issues and notification history.
type StatusService interface {
	Agent(id string) (statuscenter.AgentStatusSnapshot, bool)
	Agents() []statuscenter.AgentStatusSnapshot
	Dismiss(id string) bool
	FinishOperation(id string, state statuscenter.OperationState, errText string)
	Hydrate() error
	Issues() []statuscenter.IssueSnapshot
	LatestTransient(now time.Time, maxAge time.Duration) (statuscenter.NotificationRecord, bool)
	MarkRead(id string, read bool) bool
	Notifications(includeDismissed bool) []statuscenter.NotificationRecord
	Notify(n statuscenter.NotificationRecord) string
	ResolveIssue(id string)
	SetIssue(issue statuscenter.IssueSnapshot)
	SetOperation(op statuscenter.OperationSnapshot)
	UnreadCount() int
	Version() uint64
}

// MetricsSource is implemented by shells attached to a separate core
// process; it reads that process's performance history. An in-process core
// records into the shell's own registry instead.
type MetricsSource interface {
	CoreMetrics(ctx context.Context, query metrics.Query) (metrics.Snapshot, error)
}
