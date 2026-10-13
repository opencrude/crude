package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// historyAdmissions supplements the session admission owner without extending
// its persisted state. A restore is never resumed after process shutdown.
var historyAdmissions sync.Map // *Session -> struct{}

// ValidateHistoryAdmission is a nonblocking admission check. Reservation paths
// call it while holding their existing admission/turn lock.
func (s *Session) ValidateHistoryAdmission() error {
	if s == nil {
		return errors.New("session is unavailable")
	}
	if _, restoring := historyAdmissions.Load(s); restoring {
		return errors.New("workspace history restore is in progress; input was not consumed")
	}
	return nil
}

// BeginHistoryRestoreLease excludes fresh input until mutation AND boundary
// publication finish. Active/reserved work refuses immediately: a tool cannot
// wait on its own turn. Pending admission intents are left untouched.
func (s *Session) BeginHistoryRestoreLease(ctx context.Context) (func(), error) {
	if s == nil {
		return nil, errors.New("session is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if atomic.LoadInt32(&s.closing) != 0 || len(s.pendingAdmissions) != 0 || s.HasActiveTurns() {
		return nil, errors.New("stop active or pending agent turns before restoring history")
	}
	if _, loaded := historyAdmissions.LoadOrStore(s, struct{}{}); loaded {
		return nil, errors.New("history restore already in progress")
	}
	// Publish exclusion before checking turn locks: reservations which already
	// passed validation complete first; newer reservations observe the exclusion.
	s.orchMu.Lock()
	busy := false
	for _, o := range s.orchestrators {
		if o != nil && o.TurnActive() {
			busy = true
			break
		}
	}
	s.orchMu.Unlock()
	if busy || s.HasActiveTurns() {
		historyAdmissions.Delete(s)
		return nil, errors.New("stop reserved agent turns before restoring history")
	}
	var once sync.Once
	return func() { once.Do(func() { historyAdmissions.Delete(s) }) }, nil
}
