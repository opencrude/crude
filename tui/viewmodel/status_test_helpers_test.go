package viewmodel

import (
	"time"

	types "crude/types"
)

func testActiveAgentCount(app *App) int {
	if app == nil || app.Workspace == nil || app.Workspace.Status == nil {
		return 0
	}
	count := 0
	for _, status := range app.Workspace.Status.Agents() {
		switch status.Phase {
		case types.AgentOpening, types.AgentPreparing, types.AgentUploading, types.AgentWaiting, types.AgentThinking, types.AgentResponding, types.AgentTool, types.AgentChildren, types.AgentRetrying, types.AgentCancelling:
			count++
		}
	}
	return count
}

func testIdleAgentCount(app *App) int {
	if app == nil || app.Workspace == nil || app.Workspace.Status == nil {
		return 0
	}
	count := 0
	for _, status := range app.Workspace.Status.Agents() {
		if status.Phase == types.AgentIdle && !status.ReadOnly {
			count++
		}
	}
	return count
}

func testStatusText(app *App) string {
	if app == nil || app.Workspace == nil || app.Workspace.Status == nil {
		return ""
	}
	for _, issue := range app.Workspace.Status.Issues() {
		if issue.ResolvedAt.IsZero() {
			return issue.Summary
		}
	}
	if notice, ok := app.Workspace.Status.LatestUnread(types.SeverityInfo); ok {
		return notice.Summary
	}
	if notice, ok := app.Workspace.Status.LatestTransient(time.Now(), 10*time.Second); ok {
		return notice.Summary
	}
	return ""
}

func testBtwCount(app *App) int {
	if app == nil {
		return 0
	}
	if node := app.AgentMgr.Get(app.AgentMgr.ActiveID()); node != nil && node.State != nil && node.State.Messages != nil {
		return node.State.Messages.PendingBtwCount()
	}
	return 0
}
