package viewmodel

import (
	"time"

	types "crude/types"
)

func testActiveAgentCount(app *App) int {
	if app == nil || wsOf(app) == nil || app.ws.Status() == nil {
		return 0
	}
	count := 0
	for _, status := range app.ws.Status().Agents() {
		switch status.Phase {
		case types.AgentOpening, types.AgentPreparing, types.AgentUploading, types.AgentWaiting, types.AgentThinking, types.AgentResponding, types.AgentTool, types.AgentChildren, types.AgentRetrying, types.AgentCancelling:
			count++
		}
	}
	return count
}

func testIdleAgentCount(app *App) int {
	if app == nil || wsOf(app) == nil || app.ws.Status() == nil {
		return 0
	}
	count := 0
	for _, status := range app.ws.Status().Agents() {
		if status.Phase == types.AgentIdle && !status.ReadOnly {
			count++
		}
	}
	return count
}

func testStatusText(app *App) string {
	if app == nil || wsOf(app) == nil || app.ws.Status() == nil {
		return ""
	}
	for _, issue := range app.ws.Status().Issues() {
		if issue.ResolvedAt.IsZero() {
			return issue.Summary
		}
	}
	if notice, ok := wsOf(app).Status.LatestUnread(types.SeverityInfo); ok {
		return notice.Summary
	}
	if notice, ok := app.ws.Status().LatestTransient(time.Now(), 10*time.Second); ok {
		return notice.Summary
	}
	return ""
}
