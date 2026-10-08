package agent

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	rand2 "math/rand/v2"
	"net"
	"strings"
	"time"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/retry"
	ttypes "crude/types"
	recoverytypes "crude/types/recovery"
)

type RecoveryFailureKind string

const (
	RecoveryFailureTransport     RecoveryFailureKind = "transport"
	RecoveryFailureConfiguration RecoveryFailureKind = "configuration"
	RecoveryFailureStorage       RecoveryFailureKind = "storage"
	RecoveryFailureGuidance      RecoveryFailureKind = "guidance"
	RecoveryFailureRepeatedTools RecoveryFailureKind = "repeated_tools"
	RecoveryFailureTruncated     RecoveryFailureKind = "truncated"
	RecoveryFailureMissingReport RecoveryFailureKind = "missing_report"
	RecoveryFailureStructural    RecoveryFailureKind = "structural"
	RecoveryFailurePanic         RecoveryFailureKind = "panic"
	RecoveryFailureUnknown       RecoveryFailureKind = "unknown"
)

type AttentionDisposition string

const (
	AttentionWait        AttentionDisposition = "wait"
	AttentionParkAttempt AttentionDisposition = "park_attempt"
)

type SupervisedRunState string

const (
	SupervisedRunCompleted SupervisedRunState = "completed"
	SupervisedRunCancelled SupervisedRunState = "cancelled"
	SupervisedRunParked    SupervisedRunState = "parked"
)

// SupervisedRunResult is the single terminal snapshot of one supervised
// attempt. A parked attempt leaves the durable task open for explicit repair;
// it is neither completion nor cancellation.
type SupervisedRunResult struct {
	Archive  []provider.ChatMessage
	Active   []provider.ChatMessage
	State    SupervisedRunState
	Failure  RecoveryFailure
	Incident recoverytypes.Status
}

// RecoveryFailure describes how the supervisor may safely continue one
// unfinished run. AutomaticLimit < 0 means retry indefinitely; zero means park.
type RecoveryFailure struct {
	Kind           RecoveryFailureKind
	Cause          error
	Summary        string
	Action         string
	Continuation   rootprompt.RecoveryKind
	AutomaticLimit int
}

func TransientRecovery(err error) RecoveryFailure {
	return RecoveryFailure{
		Kind: RecoveryFailureTransport, Cause: err,
		Summary:      "Provider connection is recovering",
		Action:       "Stop to cancel; recovery continues automatically",
		Continuation: rootprompt.RecoveryTransport, AutomaticLimit: -1,
	}
}

func AttentionRecovery(kind RecoveryFailureKind, err error) RecoveryFailure {
	failure := RecoveryFailure{
		Kind: kind, Cause: err, AutomaticLimit: 0,
		Summary:      "Run needs attention",
		Action:       "Resolve the issue, then Continue",
		Continuation: rootprompt.RecoveryInterrupted,
	}
	switch kind {
	case RecoveryFailureConfiguration:
		failure.Summary = provider.ErrorSummary(err)
		failure.Action = "Update the provider or agent settings, then Continue"
		var status *provider.StatusError
		if errors.As(err, &status) {
			switch status.StatusCode {
			case 401, 403:
				failure.Action = "Reconnect or update the account in Settings → Accounts, then Continue"
			case 429:
				failure.Action = "Resolve this account's quota or billing, then Continue"
			case 404:
				failure.Action = "Update the account endpoint or model, then Continue"
			}
		}
	case RecoveryFailureStorage:
		failure.Summary = "Recovery state could not be saved"
		failure.Action = "Restore writable storage, then Continue"
	case RecoveryFailureGuidance:
		failure.Summary = "Project guidance needs attention"
		failure.Action = "Fix the guidance source or connection, then Continue"
	case RecoveryFailureMissingReport:
		failure.Summary = "Final report is still required"
		failure.Action = "Continue to publish the required final report"
		failure.Continuation = rootprompt.RecoveryMissingReport
	case RecoveryFailureStructural:
		failure.Summary = "Child run is blocked by its ownership scope"
		failure.Action = "Repair the exact claim, then Continue the preserved run"
		failure.Continuation = rootprompt.RecoveryInterrupted
	case RecoveryFailurePanic:
		failure.Summary = "Run paused after an internal failure"
		failure.Action = "Inspect current state, then Continue"
		failure.Continuation = rootprompt.RecoveryUncertain
	case RecoveryFailureUnknown:
		failure.Summary = "Run paused before a safe finish"
	}
	return failure
}

