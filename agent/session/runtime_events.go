package session

import (
	"sync/atomic"

	"crude/agent/turnloop"
	"crude/types/conversation"
)

type runtimeEventSource struct {
	ownerID, agentID, runID  string
	runtimeEpoch, generation uint64
	sequence                 atomic.Uint64
}

func (s *runtimeEventSource) bind(event turnloop.AgentEvent) turnloop.AgentEvent {
	if event.AgentID == "" {
		event.AgentID = s.agentID
	}
	if event.Identity.EventID == "" {
		event.Identity = conversation.RuntimeEventIdentity{
			EventID: newMessageID(), OwnerID: s.ownerID, AgentID: s.agentID, RunID: s.runID,
			RuntimeEpoch: s.runtimeEpoch, Generation: s.generation, Sequence: s.sequence.Add(1),
		}
	}
	return event
}

func sendRuntimeEvent(sender EventSender, event turnloop.AgentEvent) {
	if sender == nil {
		return
	}
	sender.SendAgentEventEnvelope(event)
}
