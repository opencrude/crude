package session

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"crude/collaboration"
	"crude/sessiondb"
	"crude/types"
	"crude/types/control"
	"crude/types/handover"
	recoverytypes "crude/types/recovery"
)

// The source is installed by the actual root turn, not by a request. It is
// cleared only by that exact run's unwind.
type goalHandoverSource struct {
	agentID, runID    string
	inputID           string
	epoch, generation uint64
	target            control.Target
}

// A completed source can wait for already-owned memory maintenance to join.
// This evidence is runtime-only: a crash before durable Ready is passive and
// never reconstructs a successful boundary from a generic "no active turns".
type goalHandoverBoundary struct {
	source   goalHandoverSource
	settled  collaboration.Run
	revision uint64
}

func handoverState(r handover.Reservation) handover.State {
	return handover.State{
		OperationID: r.OperationID, GoalID: r.GoalID, SuccessorCohort: r.SuccessorCohort,
		SuccessorSchema: r.SuccessorSchema, ExpiresAt: r.ExpiresAt,
		SourceAgentID: r.SourceAgentID, SourceRunID: r.SourceRunID, SourceRuntimeEpoch: r.SourceRuntimeEpoch,
		SourceTurnGeneration: r.SourceTurnGeneration, SourceCoreOwner: r.SourceCoreOwner, SourceShell: r.SourceShell,
	}
}

func handoverLive(phase handover.Phase) bool {
	return phase == handover.Reserved || phase == handover.Ready
}

func validHandoverReservation(r handover.Reservation) error {
	for _, value := range []string{r.OperationID, r.GoalID, r.SuccessorCohort, r.SuccessorSchema, r.SourceAgentID, r.SourceRunID, r.SourceCoreOwner, r.SourceShell.ClientID} {
		if strings.TrimSpace(value) == "" || len(value) > 1024 {
			return errors.New("goal handover identity is missing or too large")
		}
	}
	hash, err := hex.DecodeString(r.TokenSHA256)
	if err != nil || len(hash) != sha256.Size || strings.ToLower(r.TokenSHA256) != r.TokenSHA256 {
		return errors.New("goal handover requires a SHA-256 nonce digest")
	}
	if r.SourceRuntimeEpoch == 0 || r.SourceShell.Epoch == 0 || !r.ExpiresAt.After(time.Now()) || r.ExpiresAt.After(time.Now().Add(time.Hour)) {
		return errors.New("goal handover source or expiry is invalid")
	}
	return nil
}

// handoverFingerprint is private and covers all effective settings, explicit
// inheritance controls, capabilities, policy, execution selection and Plan mode.
// Hashing avoids persisting account secrets in the sidecar. Mutation hooks also
// invalidate on edits which are subsequently undone to the same value.
func (s *Session) handoverFingerprint() (string, error) {
	s.mu.Lock()
	settings := s.settingsSnapshotSetLocked()
	settings.Revision = 0 // runtime publication clocks restart on cold load
	raw, err := json.Marshal(struct {
		Settings   SettingsSnapshotSet
		Accounts   any
		Disabled   []string
		Selection  ExecutionSelection
		Yolo, Plan bool
	}{
		Settings: settings, Accounts: s.cfg.Accounts,
		Disabled: s.DisabledCapabilities(), Selection: ExecutionSelection{ID: s.executionSelection, Revision: s.executionRevision},
		Yolo: s.YoloEnabled(), Plan: s.PlanMode(),
	})
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Session) loadGoalHandoverLocked() error {
	if s.goalHandoverLoaded {
		return nil
	}
	record, _, err := sessiondb.LoadGoalHandover(s.db, s.sessionID)
	if err != nil {
		return err
	}
	s.goalHandover, s.goalHandoverLoaded = record, true
	return nil
}

func (s *Session) commitHandoverLocked(next sessiondb.GoalHandoverRecord, expectedGoal string, goal *sessiondb.GoalRecord) error {
	next.State.LifecycleEpoch = s.goalHandover.State.LifecycleEpoch + 1
	if err := sessiondb.CommitGoalHandover(s.db, s.sessionID, s.goalHandover.State.LifecycleEpoch, expectedGoal, next, goal); err != nil {
		return err
	}
	s.goalHandover = next
	if goal != nil {
		s.goal.record = *goal
	}
	return nil
}