func CorrectiveRecovery(kind RecoveryFailureKind, err error) RecoveryFailure {
	failure := RecoveryFailure{
		Kind: kind, Cause: err, AutomaticLimit: 2,
		Summary:      "Correcting an unfinished run",
		Action:       "Recovery will retry with a different continuation",
		Continuation: rootprompt.RecoveryInterrupted,
	}
	switch kind {
	case RecoveryFailureRepeatedTools:
		failure.Summary = "Correcting a repeated tool loop"
		failure.Continuation = rootprompt.RecoveryRepeatedTools
	case RecoveryFailureTruncated:
		failure.Summary = "Continuing truncated output"
		failure.Continuation = rootprompt.RecoveryOutputTruncated
	case RecoveryFailureMissingReport:
		failure.Summary = "Recovering the required final report"
		failure.Continuation = rootprompt.RecoveryMissingReport
	}
	return failure
}

// Supervisor is the one root/child recovery state machine. It never decides
// replay safety from elapsed time; the failure owner supplies that decision.
type Supervisor struct {
	AgentID    string
	Generation uint64
	Wake       <-chan struct{}
	Emit       func(AgentEvent)
	Persist    func(recoverytypes.Status) error
	Discard    func(string) error
	LogPersist func(error)

	RetryBase    time.Duration
	RetryCeiling time.Duration
	Wait         func(context.Context, time.Duration) error
	Now          func() time.Time

	status  *recoverytypes.Status
	visible bool
}

func NewSupervisor(agentID string, generation uint64, initial *recoverytypes.Status) *Supervisor {
	supervisor := &Supervisor{
		AgentID: agentID, Generation: generation,
		RetryBase: time.Second, RetryCeiling: retry.MaxCooldown,
		Wait: retry.Wait, Now: time.Now,
		status: &recoverytypes.Status{
			ID: newRecoveryID(), AgentID: agentID, Generation: generation,
		},
	}
	if initial != nil {
		copy := *initial
		copy.AgentID = agentID
		copy.Generation = generation
		supervisor.status = &copy
		supervisor.visible = copy.State != ""
	}
	return supervisor
}

func (s *Supervisor) Current() (recoverytypes.Status, bool) {
	if s == nil || s.status == nil || !s.visible {
		return recoverytypes.Status{}, false
	}
	return *s.status, true
}

// CheckpointStatus identifies the durable pending run before any failure is
// visible. Its empty state is internal and is never returned by hydration.
func (s *Supervisor) CheckpointStatus() recoverytypes.Status {
	if s == nil || s.status == nil {
		return recoverytypes.Status{}
	}
	now := s.now()
	if s.status.StartedAt.IsZero() {
		s.status.StartedAt = now
	}
	s.status.UpdatedAt = now
	return *s.status
}

func (s *Supervisor) IncidentID() string {
	if s == nil || s.status == nil {
		return ""
	}
	return s.status.ID
}

func (s *Supervisor) Await(ctx context.Context, failure RecoveryFailure) bool {
	if s == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if failure.Cause == nil {
		failure.Cause = errors.New("unfinished run")
	}
	status := s.ensureStatus()
	s.visible = true
	status.Attempt++
	status.Detail = recoveryDiagnostic(failure.Cause)
	status.Summary = failure.Summary
	status.Action = failure.Action
	status.RetryAt = time.Time{}

	automatic := failure.AutomaticLimit < 0 ||
		failure.AutomaticLimit > 0 && status.Attempt <= failure.AutomaticLimit
	if automatic {
		delay := retry.Exponential(status.Attempt, s.RetryBase, s.RetryCeiling)
		var providerStatus *provider.StatusError
		if errors.As(failure.Cause, &providerStatus) {
			delay = max(delay, retry.Clamp(providerStatus.RetryAfter))
		}
		delay = retry.Clamp(delay)
		status.State = recoverytypes.Recovering
		status.RetryAt = s.now().Add(delay)
		if !s.transition(*status) {
			return s.awaitAttention(ctx, status, false)
		}
		wait := s.Wait
		if wait == nil {
			wait = retry.Wait
		}
		waitCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		waited := make(chan error, 1)
		go func() { waited <- wait(waitCtx, delay) }()
		select {
		case <-ctx.Done():
			return false
		case <-s.Wake:
			// An exact Retry now/settings wake may shorten this cooldown.
		case err := <-waited:
			if err != nil {
				return false
			}
		}
		if ctx.Err() != nil {
			return false
		}
		status = s.ensureStatus()
		status.RetryAt = time.Time{}
		status.Summary = "Retrying unfinished run"
		if !s.transition(*status) {
			return s.awaitAttention(ctx, s.ensureStatus(), false)
		}
		return true
	}
	status.State = recoverytypes.NeedsAttention
	return s.awaitAttention(ctx, status, true)
}

