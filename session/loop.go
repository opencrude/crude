package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

const (
	loopStateKey    = "scheduled_loops"
	loopInputPrefix = "scheduled-loop:"
	loopLimit       = 10
	loopTaskLimit   = 4000
	loopLifetime    = 7 * 24 * time.Hour
)

// Loops returns detached state. Times are core-owned; presentation never
// advances a schedule or creates work.
func (s *Session) Loops() []types.Loop {
	if s == nil {
		return nil
	}
	s.loopMu.Lock()
	defer s.loopMu.Unlock()
	return slices.Clone(s.loops)
}

func (s *Session) saveLoopsLocked(next []types.Loop) error {
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := sessiondb.SaveRuntimeState(context.Background(), s.db, loopStateKey, raw); err != nil {
		return err
	}
	s.loops = next
	return nil
}

// loadLoops restores schedules passively. A new runtime never replays a
// missed firing or starts spending without an explicit resume.
func (s *Session) loadLoops() error {
	raw, err := sessiondb.LoadRuntimeState(context.Background(), s.db, loopStateKey)
	if err != nil || len(raw) == 0 {
		return err
	}
	var loops []types.Loop
	if err := json.Unmarshal(raw, &loops); err != nil {
		return fmt.Errorf("restore scheduled loops: %w", err)
	}
	seen := make(map[string]bool, len(loops))
	if len(loops) > loopLimit {
		return errors.New("restore scheduled loops: too many loops")
	}
	for i := range loops {
		loop := &loops[i]
		if loop.ID == "" || seen[loop.ID] || strings.TrimSpace(loop.Prompt) == "" ||
			loop.Interval < time.Minute || loop.Interval > loopLifetime || loop.ExpiresAt.IsZero() {
			return errors.New("restore scheduled loops: invalid schedule")
		}
		seen[loop.ID] = true
		if !loop.Paused {
			loop.Paused = true
			loop.Reason = "The session reopened; resume explicitly. Missed runs are not replayed."
		}
	}
	s.loopMu.Lock()
	err = s.saveLoopsLocked(loops)
	s.loopMu.Unlock()
	return err
}

func (s *Session) publishLoops(row string) {
	if row != "" {
		s.recordStatusRow("loop", row)
	}
	if o := s.liveOrchestrator(); o != nil && o.sender != nil {
		o.sender.SendAgentEventWithID("loops", s.Loops(), o.ID)
	}
	select {
	case s.goalWake <- struct{}{}:
	default:
	}
}

// CreateLoop schedules an explicit user prompt immediately and then at the
// requested fixed interval. A bounded lifetime prevents forgotten background
// work from spending indefinitely.
func (s *Session) CreateLoop(interval time.Duration, task string) (types.Loop, error) {
	task = strings.TrimSpace(task)
	if interval < time.Minute || interval > loopLifetime {
		return types.Loop{}, errors.New("loop interval must be between 1 minute and 7 days")
	}
	if task == "" || utf8.RuneCountInString(task) > loopTaskLimit {
		return types.Loop{}, errors.New("a loop needs a prompt of at most 4000 characters")
	}
	release, ok := s.beginIdleWork()
	if !ok {
		return types.Loop{}, errors.New("the session is closing or being retired")
	}
	defer release()
	s.loopOpMu.Lock()
	defer s.loopOpMu.Unlock()
	s.loopMu.Lock()
	if atomic.LoadInt32(&s.closing) != 0 || len(s.loops) >= loopLimit {
		s.loopMu.Unlock()
		return types.Loop{}, errors.New("the session is closing or already has 10 loops")
	}
	var id string
	for {
		bytes := make([]byte, 4)
		if _, err := rand.Read(bytes); err != nil {
			s.loopMu.Unlock()
			return types.Loop{}, err
		}
		id = hex.EncodeToString(bytes)
		if !slices.ContainsFunc(s.loops, func(loop types.Loop) bool { return loop.ID == id }) {
			break
		}
	}
	now := time.Now()
	loop := types.Loop{ID: id, Prompt: task, Interval: interval, NextAt: now, ExpiresAt: now.Add(loopLifetime)}
	next := append(slices.Clone(s.loops), loop)
	err := s.saveLoopsLocked(next)
	s.loopMu.Unlock()
	if err != nil {
		return types.Loop{}, err
	}
	s.publishLoops(fmt.Sprintf("Loop %s scheduled every %s: %s", loop.ID, loop.Interval, loop.Prompt))
	return loop, nil
}