func (s *Session) invalidateGoalHandoverLocked(reason string) error {
	if err := s.loadGoalHandoverLocked(); err != nil {
		return err
	}
	if !handoverLive(s.goalHandover.State.Phase) && s.goalHandoverTarget == nil {
		return nil
	}
	next := s.goalHandover
	next.State.Phase, next.State.Reason = handover.Invalidated, reason
	var goal *sessiondb.GoalRecord
	if s.goalHandoverTarget != nil && s.handoverGoalEligibleLocked(false) && next.GoalControl == sessiondb.GoalControl(s.goal.record) {
		updated := s.goal.record
		updated.Goal.Waiting = true
		goal = &updated
		next.GoalControl = sessiondb.GoalControl(updated)
	}
	if err := s.commitHandoverLocked(next, "", goal); err != nil {
		return err
	}
	s.goalHandoverTarget = nil
	s.goalHandoverBoundary = nil
	return nil
}

// invalidateGoalHandover must run before an accepted human/control mutation.
// It holds no actor, event-delivery or settings mutex while calling another owner.
func (s *Session) invalidateGoalHandover(reason string) error {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	return s.invalidateGoalHandoverLocked(reason)
}

// ReserveGoalHandover freezes only the NEXT automatic goal turn. The currently
// running root is never cancelled, resumed or given a new strike budget.
func (s *Session) ReserveGoalHandover(r handover.Reservation) (handover.State, error) {
	if s == nil {
		return handover.State{}, errors.New("session unavailable")
	}
	if err := validHandoverReservation(r); err != nil {
		return handover.State{}, err
	}
	release, ok := s.beginIdleWork()
	if !ok {
		return handover.State{}, errors.New("session lifetime admission is fenced")
	}
	defer release()
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	s.settingsOperationMu.Lock()
	defer s.settingsOperationMu.Unlock()
	fingerprint, err := s.handoverFingerprint()
	if err != nil {
		return handover.State{}, err
	}
	store := s.CollaborationStore()
	if store == nil {
		return handover.State{}, errors.New("source collaboration owner unavailable")
	}
	actual, err := store.Agent(context.Background(), r.SourceAgentID)
	if err != nil || actual.ParentID != "" || actual.SessionID != s.sessionID || actual.CurrentRunID != r.SourceRunID || actual.RuntimeEpoch != r.SourceRuntimeEpoch ||
		actual.State != collaboration.AgentAvailable || actual.Presence != collaboration.RuntimeAttached {
		return handover.State{}, errors.New("handover source is not this session's running root")
	}
	run, err := store.Run(context.Background(), r.SourceRunID)
	if err != nil || run.AgentID != r.SourceAgentID || run.RuntimeEpoch != r.SourceRuntimeEpoch || run.State != collaboration.RunRunning {
		return handover.State{}, errors.New("handover source run is not running")
	}
	s.orchMu.Lock()
	original := s.orchestrators[r.SourceAgentID]
	s.orchMu.Unlock()
	if original == nil {
		return handover.State{}, errors.New("handover source has no live root owner")
	}
	original.turnMu.Lock()
	live := original.RunID == r.SourceRunID && original.RuntimeEpoch == r.SourceRuntimeEpoch && original.turnCancel != nil &&
		!original.turnCancelling && !original.turnSealed && !original.closing.Load()
	original.turnMu.Unlock()
	if !live {
		return handover.State{}, errors.New("handover source is stopped, sealed or no longer running")
	}
	if s.PlanMode() || len(s.pendingAdmissions) != 0 || len(s.QueuedInputs()) != 0 || s.handoverHasRecovery() || s.handoverHasLoops() {
		return handover.State{}, errors.New("plan, queued input, recovery or scheduled work prevents handover")
	}
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	if err := s.loadGoalHandoverLocked(); err != nil {
		return handover.State{}, err
	}
	source := s.goalHandoverSource
	if source.agentID != r.SourceAgentID || source.runID != r.SourceRunID || source.epoch != r.SourceRuntimeEpoch || source.generation == 0 ||
		r.SourceTurnGeneration != 0 && r.SourceTurnGeneration != source.generation {
		return handover.State{}, errors.New("handover source turn changed")
	}
	r.SourceTurnGeneration = source.generation
	if source.target.WorkspaceID == "" || source.target.SessionID != s.sessionID || source.target.Shell == nil || *source.target.Shell != r.SourceShell {
		return handover.State{}, errors.New("handover must retain the actual submitting shell")
	}
	if atomic.LoadInt32(&s.closing) != 0 || !s.goal.present || s.goal.record.Goal.ID != r.GoalID || !s.handoverGoalEligibleLocked(false) {
		return handover.State{}, errors.New("handover requires the unchanged active nonwaiting goal")
	}
	if s.goalHandover.State.OperationID == r.OperationID {
		if s.goalHandover.Reservation != r {
			return handover.State{}, errors.New("handover operation identity reused with different input")
		}
		return s.goalHandover.State, nil
	}
	if handoverLive(s.goalHandover.State.Phase) || s.goalHandoverTarget != nil {
		return handover.State{}, errors.New("another goal handover is outstanding")
	}
	next := sessiondb.GoalHandoverRecord{Reservation: r, State: handoverState(r), GoalControl: sessiondb.GoalControl(s.goal.record),
		SettingsFingerprint: fingerprint, SourceTarget: source.target.Clone()}
	next.State.Phase = handover.Reserved
	if err := s.commitHandoverLocked(next, next.GoalControl, nil); err != nil {
		return handover.State{}, err
	}
	return s.goalHandover.State, nil
}