// BeginRetry transitions a passively restored incident only after an explicit,
// generation-fenced owner action has submitted it.
func (s *Supervisor) BeginRetry() error {
	if s == nil || s.status == nil || s.status.State == "" {
		return nil
	}
	s.visible = true
	status := *s.status
	status.State = recoverytypes.Recovering
	status.RetryAt = time.Time{}
	status.Summary = "Retrying unfinished run"
	status.Action = "Stop to cancel"
	if !s.transition(status) {
		return errors.New("recovery state could not be saved")
	}
	return nil
}

// Park records an abnormal owner exit without waiting in the unwinding stack.
// The same durable task may be submitted later through RetryRecoveryFor.
func (s *Supervisor) Park(failure RecoveryFailure) recoverytypes.Status {
	status := s.ensureStatus()
	s.visible = true
	status.Attempt++
	status.State = recoverytypes.NeedsAttention
	status.RetryAt = time.Time{}
	status.Summary = failure.Summary
	status.Detail = recoveryDiagnostic(failure.Cause)
	status.Action = failure.Action
	_ = s.transition(*status)
	return *s.status
}

func (s *Supervisor) Resolve() error {
	if s == nil || s.status == nil {
		return nil
	}
	visible := s.visible
	if !visible {
		if s.Discard != nil {
			return s.Discard(s.status.ID)
		}
		return nil
	}
	status := *s.status
	status.State = recoverytypes.Recovered
	status.RetryAt = time.Time{}
	status.Summary = "Run recovered"
	status.Action = ""
	now := s.now()
	if status.StartedAt.IsZero() {
		status.StartedAt = now
	}
	status.UpdatedAt = now
	if s.Persist != nil {
		if err := s.Persist(status); err != nil {
			return err
		}
	}
	s.status = &status
	s.emit(status)
	return nil
}

// Cancel settles the unfinished task only for explicit owner cancellation.
func (s *Supervisor) Cancel() error {
	if s == nil || s.status == nil {
		return nil
	}
	if !s.visible {
		if s.Discard != nil {
			return s.Discard(s.status.ID)
		}
		return nil
	}
	if s.status.State == recoverytypes.Cancelled {
		return nil
	}
	status := *s.status
	status.State = recoverytypes.Cancelled
	status.RetryAt = time.Time{}
	status.Summary = "Run cancelled"
	status.Action = ""
	now := s.now()
	if status.StartedAt.IsZero() {
		status.StartedAt = now
	}
	status.UpdatedAt = now
	if s.Persist != nil {
		if err := s.Persist(status); err != nil {
			return err
		}
	}
	s.status = &status
	s.emit(status)
	return nil
}

func (s *Supervisor) ensureStatus() *recoverytypes.Status {
	return s.status
}

func (s *Supervisor) transition(status recoverytypes.Status) bool {
	now := s.now()
	if status.StartedAt.IsZero() {
		status.StartedAt = now
	}
	status.UpdatedAt = now
	if s.Persist != nil {
		if err := s.Persist(status); err != nil {
			if s.LogPersist != nil {
				s.LogPersist(err)
			}
			status.State = recoverytypes.NeedsAttention
			status.RetryAt = time.Time{}
			status.Summary = "Recovery state could not be saved"
			status.Detail = recoveryDiagnostic(err)
			status.Action = "Restore writable storage, then Continue"
			s.status = &status
			s.emit(status)
			return false
		}
	}
	s.status = &status
	s.emit(status)
	return true
}