// ChangeLoop acts on an exact identity. Pausing or clearing prevents future
// iterations but does not cancel a turn already admitted.
func (s *Session) ChangeLoop(action, id string) error {
	release, ok := s.beginIdleWork()
	if !ok {
		return errors.New("the session is closing or being retired")
	}
	defer release()
	s.loopOpMu.Lock()
	defer s.loopOpMu.Unlock()
	s.loopMu.Lock()
	next := slices.Clone(s.loops)
	index := slices.IndexFunc(next, func(loop types.Loop) bool { return loop.ID == id })
	if index < 0 {
		s.loopMu.Unlock()
		return errors.New("loop not found; use /loop to inspect its exact ID")
	}
	loop := &next[index]
	var row string
	switch action {
	case "pause":
		loop.Paused, loop.Reason = true, "Paused by the user."
		row = "Loop " + id + " paused; an iteration already running is not stopped."
	case "resume":
		if !loop.Paused {
			s.loopMu.Unlock()
			return errors.New("loop is already active")
		}
		if !time.Now().Before(loop.ExpiresAt) {
			s.loopMu.Unlock()
			return errors.New("loop expired; create a new loop to schedule it again")
		}
		loop.Paused, loop.Reason, loop.NextAt = false, "", time.Now().Add(loop.Interval)
		row = "Loop " + id + " resumed."
	case "clear":
		next = slices.Delete(next, index, index+1)
		row = "Loop " + id + " cleared; an iteration already running is not stopped."
	default:
		s.loopMu.Unlock()
		return errors.New("unknown loop action")
	}
	err := s.saveLoopsLocked(next)
	if err != nil && (action == "pause" || action == "clear") {
		// A storage failure must not turn an attempted Stop into more
		// autonomous spending. Keep the exact schedule visible and paused in
		// this runtime; a cold reopen also restores it paused.
		fallback := slices.Clone(s.loops)
		fallback[index].Paused = true
		fallback[index].Reason = "The stop could not be saved; this loop is paused in this runtime. Retry /loop clear or pause after storage is repaired."
		s.loops = fallback
	}
	s.loopMu.Unlock()
	if err != nil {
		if action == "pause" || action == "clear" {
			s.publishLoops("Loop " + id + " paused in this runtime; its stop could not be saved.")
			return fmt.Errorf("loop paused in this runtime, but the stop could not be saved: %w", err)
		}
		return err
	}
	s.publishLoops(row)
	return nil
}