func (s *Session) handoverHasLoops() bool {
	for _, loop := range s.Loops() {
		if !loop.Paused {
			return true
		}
	}
	return false
}

func (s *Session) handoverHasRecovery() bool {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	// A live turn's ordinary checkpoint is not recovery. Pending/parked
	// checkpoints and actionable supervisor states are.
	if len(s.pendingRecoveries) != 0 {
		return true
	}
	for _, slot := range s.recoverySlots {
		if slot != nil && (slot.status.CanRetry() || slot.status.Attempt > 0 || slot.status.State == recoverytypes.Interrupted) {
			return true
		}
	}
	return false
}

func (s *Session) handoverGoalEligibleLocked(waiting bool) bool {
	g := s.goal.record
	return s.goal.present && g.Goal.Status == types.GoalActive && g.Goal.Waiting == waiting && g.VerifierID == "" &&
		(g.Goal.TokenBudget <= 0 || g.Goal.TokensUsed < g.Goal.TokenBudget) &&
		s.goal.emptyTurns < goalStrikeLimit && s.goal.failingTurns < goalStrikeLimit && s.goal.verifierFailures < goalStrikeLimit
}

func (s *Session) expireGoalHandoverLocked() error {
	if handoverLive(s.goalHandover.State.Phase) && !s.goalHandover.State.ExpiresAt.After(time.Now()) {
		next := s.goalHandover
		next.State.Phase, next.State.Reason = handover.Expired, "The handover permit expired; the goal remains protected and requires explicit recovery."
		return s.commitHandoverLocked(next, "", nil)
	}
	return nil
}

// GoalHandover observes one exact operation. Expiry never releases an owned
// waiting goal or removes active-goal idle protection.
func (s *Session) GoalHandover(operationID string) (handover.State, error) {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	if err := s.loadGoalHandoverLocked(); err != nil {
		return handover.State{}, err
	}
	if operationID == "" || s.goalHandover.State.OperationID != operationID {
		return handover.State{}, errors.New("goal handover operation not found")
	}
	if err := s.expireGoalHandoverLocked(); err != nil {
		return handover.State{}, err
	}
	return s.goalHandover.State, nil
}

func validHandoverToken(record sessiondb.GoalHandoverRecord, token string) bool {
	if len(token) < 16 || len(token) > 256 {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(hash), []byte(record.Reservation.TokenSHA256)) == 1
}