func (s *Supervisor) awaitAttention(ctx context.Context, status *recoverytypes.Status, announce bool) bool {
	for {
		if announce {
			status.State = recoverytypes.NeedsAttention
			status.RetryAt = time.Time{}
			_ = s.transition(*status)
		}
		announce = false
		if s.Wake == nil {
			<-ctx.Done()
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-s.Wake:
		}
		status.State = recoverytypes.Recovering
		status.RetryAt = time.Time{}
		status.Summary = "Retrying unfinished run"
		status.Action = "Stop to cancel"
		if s.transition(*status) {
			return true
		}
		// transition already emitted the persistence-blocked attention state.
	}
}

func (s *Supervisor) emit(status recoverytypes.Status) {
	if s.Emit != nil {
		s.Emit(AgentEvent{Type: "recovery", Data: status, AgentID: status.AgentID})
	}
}

func (s *Supervisor) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newRecoveryID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("recovery-%d", time.Now().UnixNano())
	}
	return "recovery-" + hex.EncodeToString(raw[:])
}

func recoveryDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"authorization:", "bearer ", "api_key", "api-key", "apikey",
		"access_token", "refresh_token", "password", "private key", "secret=",
	} {
		if strings.Contains(lower, marker) {
			return "Diagnostic contained credential-like data and was redacted. Consult the local session log."
		}
	}
	return text
}

func classifyRecoveryError(err error) RecoveryFailure {
	if err == nil {
		return AttentionRecovery(RecoveryFailureUnknown, errors.New("run ended without a natural finish"))
	}
	retryErr := err
	var exhausted *provider.ExhaustedError
	if errors.As(err, &exhausted) && exhausted.Cause != nil {
		retryErr = exhausted.Cause
	}
	if provider.Retryable(retryErr) || errors.Is(retryErr, context.DeadlineExceeded) {
		return TransientRecovery(err)
	}
	var dns *net.DNSError
	if errors.As(retryErr, &dns) {
		// A resolver's "not found" can reflect loss of connectivity or a
		// temporarily missing provider record. Request retries remain bounded;
		// the owned task rechecks without requiring a human /retry.
		return TransientRecovery(err)
	}
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknownAuthority) || errors.As(err, &certificateInvalid) || errors.As(err, &hostname) {
		return AttentionRecovery(RecoveryFailureConfiguration, err)
	}
	return AttentionRecovery(RecoveryFailureConfiguration, err)
}

func classifyRecordedRecovery(kind RecoveryFailureKind, err error) RecoveryFailure {
	switch kind {
	case RecoveryFailureTransport:
		return classifyRecoveryError(err)
	case RecoveryFailureGuidance:
		return AttentionRecovery(kind, err)
	case RecoveryFailureStorage:
		return AttentionRecovery(kind, err)
	case RecoveryFailureConfiguration:
		return AttentionRecovery(kind, err)
	case RecoveryFailureTruncated:
		return CorrectiveRecovery(kind, err)
	case RecoveryFailureRepeatedTools:
		return CorrectiveRecovery(kind, err)
	case RecoveryFailureMissingReport:
		return CorrectiveRecovery(kind, err)
	case RecoveryFailureStructural:
		return AttentionRecovery(kind, err)
	case RecoveryFailurePanic:
		return AttentionRecovery(kind, err)
	case RecoveryFailureUnknown:
		return AttentionRecovery(kind, err)
	default:
		return classifyRecoveryError(err)
	}
}

// SupervisedRunOptions lets the session refresh settings and atomically capture
// the latest archive/context without owning another recovery state machine.
type SupervisedRunOptions struct {
	Supervisor *Supervisor
	Prepare    func() (string, error)
	Snapshot   func(archive, active []provider.ChatMessage)
	// OnAttention lets the domain owner end only the current attempt for a
	// structural, non-automatic blocker. Nil preserves the root wait/repair
	// behavior. Transient and bounded corrective recovery do not consult it.
	OnAttention func(RecoveryFailure) AttentionDisposition
	// DeferSettlement lets the session commit transcript/context before the
	// recovery task is resolved or discarded.
	DeferSettlement bool
}

