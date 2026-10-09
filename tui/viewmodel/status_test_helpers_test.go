package viewmodel

import (
	"time"

	types "crude/types"
)

func testActiveAgentCount(app *App) int {
	if app == nil || app.Workspace == nil || app.Status == nil {
		return 0
	}
	count := 0
	for _, status := range app.Status.Agents() {
		switch status.Phase {
		case types.AgentOpening, types.AgentPreparing, types.AgentUploading, types.AgentWaiting, types.AgentThinking, types.AgentResponding, types.AgentTool, types.AgentChildren, types.AgentRetrying, types.AgentCancelling:
			count++
		}
	}
	return count
}

func testIdleAgentCount(app *App) int {
	if app == nil || app.Workspace == nil || app.Status == nil {
		return 0
	}
	count := 0
	for _, status := range app.Status.Agents() {
		if status.Phase == types.AgentIdle && !status.ReadOnly {
			count++
		}
	}
	return count
}

func testStatusText(app *App) string {
	if app == nil || app.Workspace == nil || app.Status == nil {
		return ""
	}
	for _, issue := range app.Status.Issues() {
		if issue.ResolvedAt.IsZero() {
			return issue.Summary
		}
	}
	if notice, ok := app.Status.LatestUnread(types.SeverityInfo); ok {
		return notice.Summary
	}
	if notice, ok := app.Status.LatestTransient(time.Now(), 10*time.Second); ok {
		return notice.Summary
	}
	return ""
}
