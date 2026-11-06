package workspace

import (
	"testing"

	"crude/types/reasoning"
)

// Some providers (observed with inclusionai/ling-3.1 over the Responses API)
// keep sending "\n" deltas for the answer item after its tool calls started.
// Whitespace continues an open message but never opens one: a message made of
// it is a blank row that splits the calls around it out of their group.
func TestWhitespaceStreamTextNeverOpensMessage(t *testing.T) {
	state := NewAgentState("agent", NewConversation(), nil)
	apply := func(typ string, data any) { state.ApplyEvent(AgentEvent{Type: typ, Data: data}) }
	apply("thinking", reasoning.Delta{Text: "Plan the reads.", Kind: reasoning.Raw})
	apply("stream_token", "Let me check")
	apply("stream_token", "\n\n")
	apply("stream_token", "the rules.\n")
	for _, id := range []string{"a", "b", "c"} {
		apply("tool_call_streaming", map[string]any{"tool_id": id, "internal_id": id, "name": "read_file", "path": id + ".go"})
		apply("stream_token", "\n")
		apply("thinking", reasoning.Delta{Text: "\n\n", Kind: reasoning.Raw})
	}
	var roles []string
	for _, message := range state.Messages.Messages() {
		roles = append(roles, message.Role)
	}
	want := []string{MessageRoleThinking, MessageRoleAssistant, MessageRoleTool, MessageRoleTool, MessageRoleTool}
	if len(roles) != len(want) {
		t.Fatalf("messages %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("messages %v, want %v", roles, want)
		}
	}
	if got := state.Messages.Messages()[1].Content; got != "Let me check\n\nthe rules.\n" {
		t.Fatalf("whitespace inside an open answer was lost: %q", got)
	}

	// Whitespace still continues a live thought.
	thought := NewAgentState("thought", NewConversation(), nil)
	thought.ApplyEvent(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: "First.", Kind: reasoning.Raw}})
	thought.ApplyEvent(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: "\n\n", Kind: reasoning.Raw}})
	thought.ApplyEvent(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: "Second.", Kind: reasoning.Raw}})
	if messages := thought.Messages.Messages(); len(messages) != 1 || messages[0].Content != "First.\n\nSecond." {
		t.Fatalf("thought lost its paragraph break: %+v", messages)
	}
}
