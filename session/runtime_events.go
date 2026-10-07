package session

import (
	"sync/atomic"

	"crude/agent"
	ttypes "crude/types"
)

type runtimeEventSource struct {
	ownerID, agentID, runID  string
	runtimeEpoch, generation uint64
	sequence                 atomic.Uint64
}

func (s *runtimeEventSource) bind(event agent.AgentEvent) agent.AgentEvent {
	if event.AgentID == "" {
		event.AgentID = s.agentID
	}
	if event.Identity.EventID == "" {
		event.Identity = ttypes.RuntimeEventIdentity{
			EventID: newMessageID(), OwnerID: s.ownerID, AgentID: s.agentID, RunID: s.runID,
			RuntimeEpoch: s.runtimeEpoch, Generation: s.generation, Sequence: s.sequence.Add(1),
		}
	}
	return event
}

func sendRuntimeEvent(sender EventSender, event agent.AgentEvent) {
	if sender == nil {
		return
	}
	if exact, ok := sender.(IdentityEventSender); ok && event.Identity.Valid() {
		exact.SendAgentEventEnvelope(event)
		return
	}
	sender.SendAgentEventWithID(event.Type, event.Data, event.AgentID)
}
