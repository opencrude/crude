package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// idleAdmission covers autonomous operations which can run without a turn.
// It never waits for work: an operation either owns a reservation, or a stop
// owns exclusion. Reservations do not hold the mutex while calling owners.
type idleAdmission struct {
	mu     sync.Mutex
	fenced bool
	active int
}

func (s *Session) beginIdleWork() (func(), bool) {
	s.idle.mu.Lock()
	defer s.idle.mu.Unlock()
	if s.idle.fenced || atomic.LoadInt32(&s.closing) != 0 {
		return nil, false
	}
	s.idle.active++
	var once sync.Once
	return func() { once.Do(func() { s.idle.mu.Lock(); s.idle.active--; s.idle.mu.Unlock() }) }, true
}

// BeginIdleLease reserves existing admission owners without cancelling work or
// changing persisted state. The workspace actor must exclude session creation.
// Lock order: autonomous exclusion, history admission, turn gate, memory owner.
// No caller holds the runtime registry mutex while acquiring these leases.
func (s *Session) BeginIdleLease(ctx context.Context) (func(), error) {
	s.idle.mu.Lock()
	if s.idle.fenced || s.idle.active != 0 {
		s.idle.mu.Unlock()
		return nil, errors.New("autonomous collaboration, control or child admission is in progress")
	}
	s.idle.fenced = true
	s.idle.mu.Unlock()
	var releases []func()
	releases = append(releases, func() { s.idle.mu.Lock(); s.idle.fenced = false; s.idle.mu.Unlock() })
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	failed := func(err error) (func(), error) { release(); return nil, err }
	if activity := s.Activity(); activity.Any() {
		return failed(fmt.Errorf("active work: %d turns, %d children, %d commands, %d compactions, %d memory jobs", activity.Turns, activity.Children, activity.AsyncCommands, activity.Compaction, activity.MemoryJobs))
	}
	if !s.goalAllowsIdleHandover() {
		return failed(errors.New("active goal can continue autonomously; pause or finish the goal"))
	}
	for _, loop := range s.Loops() {
		if !loop.Paused {
			return failed(errors.New("an active loop can start work; pause or clear it"))
		}
	}
	history, err := s.BeginHistoryRestoreLease(ctx)
	if err != nil {
		return failed(err)
	}
	releases = append(releases, history)
	if !s.turnGate.TryLock() {
		return failed(errors.New("turn or compaction is still finishing"))
	}
	releases = append(releases, s.turnGate.Unlock)
	// Recheck the exact unexpired permit under final turn/history exclusion.
	// A sampled Ready is not permission to abandon a subsequently expired goal.
	if !s.goalAllowsIdleHandover() {
		return failed(errors.New("active goal no longer owns an unexpired ready handover"))
	}
	if len(s.QueuedInputs()) != 0 {
		return failed(errors.New("queued input awaits admission"))
	}
	s.memoryJobs.mu.Lock()
	if s.memoryJobs.active != 0 || s.memoryJobs.fenced {
		s.memoryJobs.mu.Unlock()
		return failed(errors.New("background memory jobs are still running"))
	}
	s.memoryJobs.fenced = true
	s.memoryJobs.mu.Unlock()
	releases = append(releases, func() { s.memoryJobs.mu.Lock(); s.memoryJobs.fenced = false; s.memoryJobs.mu.Unlock() })
	if activity := s.Activity(); activity.Any() {
		return failed(fmt.Errorf("active work: %d turns, %d children, %d commands, %d compactions, %d memory jobs", activity.Turns, activity.Children, activity.AsyncCommands, activity.Compaction, activity.MemoryJobs))
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}
