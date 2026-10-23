package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"crude/sessiondb"
)

// Fork copies this session's conversation into a new saved session id under
// dir; this session is left as it is. The
// copy is taken while no turn can start, and only when nothing is mid-flight
// whose ownership a copy would duplicate: a turn, a compaction, an unfinished
// run's recovery or a running child agent.
func (s *Session) Fork(dir, id string) error {
	if s == nil || s.db == nil {
		return errors.New("this session has no saved history to fork")
	}
	name, err := allocateAgentName(s.nameHome)
	if err != nil {
		return fmt.Errorf("allocate agent name: %w", err)
	}
	// Lock order matches turn admission: runner, then turn, then recovery and
	// compaction state.
	runner := s.SubAgentRunner()
	s.orchMu.Lock()
	orchestrators := make([]*Orchestrator, 0, len(s.orchestrators))
	for _, orchestrator := range s.orchestrators {
		orchestrators = append(orchestrators, orchestrator)
	}
	s.orchMu.Unlock()
	for _, orchestrator := range orchestrators {
		orchestrator.turnMu.Lock()
		defer orchestrator.turnMu.Unlock()
		if orchestrator.turnCancel != nil {
			return errors.New("stop the running turn before forking")
		}
	}
	if s.compactionActive("") {
		return errors.New("Wait for context compaction to finish before forking")
	}
	s.recoveryMu.Lock()
	unfinished := len(s.pendingRecoveries) > 0
	s.recoveryMu.Unlock()
	if unfinished {
		return errors.New("continue or stop the unfinished run before forking")
	}
	if runner != nil && len(runner.Manager().ActiveHandles()) > 0 {
		return errors.New("Wait for this session's agents to finish, or stop them, before forking")
	}
	return sessiondb.Fork(s.db, filepath.Join(dir, id+".db"), sessiondb.ForkIdentity{
		SessionID: id, AgentName: name, CreatedAt: time.Now().UnixNano(),
	})
}
