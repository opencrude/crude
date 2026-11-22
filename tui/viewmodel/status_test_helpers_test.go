package viewmodel

import (
	"time"

	"crude/types/statuscenter"
)

func testActiveAgentCount(app *App) int {
	if app == nil || wsOf(app) == nil || app.ws.Status() == nil {
		return 0
	}
	count := 0
	for _, status := range app.ws.Status().Agents() {
		switch status.Phase {
		case statuscenter.AgentOpening, statuscenter.AgentPreparing, statuscenter.AgentUploading, statuscenter.AgentWaiting, statuscenter.AgentThinking, statuscenter.AgentResponding, statuscenter.AgentTool, statuscenter.AgentChildren, statuscenter.AgentRetrying, statuscenter.AgentCancelling:
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
		if status.Phase == statuscenter.AgentIdle && !status.ReadOnly {
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
	if notice, ok := wsOf(app).Status.LatestUnread(statuscenter.SeverityInfo); ok {
		return notice.Summary
	}
	if notice, ok := app.ws.Status().LatestTransient(time.Now(), 10*time.Second); ok {
		return notice.Summary
	}
	return ""
}
