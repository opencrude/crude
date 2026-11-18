package session

import (
	"fmt"
	"time"

	ttypes "crude/types"
)

// maxWait is the configured bound on one agent wait (wait, agent_wait). An
// awaited event that never happens, or a stopped child, must not hold a turn
// open indefinitely: the wait ends at the bound and the agent decides again.
func (s *Session) maxWait() time.Duration {
	minutes := 0
	if s != nil {
		s.mu.Lock()
		if s.cfg != nil {
			minutes = s.cfg.Agents.MaxWaitMinutes
		}
		s.mu.Unlock()
	}
	if minutes <= 0 {
		minutes = ttypes.DefaultMaxWaitMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// boundWait clamps a requested wait and reports whether the bound applied.
func (s *Session) boundWait(requested time.Duration) (time.Duration, bool) {
	if bound := s.maxWait(); requested > bound {
		return bound, true
	}
	return requested, false
}

func waitBoundNote(bound time.Duration) string {
	return fmt.Sprintf("The wait stopped at the configured longest wait (%d minutes); wait again if still needed.", int(bound/time.Minute))
}