// CancelGoalHandover releases only its own exact unchanged waiting boundary,
// never a pause, newer goal or user-changed controls.
func (s *Session) CancelGoalHandover(operationID, token string) error {
	release, ok := s.beginIdleWork()
	if !ok {
		return errors.New("session lifetime admission is fenced")
	}
	defer release()
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	s.settingsOperationMu.Lock()
	defer s.settingsOperationMu.Unlock()
	fingerprint, err := s.handoverFingerprint()
	if err != nil {
		return err
	}
	s.msgsMu.Lock()
	revision := s.contextRevision
	s.msgsMu.Unlock()
	total := s.tokenTotal()
	s.goalMu.Lock()
	if err := s.loadGoalHandoverLocked(); err != nil {
		s.goalMu.Unlock()
		return err
	}
	record := s.goalHandover
	if record.State.OperationID != operationID || !validHandoverToken(record, token) {
		s.goalMu.Unlock()
		return errors.New("goal handover authorization failed")
	}
	if record.State.Phase == handover.Cancelled {
		s.goalMu.Unlock()
		return nil
	}
	if err := s.expireGoalHandoverLocked(); err != nil {
		s.goalMu.Unlock()
		return err
	}
	if !handoverLive(s.goalHandover.State.Phase) {
		s.goalMu.Unlock()
		return errors.New("goal handover is no longer cancellable")
	}
	next := record
	next.State.Phase, next.State.Reason = handover.Cancelled, "Cancelled by the operation owner."
	var goal *sessiondb.GoalRecord
	if record.State.Phase == handover.Ready && s.handoverGoalEligibleLocked(true) &&
		sessiondb.GoalControl(s.goal.record) == record.GoalControl && fingerprint == record.SettingsFingerprint && revision == record.State.ReadyContextRevision {
		updated := s.goal.record
		updated.Goal.Waiting = false
		goal = &updated
	}
	expectedGoal := ""
	if goal != nil {
		expectedGoal = record.GoalControl
	}
	err = s.commitHandoverLocked(next, expectedGoal, goal)
	if err == nil {
		s.goalAccountLocked(total)
	}
	s.goalMu.Unlock()
	if err == nil && goal != nil {
		s.publishGoal(goal.Goal, true)
	}
	return err
}

func validateGoalHandoverContinuation(record sessiondb.GoalHandoverRecord, sessionID, operationID, token, cohort, schema string, target control.Target) error {
	r := record.Reservation
	if r.OperationID != operationID || !validHandoverToken(record, token) || r.SuccessorCohort != cohort || r.SuccessorSchema != schema {
		return errors.New("goal handover authorization or successor identity failed")
	}
	if target.WorkspaceID == "" || target.WorkspaceID != record.SourceTarget.WorkspaceID || target.SessionID != sessionID ||
		target.ExecutionID != "" || target.ResourceID != "" || target.Shell == nil || target.Shell.ClientID != r.SourceShell.ClientID || target.Shell.Epoch == 0 {
		return errors.New("goal handover destination is not the original session and shell client")
	}
	return nil
}

