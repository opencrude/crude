package session

import (
	"testing"

	"crude/agent"
)

func TestRuntimeEventSourceAssignsStableExactSequence(t *testing.T) {
	source := &runtimeEventSource{ownerID: "session", agentID: "agent", runID: "run", runtimeEpoch: 2, generation: 3}
	first := source.bind(agent.AgentEvent{Type: "stream_token"})
	second := source.bind(agent.AgentEvent{Type: "done"})
	if !first.Identity.Valid() || first.Identity.Sequence != 1 || second.Identity.Sequence != 2 || first.Identity.EventID == second.Identity.EventID {
		t.Fatalf("bad identities: first=%+v second=%+v", first.Identity, second.Identity)
	}
	duplicate := source.bind(first)
	if duplicate.Identity != first.Identity {
		t.Fatalf("duplicate identity regenerated: before=%+v after=%+v", first.Identity, duplicate.Identity)
	}
}