// RunSupervised preserves canonical output/tool results across attempts. It
// suppresses terminal error events for recoverable attempts and emits done only
// after a natural finish or explicit cancellation.
func (a *Agent) RunSupervised(
	ctx context.Context,
	sessionMessages []provider.ChatMessage,
	systemPrompt string,
	emit func(AgentEvent),
	blockResolveCh <-chan ttypes.BlockResponse,
	sessionID string,
	runtimeCh <-chan RuntimeMessage,
	options SupervisedRunOptions,
) SupervisedRunResult {
	if options.Supervisor == nil {
		if emit != nil {
			emit(AgentEvent{Type: "error", Data: "Recovery supervisor is unavailable; the pending run was not started."})
		}
		messages := append([]provider.ChatMessage(nil), sessionMessages...)
		return SupervisedRunResult{Archive: messages, Active: messages, State: SupervisedRunParked,
			Failure: AttentionRecovery(RecoveryFailureConfiguration, errors.New("recovery supervisor is unavailable"))}
	}
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	a.preserveReportState = true
	defer func() { a.preserveReportState = false }()

	active := append([]provider.ChatMessage(nil), sessionMessages...)
	archive := append([]provider.ChatMessage(nil), a.ContextArchive...)
	if len(archive) == 0 {
		archive = append([]provider.ChatMessage(nil), sessionMessages...)
	}
	a.lastActiveContext = append([]provider.ChatMessage(nil), active...)
	a.lastArchive = append([]provider.ChatMessage(nil), archive...)
	first := true
	snapshot := func() {
		a.lastActiveContext = append([]provider.ChatMessage(nil), active...)
		a.lastArchive = append([]provider.ChatMessage(nil), archive...)
		if options.Snapshot != nil {
			options.Snapshot(archive, active)
		}
	}
	result := func(state SupervisedRunState, failure RecoveryFailure) SupervisedRunResult {
		snapshot()
		incident, _ := options.Supervisor.Current()
		return SupervisedRunResult{
			Archive: append([]provider.ChatMessage(nil), archive...),
			Active:  append([]provider.ChatMessage(nil), active...),
			State:   state, Failure: failure, Incident: incident,
		}
	}
	parkAttention := func(failure RecoveryFailure) (SupervisedRunResult, bool) {
		if failure.AutomaticLimit != 0 || options.OnAttention == nil ||
			options.OnAttention(failure) != AttentionParkAttempt {
			return SupervisedRunResult{}, false
		}
		status := options.Supervisor.Park(failure)
		snapshot()
		return SupervisedRunResult{
			Archive: append([]provider.ChatMessage(nil), archive...),
			Active:  append([]provider.ChatMessage(nil), active...),
			State:   SupervisedRunParked, Failure: failure, Incident: status,
		}, true
	}
	for {
		if err := ctx.Err(); err != nil {
			archive, active = appendStoppedBoundary(archive, active)
			snapshot()
			if !options.DeferSettlement {
				_ = options.Supervisor.Cancel()
			}
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
			return result(SupervisedRunCancelled, RecoveryFailure{})
		}
		if options.Prepare != nil {
			prepared, err := options.Prepare()
			if err != nil {
				snapshot()
				failure := AttentionRecovery(RecoveryFailureConfiguration, err)
				if parked, ok := parkAttention(failure); ok {
					return parked
				}
				if !options.Supervisor.Await(ctx, failure) {
					archive, active = appendStoppedBoundary(archive, active)
					snapshot()
					if !options.DeferSettlement {
						_ = options.Supervisor.Cancel()
					}
					emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
					return result(SupervisedRunCancelled, failure)
				}
				active, archive = appendRecoveryContinuation(active, archive, rootprompt.RecoveryInterrupted)
				continue
			}
			systemPrompt = prepared
		}
		if !first {
			a.ContextArchive = archiveWithoutSystem(archive)
			a.FrozenPrefix = append([]provider.ChatMessage(nil), active...)
		}
		first = false
		var terminal *AgentEvent
		var panicked any
		attemptEmit := func(event AgentEvent) {
			if event.Type == "error" || event.Type == "done" {
				copy := event
				terminal = &copy
				return
			}
			emit(event)
		}
		func() {
			defer func() { panicked = recover() }()
			archive = a.Run(ctx, active, systemPrompt, attemptEmit, blockResolveCh, sessionID, runtimeCh)
		}()
		if latest := a.ActiveContext(); latest != nil {
			active = latest
		}
		if panicked != nil {
			if lastArchive := a.ArchiveContext(); len(lastArchive) > 0 {
				archive = lastArchive
			}
			if latest := a.ActiveContext(); latest != nil {
				active = latest
			}
		}
		snapshot()

		reason := terminalReason(terminal)
		if panicked == nil && terminal != nil && terminal.Type == "done" &&
			(reason == "completed" || reason == "cancelled") {
			if reason == "cancelled" {
				if !options.DeferSettlement {
					_ = options.Supervisor.Cancel()
				}
				emit(*terminal)
				return result(SupervisedRunCancelled, RecoveryFailure{})
			}
			if !options.DeferSettlement {
				for {
					if err := options.Supervisor.Resolve(); err == nil {
						break
					} else if !options.Supervisor.Await(ctx, AttentionRecovery(RecoveryFailureStorage, err)) {
						archive, active = appendStoppedBoundary(archive, active)
						snapshot()
						_ = options.Supervisor.Cancel()
						emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
						return result(SupervisedRunCancelled, AttentionRecovery(RecoveryFailureStorage, errors.New("recovery settlement was cancelled")))
					}
				}
			}
			emit(*terminal)
			return result(SupervisedRunCompleted, RecoveryFailure{})
		}

		var failure RecoveryFailure
		switch {
		case panicked != nil:
			failure = AttentionRecovery(RecoveryFailurePanic, fmt.Errorf("agent panic: %v", panicked))
		case reason == "stop_condition_met":
			failure = CorrectiveRecovery(RecoveryFailureRepeatedTools, errors.New("repeated tool guard stopped the attempt"))
		case reason == "missing_final_report":
			failure = CorrectiveRecovery(RecoveryFailureMissingReport, errors.New("required final report was not published"))
		case isTruncatedFinishReason(reason):
			failure = CorrectiveRecovery(RecoveryFailureTruncated, fmt.Errorf("provider stopped output with %s", reason))
		default:
			runFailure := a.RunFailure()
			failure = classifyRecordedRecovery(a.RunFailureKind(), runFailure)
			if kind := a.RunRecoveryKind(); kind != "" {
				failure.Continuation = kind
			}
		}
		if parked, ok := parkAttention(failure); ok {
			return parked
		}
		if !options.Supervisor.Await(ctx, failure) {
			archive, active = appendStoppedBoundary(archive, active)
			snapshot()
			if !options.DeferSettlement {
				_ = options.Supervisor.Cancel()
			}
			emit(AgentEvent{Type: "done", Data: map[string]any{"content": "Cancelled.", "reason": "cancelled"}})
			return result(SupervisedRunCancelled, failure)
		}
		active, archive = appendRecoveryContinuation(active, archive, failure.Continuation)
		snapshot()
	}
}