// ContinueGoalHandover consumes before admission. The caller is the authenticated
// successor core: it verifies owner/build/schema and supplies its CURRENT target,
// not old native-run credentials. The session independently rejects foreign
// sessions/executions and replacement shell clients.
func (s *Session) ContinueGoalHandover(operationID, token, successorCohort, successorSchema string, target control.Target) (handover.State, error) {
	release, ok := s.beginIdleWork()
	if !ok {
		return handover.State{}, errors.New("session lifetime admission is fenced")
	}
	defer release()
	// Continuation must not race a newly admitted memory job after observing
	// idleness. This short owner reservation never cancels or waits under a
	// mutex for running work; a pending join remains an explicit no-effect result.
	s.memoryJobs.mu.Lock()
	maintenanceBusy := s.memoryJobs.active != 0
	maintenanceReserved := !maintenanceBusy && !s.memoryJobs.fenced
	if maintenanceReserved {
		s.memoryJobs.fenced = true
	}
	s.memoryJobs.mu.Unlock()
	if maintenanceReserved {
		defer func() {
			s.memoryJobs.mu.Lock()
			s.memoryJobs.fenced = false
			s.memoryJobs.mu.Unlock()
		}()
	}
	s.admissionMu.Lock()
	s.settingsOperationMu.Lock()
	var published *types.Goal
	defer func() {
		s.settingsOperationMu.Unlock()
		s.admissionMu.Unlock()
		if published != nil {
			s.publishGoal(*published, true)
		}
	}()
	// Re-observing a consumed permit is read-only, including while its admitted
	// turn is active. Fresh consumption instead reserves the same canonical gate
	// as root input/compaction and checks the history owner's exclusion under
	// admissionMu. Activity and a context-revision sample are not reservations.
	s.goalMu.Lock()
	if err := s.loadGoalHandoverLocked(); err != nil {
		s.goalMu.Unlock()
		return handover.State{}, err
	}
	prior := s.goalHandover
	if err := validateGoalHandoverContinuation(prior, s.sessionID, operationID, token, successorCohort, successorSchema, target); err != nil {
		s.goalMu.Unlock()
		return handover.State{}, err
	}
	s.goalMu.Unlock()
	if prior.State.Phase == handover.Consumed {
		return prior.State, nil
	}
	if err := s.ValidateHistoryAdmission(); err != nil {
		return prior.State, err
	}
	if !s.turnGate.TryLock() {
		return prior.State, errors.New("a turn or compaction owns the continuation boundary")
	}
	defer s.turnGate.Unlock()
	fingerprint, err := s.handoverFingerprint()
	if err != nil {
		return handover.State{}, err
	}
	activity := s.Activity()
	busy := activity.Any() || maintenanceBusy || !maintenanceReserved || len(s.pendingAdmissions) != 0 || len(s.QueuedInputs()) != 0 || s.handoverHasRecovery() || s.handoverHasLoops() || s.PlanMode()
	total := s.tokenTotal()
	s.msgsMu.Lock()
	revision := s.contextRevision
	s.msgsMu.Unlock()
	s.goalMu.Lock()
	if err := s.loadGoalHandoverLocked(); err != nil {
		s.goalMu.Unlock()
		return handover.State{}, err
	}
	record := s.goalHandover
	if err := validateGoalHandoverContinuation(record, s.sessionID, operationID, token, successorCohort, successorSchema, target); err != nil {
		s.goalMu.Unlock()
		return handover.State{}, err
	}
	if record.State.Phase == handover.Consumed {
		s.goalMu.Unlock()
		return record.State, nil
	}
	if err := s.expireGoalHandoverLocked(); err != nil {
		s.goalMu.Unlock()
		return handover.State{}, err
	}
	if s.goalHandover.State.Phase == handover.Ready && maintenanceBusy &&
		activity.Turns+activity.Children+activity.AsyncCommands+activity.Compaction == 0 &&
		len(s.pendingAdmissions) == 0 && len(s.QueuedInputs()) == 0 && !s.handoverHasRecovery() &&
		!s.handoverHasLoops() && !s.PlanMode() && atomic.LoadInt32(&s.closing) == 0 &&
		s.handoverGoalEligibleLocked(true) && sessiondb.GoalControl(s.goal.record) == record.GoalControl &&
		fingerprint == record.SettingsFingerprint && revision == record.State.ReadyContextRevision {
		state := s.goalHandover.State
		s.goalMu.Unlock()
		return state, handover.ErrMaintenancePending
	}
	if s.goalHandover.State.Phase != handover.Ready || busy || atomic.LoadInt32(&s.closing) != 0 ||
		!s.handoverGoalEligibleLocked(true) || sessiondb.GoalControl(s.goal.record) != record.GoalControl ||
		fingerprint != record.SettingsFingerprint || revision != record.State.ReadyContextRevision {
		state := s.goalHandover.State
		reason := fmt.Sprintf("goal handover not ready: phase=%s, work_busy=%t, closing=%t, eligible_goal=%t, unchanged_goal=%t, unchanged_settings=%t, context_revision=%d (expected %d)",
			state.Phase, busy, atomic.LoadInt32(&s.closing) != 0, s.handoverGoalEligibleLocked(true),
			sessiondb.GoalControl(s.goal.record) == record.GoalControl, fingerprint == record.SettingsFingerprint,
			revision, record.State.ReadyContextRevision)
		s.goalMu.Unlock()
		return state, errors.New(reason)
	}
	next := record
	next.State.Phase = handover.Consumed
	next.State.Reason = "Permit consumed; a crash before admission requires manual recovery and is never replayed."
	goal := s.goal.record
	goal.Goal.Waiting = false
	next.GoalControl = sessiondb.GoalControl(goal)
	if err := s.commitHandoverLocked(next, record.GoalControl, &goal); err != nil {
		s.goalMu.Unlock()
		return handover.State{}, err
	}
	bound := target.Clone()
	bound.ExecutionID = record.SourceTarget.ExecutionID
	s.goalHandoverTarget = &bound
	s.goal.emptyTurns, s.goal.failingTurns, s.goal.verifierFailures = record.EmptyTurns, record.FailingTurns, record.VerifierFailures
	s.goal.findings = record.Findings
	s.goalAccountLocked(total)
	state := s.goalHandover.State
	s.goalMu.Unlock()
	published = &goal.Goal
	return state, nil
}

