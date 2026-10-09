package viewmodel

import (
	"strings"
	"testing"

	"crude/workspace"
)

func TestResponsesContinuationFailureIsVisibleAndDeduplicated(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	event := workspace.AgentEvent{Type: "response_continuation_unavailable", AgentID: "root", Data: map[string]any{"reason": "response_not_stored"}}
	app.handleAgentDomainEvent(event)
	app.handleAgentDomainEvent(event)
	notifications := app.ws.Status().Notifications(true)
	matched := 0
	for _, notification := range notifications {
		if strings.Contains(notification.Summary, "endpoint declined server storage") {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("continuation limitation notifications=%d, want one: %+v", matched, notifications)
	}
}
