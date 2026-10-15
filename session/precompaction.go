package session

import (
	"context"

	"crude/agent"
	"crude/sessiondb"
)

// precompactionSlot returns a root agent's background checkpoint slot. It
// outlives runs, so ending a short human turn never discards a checkpoint that
// is already being prepared and paid for; the next run adopts it. Background
// spend is recorded and progress reaches the shell whether or not a run is
// active. The slot closes with its orchestrator or the session.
func (s *Session) precompactionSlot(agentID string) *agent.PrecompactionSlot {
	s.precompactionMu.Lock()
	defer s.precompactionMu.Unlock()
	if slot := s.precompaction[agentID]; slot != nil {
		return slot
	}
	if s.precompaction == nil {
		s.precompaction = make(map[string]*agent.PrecompactionSlot)
		s.precompactionCtx, s.precompactionCancel = context.WithCancel(context.Background())
	}
	slot := agent.NewPrecompactionSlot(s.precompactionCtx, func(event agent.AgentEvent) {
		if event.AgentID == "" {
			event.AgentID = agentID
		}
		if event.Type == "usage" {
			s.processUsageEvent(event.Data, "", agentID, "")
		}
		sendRuntimeEvent(s.orchestratorSender(agentID), event)
	})
	s.precompaction[agentID] = slot
	return slot
}

// closePrecompaction stops one agent's background preparation, or every
// agent's when agentID is empty, and waits for it.
func (s *Session) closePrecompaction(agentID string) {
	s.precompactionMu.Lock()
	var slots []*agent.PrecompactionSlot
	for id, slot := range s.precompaction {
		if agentID == "" || id == agentID {
			slots = append(slots, slot)
			delete(s.precompaction, id)
		}
	}
	if agentID == "" && s.precompactionCancel != nil {
		s.precompactionCancel()
	}
	s.precompactionMu.Unlock()
	for _, slot := range slots {
		slot.Close()
	}
}

// lastCheckpointTokens is the size after this agent's most recent completed
// compaction, the starting estimate for the next checkpoint.
func lastCheckpointTokens(records []sessiondb.CompactionRecord) int {
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].State == sessiondb.CompactionCompleted && records[index].AfterTokens > 0 {
			return records[index].AfterTokens
		}
	}
	return 0
}