// readyGoalHandover is called only after successful final context publication,
// collaboration terminal settlement and supervisor resolution of this exact root.
func (s *Session) readyGoalHandover(source goalHandoverSource, settled collaboration.Run, revision uint64, natural bool) {
	s.goalMu.Lock()
	pending := s.goalHandover.State.Phase == handover.Reserved && s.goalHandover.Reservation.SourceRunID == source.runID
	s.goalMu.Unlock()
	if !pending {
		return
	}
	s.admissionMu.Lock()
	s.settingsOperationMu.Lock()
	var published *types.Goal
	defer func() {
		s.settingsOperationMu.Unlock()
		s.admissionMu.Unlock()
		if published != nil {
			s.publishGoal(*published, true)
		}
	}()
	fingerprint, fingerprintErr := s.handoverFingerprint()
	activity := s.Activity()
	pendingInputs, queuedInputs := len(s.pendingAdmissions), len(s.QueuedInputs())
	recovering, scheduled, planMode := s.handoverHasRecovery(), s.handoverHasLoops(), s.PlanMode()
	busy := activity.Any() || pendingInputs != 0 || queuedInputs != 0 || recovering || scheduled || planMode
	s.msgsMu.Lock()
	currentRevision := s.contextRevision
	s.msgsMu.Unlock()
	total := s.tokenTotal()
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	if err := s.loadGoalHandoverLocked(); err != nil {
		return
	}
	record := s.goalHandover
	r := record.Reservation
	if record.State.Phase != handover.Reserved || r.SourceRunID != source.runID || r.SourceAgentID != source.agentID ||
		r.SourceRuntimeEpoch != source.epoch || r.SourceTurnGeneration != source.generation {
		return
	}
	if err := s.expireGoalHandoverLocked(); err != nil || s.goalHandover.State.Phase != handover.Reserved {
		return
	}
	reason := ""
	switch {
	case !natural:
		reason = "The source did not settle naturally without recovery."
	case pendingInputs != 0 || queuedInputs != 0 || recovering || scheduled || planMode ||
		activity.Turns != 0 || activity.Children != 0 || activity.AsyncCommands != 0 || activity.Compaction != 0:
		reason = fmt.Sprintf("Other work owns the boundary: turns=%d children=%d commands=%d compactions=%d memory=%d pending=%d queued=%d recovery=%t loops=%t plan=%t.",
			activity.Turns, activity.Children, activity.AsyncCommands, activity.Compaction, activity.MemoryJobs, pendingInputs, queuedInputs, recovering, scheduled, planMode)
	case fingerprintErr != nil || fingerprint != record.SettingsFingerprint:
		reason = "The source settings or execution controls changed."
	case revision == 0 || currentRevision != revision:
		reason = "The source has no unchanged durable terminal context receipt."
	case settled.ID != r.SourceRunID || settled.AgentID != r.SourceAgentID || settled.RuntimeEpoch != r.SourceRuntimeEpoch ||
		settled.State != collaboration.RunSucceeded || settled.ContextCommitID == "" || settled.ContextCommitID != source.inputID:
		reason = "The source terminal run does not match its exact successful input receipt."
	case !s.handoverGoalEligibleLocked(false) || sessiondb.GoalControl(s.goal.record) != record.GoalControl:
		reason = "The source goal changed or reached a stop rule."
	}
	if reason != "" {
		_ = s.invalidateGoalHandoverLocked(reason)
		return
	}
	if busy {
		s.goalHandoverBoundary = &goalHandoverBoundary{source: source, settled: settled, revision: revision}
		return
	}
	s.goalAccountLocked(total)
	goal := s.goal.record
	goal.Goal.Waiting = true
	next := record
	next.State.Phase, next.State.ReadyContextRevision = handover.Ready, revision
	next.State.Reason = "The exact source root settled naturally; the goal is waiting for its authorized successor."
	next.GoalControl = sessiondb.GoalControl(goal)
	next.EmptyTurns, next.FailingTurns, next.VerifierFailures, next.Findings = s.goal.emptyTurns, s.goal.failingTurns, s.goal.verifierFailures, s.goal.findings
	if err := s.commitHandoverLocked(next, record.GoalControl, &goal); err == nil {
		s.goalAccountLocked(total)
		s.goalHandoverBoundary = nil
		published = &goal.Goal
	}
}

