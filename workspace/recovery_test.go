package workspace

import (
	"reflect"
	"testing"
	"time"

	"crude/types"
	"crude/types/recovery"
)

func TestRecoveryTransitionsRetainOneCanonicalRowAndUnfinishedOwnership(t *testing.T) {
	conversation := NewConversation()
	state := NewAgentState("owner", conversation, nil)
	handleEvent := func(event AgentEvent) {
		state.HandleEvent(event, nil,
			func(string) {}, func(bool, bool, bool) {}, func() {},
			func(string, string, string, string, string, string, string) {}, func(string) {}, func() {})
	}
	handleEvent(AgentEvent{Type: "stream_token", Data: "accepted output"})
	status := recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 1, State: recovery.Recovering, Attempt: 1,
		StartedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(1001, 0), Summary: "Recovering connection", Detail: "HTTP 503",
	}
	handle := func(status recovery.Status) {
		handleEvent(AgentEvent{Type: "recovery", Data: status})
	}
	handle(status)
	status.State, status.Attempt, status.Summary = recovery.NeedsAttention, 2, "Check account"
	status.UpdatedAt = time.Unix(1002, 0)
	handle(status)
	if !state.IsActive() || state.Completed {
		t.Fatal("attention was treated as task death/completion")
	}
	messages := conversation.Messages()
	var incidents, accepted int
	for _, message := range messages {
		if message.Recovery != nil {
			incidents++
			if !reflect.DeepEqual(message, RecoveryMessage(status)) {
				t.Fatal("live projection differs from durable incident projection")
			}
		}
		if message.Content == "accepted output" {
			accepted++
		}
	}
	if incidents != 1 || accepted != 1 {
		t.Fatalf("recovery lost/spammed accepted content: %+v", messages)
	}
	conversation.ClearTurnStatus()
	if len(conversation.Messages()) != len(messages) {
		t.Fatal("ordinary phase cleanup consumed a durable recovery row")
	}
}

func TestStaleRecoveryCannotReplaceANewerTurn(t *testing.T) {
	state := NewAgentState("owner", NewConversation(), nil)
	state.TurnIdentity = types.TurnIdentity{Generation: 3, ModelID: "model"}
	state.Running = true
	state.HandleEvent(AgentEvent{Type: "recovery", Data: recovery.Status{ID: "old", AgentID: "owner", Generation: 2, State: recovery.NeedsAttention}}, nil,
		func(string) {}, func(bool, bool, bool) {}, func() {},
		func(string, string, string, string, string, string, string) {}, func(string) {}, func() {})
	if state.Recovery.ID != "" || len(state.Messages.Messages()) != 0 || !state.IsActive() {
		t.Fatal("stale recovery changed a replacement turn")
	}
}
