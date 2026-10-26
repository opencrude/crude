package workspace

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/types"
	"crude/types/recovery"
)

func TestRecoveredCompletionAcknowledgesAcceptedResponse(t *testing.T) {
	for _, owner := range []string{"root", "child"} {
		t.Run(owner, func(t *testing.T) {
			w := newIngressWorkspace(t)
			if owner == "child" {
				w.ApplyAgentEvent(AgentEvent{AgentID: owner, Type: "sub_agent_started", Data: map[string]string{
					"agent_id": owner, "parent_id": "root", "agent_name": "Child",
				}})
			}
			sequence := uint64(0)
			emit := func(kind string, data any) {
				t.Helper()
				sequence++
				ownerID := "session"
				if owner == "child" {
					ownerID = owner
				}
				if !w.ApplyAgentEvent(ingressEnvelope(owner, ownerID, kind, sequence, data)).Accepted {
					t.Fatalf("rejected %s", kind)
				}
			}
			emit("turn_started", types.TurnIdentity{Generation: 1, ModelID: "model"})
			emit("phase", map[string]any{"name": "preparing", "request_id": "interrupted"})
			emit("stream_token", "Accepted original fragment.\n\n")
			status := recovery.Status{ID: "incident", AgentID: owner, Generation: 1, State: recovery.Recovering,
				StartedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(1001, 0)}
			emit("recovery", status)
			emit("phase", map[string]any{"name": "preparing", "request_id": "response"})
			emit("phase", map[string]any{"name": "waiting_provider", "request_id": "response"})
			const answer = "### What changed\nOriginal pixels.\n\n### Verified\nOne response."
			for _, token := range strings.SplitAfter(answer, "\n") {
				emit("stream_token", token)
			}
			state := w.AgentMgr.Get(owner).State
			accepted := state.Messages.Messages()
			answerID := accepted[len(accepted)-1].InternalID
			status.State, status.UpdatedAt = recovery.Recovered, time.Unix(1002, 0)
			emit("recovery", status)
			if state.Streaming {
				t.Fatal("fixture did not cross the recovery presentation transition")
			}
			emit("done", map[string]any{"content": answer, "reason": "completed"})
			var responses []Message
			for _, message := range state.Messages.Messages() {
				if message.Role == MessageRoleAssistant && strings.Contains(message.Content, "What changed") {
					responses = append(responses, message)
				}
			}
			if len(responses) != 1 || responses[0].Content != answer || responses[0].InternalID != answerID {
				t.Fatalf("completion repeated or replaced accepted text: %+v", responses)
			}
			if state.IsActive() || state.RecoveryStatus().State != recovery.Recovered {
				t.Fatal("completion lost terminal/recovery lifecycle")
			}
		})
	}
}

func TestResponseCompletionKeepsNonstreamedAndLaterRequestText(t *testing.T) {
	state := NewAgentState("owner", nil, nil)
	emit := func(kind string, data any) { state.ApplyEvent(AgentEvent{Type: kind, Data: data}) }
	emit("turn_started", types.TurnIdentity{Generation: 1, ModelID: "model"})
	emit("phase", map[string]any{"name": "preparing", "request_id": "first"})
	emit("stream_token", "Original response")
	// A distinct request in the same turn has no streamed assistant output. Its
	// final content must not be mistaken for the first request's acknowledgement.
	emit("phase", map[string]any{"name": "preparing", "request_id": "second"})
	emit("done", map[string]any{"content": "Done-only response", "reason": "completed"})
	if rows := state.Messages.Messages(); len(rows) != 2 || rows[0].Content != "Original response" || rows[1].Content != "Done-only response" {
		t.Fatalf("a prior request swallowed a distinct completion: %+v", rows)
	}
	// Repeating prose in a later human turn is legitimate, not a duplicate event.
	emit("turn_started", types.TurnIdentity{Generation: 2, ModelID: "model"})
	emit("done", map[string]any{"content": "Done-only response", "reason": "completed"})
	if rows := state.Messages.Messages(); len(rows) != 3 || rows[2].Content != "Done-only response" {
		t.Fatalf("text-based deduplication swallowed legitimate output: %+v", rows)
	}
}

func TestRecoveredCompletionDoesNotRepeatContinuationSuffix(t *testing.T) {
	state := NewAgentState("owner", nil, nil)
	emit := func(kind string, data any) { state.ApplyEvent(AgentEvent{Type: kind, Data: data}) }
	emit("turn_started", types.TurnIdentity{Generation: 1, ModelID: "model"})
	emit("phase", map[string]any{"name": "preparing", "request_id": "partial"})
	emit("stream_token", "Original response. ")
	emit("phase", map[string]any{"name": "preparing", "request_id": "continuation"})
	emit("stream_token", "### What changed\nAccepted continuation.")
	emit("recovery", recovery.Status{ID: "incident", AgentID: "owner", Generation: 1, State: recovery.Recovered})
	emit("done", map[string]any{"content": "### What changed\nAccepted continuation.", "reason": "completed"})
	var text strings.Builder
	for _, row := range state.Messages.Messages() {
		if row.Role == MessageRoleAssistant {
			text.WriteString(row.Content)
		}
	}
	if got := text.String(); got != "Original response. ### What changed\nAccepted continuation." {
		t.Fatalf("completion appended an already accepted suffix: %q", got)
	}
}

func TestRecoveryTransitionsRetainOneCanonicalRowAndUnfinishedOwnership(t *testing.T) {
	conversation := NewConversation()
	state := NewAgentState("owner", conversation, nil)
	handleEvent := func(event AgentEvent) {
		state.ApplyEvent(event)
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
	state.ApplyEvent(AgentEvent{Type: "recovery", Data: recovery.Status{ID: "old", AgentID: "owner", Generation: 2, State: recovery.NeedsAttention}})
	if state.Recovery.ID != "" || len(state.Messages.Messages()) != 0 || !state.IsActive() {
		t.Fatal("stale recovery changed a replacement turn")
	}
}