func (s *Session) reconsiderGoalHandover() {
	s.goalMu.Lock()
	boundary := s.goalHandoverBoundary
	s.goalMu.Unlock()
	if boundary != nil {
		s.readyGoalHandover(boundary.source, boundary.settled, boundary.revision, true)
	}
}

// readyGoalHandoverLocked is deliberately a read-only lifetime predicate.
// Expired/unknown state always retains ordinary active-goal protection.
func (s *Session) readyGoalHandoverLocked() bool {
	return s.goalHandoverLoaded && s.goalHandover.State.Phase == handover.Ready &&
		s.goalHandover.State.ExpiresAt.After(time.Now()) && s.handoverGoalEligibleLocked(true) &&
		s.goalHandover.State.GoalID == s.goal.record.Goal.ID && s.goalHandover.GoalControl == sessiondb.GoalControl(s.goal.record)
}

func (s *Session) goalAllowsIdleHandover() bool {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	return !s.goal.present || s.goal.record.Goal.Status != types.GoalActive || s.readyGoalHandoverLocked()
}

// saveGoalWithHandoverLocked invalidates control changes in the same transaction
// as the goal save. Normal time/token accounting is not a lifecycle edit.
func (s *Session) saveGoalWithHandoverLocked() error {
	if err := s.loadGoalHandoverLocked(); err != nil {
		return err
	}
	record := s.goalHandover
	if (handoverLive(record.State.Phase) || s.goalHandoverTarget != nil) && record.GoalControl != sessiondb.GoalControl(s.goal.record) {
		next := record
		next.State.Phase, next.State.Reason = handover.Invalidated, "Goal control changed."
		if err := s.commitHandoverLocked(next, "", &s.goal.record); err != nil {
			return err
		}
		s.goalHandoverTarget = nil
		return nil
	}
	return sessiondb.SaveGoal(s.db, s.sessionID, s.goal.record)
}

func (s *Session) restoreGoalHandoverLocked() error {
	if err := s.loadGoalHandoverLocked(); err != nil {
		return err
	}
	record := s.goalHandover
	if record.State.Phase == "" {
		return nil
	}
	if record.State.GoalID == s.goal.record.Goal.ID && (record.State.Phase == handover.Ready || record.State.Phase == handover.Consumed) {
		s.goal.emptyTurns, s.goal.failingTurns, s.goal.verifierFailures, s.goal.findings = record.EmptyTurns, record.FailingTurns, record.VerifierFailures, record.Findings
	}
	if record.State.Phase == handover.Reserved {
		return s.invalidateGoalHandoverLocked("Source runtime ended before an authorized natural boundary.")
	}
	return s.expireGoalHandoverLocked()
}
