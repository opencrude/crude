package viewmodel

import (
	"strings"
	"testing"

	"crude/types"
	"crude/workspace"
)

func TestResponsesContinuationFailureIsVisibleAndDeduplicated(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": {SessionID: "session", AgentID: "root"}}
	event := workspace.AgentEvent{Type: "response_continuation_unavailable", AgentID: "root", Data: map[string]any{"reason": "response_not_stored"}}
	event.Identity = types.RuntimeEventIdentity{EventID: "response-policy", OwnerID: "session", AgentID: "root", RuntimeEpoch: 1, Generation: 1, Sequence: 1}
	notice, ok := workspace.NoticeFromEvent(event)
	if !ok {
		t.Fatal("continuation event did not produce a service notice")
	}
	wsOf(app).ApplyAgentNotice(notice)
	version := wsOf(app).Status.Version()
	for range 200 {
		app.handleAgentDomainEvent(event)
	}
	if wsOf(app).Status.Version() != version {
		t.Fatal("shell replayed canonical continuation feedback")
	}
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