// startLoopTurn reserves the normal root turn before durably advancing the
// schedule. If the process dies at that boundary, the occurrence may be
// missed but is never replayed as a second autonomous turn.
func (s *Session) startLoopTurn(o *Orchestrator, now time.Time) bool {
	release, ok := s.beginIdleWork()
	if !ok {
		return false
	}
	defer release()
	if o.RuntimeEpoch == 0 || o.TurnActive() || o.closing.Load() || atomic.LoadInt32(&s.closing) != 0 {
		return false
	}
	s.loopOpMu.Lock()
	defer s.loopOpMu.Unlock()
	s.loopMu.Lock()
	next := slices.Clone(s.loops)
	if len(next) == 0 {
		s.loopMu.Unlock()
		return false
	}
	for i := range next {
		if !next[i].Paused && !now.Before(next[i].ExpiresAt) {
			next[i].Paused, next[i].Reason = true, "Expired after seven days; create a new loop to continue."
			if err := s.saveLoopsLocked(next); err != nil {
				next[i].Reason += " The expiry could not be saved; it remains paused in this runtime."
				s.loops = next
				s.loopMu.Unlock()
				if s.logger != nil {
					s.logger.Warn("LOOP", "expire_save_failed", "error", err.Error())
				}
				s.publishLoops("Loop " + next[i].ID + " expired; its pause could not be saved.")
				return false
			}
			id := next[i].ID
			s.loopMu.Unlock()
			s.publishLoops("Loop " + id + " expired after seven days.")
			return false
		}
	}
	if s.PlanMode() {
		s.loopMu.Unlock()
		return false
	}
	if goal, present := s.Goal(); present && goal.Status == types.GoalActive {
		s.loopMu.Unlock()
		return false
	}
	s.queueMu.Lock()
	queued := len(s.queued) != 0
	s.queueMu.Unlock()
	if queued {
		s.loopMu.Unlock()
		return false
	}
	// Long iterations may make several schedules due together. Oldest due
	// first prevents an earlier, frequently overdue entry starving its peers.
	index := -1
	for i := range next {
		if !next[i].Paused && !now.Before(next[i].NextAt) &&
			(index < 0 || next[i].NextAt.Before(next[index].NextAt)) {
			index = i
		}
	}
	if index < 0 {
		s.loopMu.Unlock()
		return false
	}
	loop := next[index]
	input := provider.UserInput{
		InternalID: fmt.Sprintf("%s%s:%d", loopInputPrefix, loop.ID, now.UnixNano()),
		Text:       rootprompt.LoopTurn(loop.Prompt),
		Origin:     provider.OriginRuntimeContext,
	}
	input = s.bindInputExecution(input)
	request, reserved := o.reserveTurn(input, nil, nil)
	if !reserved {
		s.loopMu.Unlock()
		return false
	}
	next[index].NextAt = now.Add(loop.Interval)
	if err := s.saveLoopsLocked(next); err != nil {
		s.loopMu.Unlock()
		o.endTurn(request.generation)
		s.logger.Warn("LOOP", "advance_save_failed", "error", err.Error())
		return false
	}
	enqueued := o.enqueueReserved(request)
	if !enqueued {
		o.endTurn(request.generation)
	}
	s.loopMu.Unlock()
	if !enqueued {
		s.publishLoops("Loop " + loop.ID + " missed an iteration during admission; it was not replayed.")
		return false
	}
	s.publishLoops("Loop " + loop.ID + " running.")
	return true
}

func (s *Session) loopTurnEnd(input provider.UserInput, stopped, failed, parked bool, failure string) {
	if !strings.HasPrefix(input.InternalID, loopInputPrefix) || parked || !stopped && !failed {
		return
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(input.InternalID, loopInputPrefix), ":")
	s.loopOpMu.Lock()
	defer s.loopOpMu.Unlock()
	s.loopMu.Lock()
	next := slices.Clone(s.loops)
	index := slices.IndexFunc(next, func(loop types.Loop) bool { return loop.ID == id })
	if index < 0 || next[index].Paused {
		s.loopMu.Unlock()
		return
	}
	reason := "The loop iteration was stopped."
	if failed {
		reason = "The loop iteration failed."
		if failure != "" {
			reason += " " + failure
		}
	}
	next[index].Paused, next[index].Reason = true, reason
	err := s.saveLoopsLocked(next)
	if err != nil {
		next[index].Reason += " The pause could not be saved; it remains paused in this runtime."
		s.loops = next
	}
	s.loopMu.Unlock()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("LOOP", "pause_save_failed", "error", err.Error())
		}
		s.publishLoops("Loop " + id + " paused in this runtime; its pause could not be saved.")
		return
	}
	s.publishLoops("Loop " + id + " paused. " + reason)
}