func terminalReason(event *AgentEvent) string {
	if event == nil {
		return ""
	}
	if data, ok := event.Data.(map[string]any); ok {
		reason, _ := data["reason"].(string)
		return reason
	}
	return ""
}

func appendRecoveryContinuation(active, archive []provider.ChatMessage, kind rootprompt.RecoveryKind) ([]provider.ChatMessage, []provider.ChatMessage) {
	message := provider.NewTextMessage("user", rootprompt.RecoveryContinuation(kind))
	message.InternalID = fmt.Sprintf("msg-%016x%016x", rand2.Uint64(), rand2.Uint64())
	message.Origin = provider.OriginContinuation
	message.Visibility = provider.VisibilityContextOnly
	return append(active, message), append(archive, message)
}

func appendStoppedBoundary(archive, active []provider.ChatMessage) ([]provider.ChatMessage, []provider.ChatMessage) {
	if len(active) > 0 && active[len(active)-1].EffectiveOrigin() == provider.OriginTurnStopped {
		return archive, active
	}
	message := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
	message.InternalID = fmt.Sprintf("turn-stopped:%016x%016x", rand2.Uint64(), rand2.Uint64())
	message.Origin = provider.OriginTurnStopped
	message.Visibility = provider.VisibilityContextOnly
	return append(archive, message), append(active, message)
}

func archiveWithoutSystem(messages []provider.ChatMessage) []provider.ChatMessage {
	if len(messages) > 0 && messages[0].Role == "system" {
		return append([]provider.ChatMessage(nil), messages[1:]...)
	}
	return append([]provider.ChatMessage(nil), messages...)
}
