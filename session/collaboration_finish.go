package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"crude/agent"
	"crude/collaboration"
	"crude/sessiondb"
	"crude/types/recovery"
)

// A lifecycle control may commit between the execution owner's version snapshot
// and finalization. Retry only that exact owned run, never another generation.
func finishCollaborationRun(store *collaboration.Store, request collaboration.FinishRunRequest) (collaboration.Run, error) {
	for attempt := 0; attempt < 3; attempt++ {
		finished, err := store.FinishRun(context.Background(), request)
		if !errors.Is(err, collaboration.ErrStale) {
			return finished, err
		}
		current, readErr := store.Run(context.Background(), request.RunID)
		if readErr != nil {
			return collaboration.Run{}, readErr
		}
		if current.AgentID != request.AgentID || current.RuntimeEpoch != request.RuntimeEpoch {
			return collaboration.Run{}, err
		}
		if current.State.Terminal() {
			// Finalization spans the session and workspace stores. A retry
			// may observe its exact lifecycle commit after a later write
			// failed. A matching immutable terminal result is an ack, never
			// permission to settle another run or overwrite an explicit stop.
			if current.State == request.State && current.Outcome == request.Outcome &&
				current.ContextCommitID == request.ContextCommitID {
				return current, nil
			}
			return collaboration.Run{}, err
		}
		if request.SettlementEpoch != 0 {
			return collaboration.Run{}, err
		}
		request.ExpectedVersion = current.Version
	}
	return collaboration.Run{}, collaboration.ErrStale
}

type terminalSettlementIntent struct {
	SessionID  string
	AgentID    string
	OwnerID    string
	Generation uint64
	TurnState  string
	Run        collaboration.FinishRunRequest
	Child      *agent.SubAgentResult
	Stopped    bool
}

func (run *recoveryRunState) captureTerminalSettlement(intent terminalSettlementIntent) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.terminalSettlement) != 0 {
		var prior terminalSettlementIntent
		if err := json.Unmarshal(run.terminalSettlement, &prior); err != nil {
			return err
		}
		if prior.AgentID != intent.AgentID || prior.Generation != intent.Generation {
			return errors.New("terminal settlement identity changed")
		}
		if !intent.Stopped || prior.Stopped {
			return nil
		}
		prior.Stopped = true
		intent = prior
	}
	encoded, err := json.Marshal(intent)
	if err == nil {
		run.terminalSettlement = encoded
	}
	return err
}

func terminalIntent(checkpoint sessiondb.RecoveryCheckpoint) (*terminalSettlementIntent, error) {
	if len(checkpoint.TerminalSettlement) == 0 {
		return nil, nil
	}
	var intent terminalSettlementIntent
	if err := json.Unmarshal(checkpoint.TerminalSettlement, &intent); err != nil {
		return nil, err
	}
	if intent.AgentID != checkpoint.Status.AgentID || intent.Generation != checkpoint.Status.Generation || intent.SessionID == "" || intent.OwnerID == "" || intent.Generation == 0 {
		return nil, errors.New("terminal settlement has a foreign owner or generation")
	}
	if intent.Child != nil && (string(intent.Child.AgentID) != intent.AgentID || intent.Child.SessionID != intent.SessionID || intent.OwnerID != intent.AgentID) {
		return nil, errors.New("terminal child result has a foreign identity")
	}
	if intent.Child == nil && intent.OwnerID != intent.SessionID {
		return nil, errors.New("terminal root context has a foreign owner")
	}
	if intent.Run.RunID != "" && (intent.Run.AgentID != intent.AgentID || intent.Run.RuntimeEpoch == 0 || intent.Run.ExpectedVersion == 0 || !intent.Run.State.Terminal()) {
		return nil, errors.New("terminal run identity/version is invalid")
	}
	return &intent, nil
}

func (s *Session) retryTerminalSettlement(checkpoint sessiondb.RecoveryCheckpoint) bool {
	intent, err := terminalIntent(checkpoint)
	if err != nil || intent == nil || atomic.LoadInt32(&s.closing) != 0 {
		return false
	}
	db := s.db
	if intent.Child != nil {
		path := s.completedSubAgentDBPath(intent.Child.SessionID)
		if info, statErr := os.Lstat(path); statErr != nil || !info.Mode().IsRegular() {
			return false
		}
		db, err = sessiondb.OpenAndMigrate(path)
		if err != nil {
			return false
		}
	}
	current, found, err := sessiondb.LoadContextState(db, intent.SessionID, intent.OwnerID)
	if err != nil || !found || current.Revision < checkpoint.ContextRevision || current.Generation != checkpoint.ContextGeneration || !retainsContextSource(current.Messages, checkpoint.ActiveContext) {
		if db != s.db {
			_ = db.Close()
		}
		return false
	}
	checkpoint.ContextOwnerID, checkpoint.ContextRevision = intent.OwnerID, current.Revision
	checkpoint.ContextGeneration, checkpoint.ContextReason, checkpoint.ActiveContext = current.Generation, current.Reason, current.Messages
	run := newRecoveryRunState(checkpoint.PendingInput, &checkpoint)
	name, parentID, category := s.agentName, "", ""
	var handle *agent.SubAgentHandle
	if intent.Child != nil {
		var owner childRecoveryOwner
		if err = json.Unmarshal(checkpoint.OwnerData, &owner); err != nil {
			_ = db.Close()
			return false
		} else {
			if string(owner.Spec.ID) != intent.AgentID {
				if db != s.db {
					_ = db.Close()
				}
				return false
			}
			name, parentID, category = owner.Spec.Name, string(owner.Spec.ParentID), owner.Spec.Category
			handle = &agent.SubAgentHandle{Spec: owner.Spec, Agent: &agent.Agent{TurnGeneration: intent.Generation}, DB: db, SessionID: intent.SessionID, RuntimeEpoch: intent.Run.RuntimeEpoch, ResultCh: make(chan agent.SubAgentResult, 1), StartedAt: checkpoint.Status.StartedAt}
			handle.SetRunIdentity(intent.Run.RunID, intent.Run.ExpectedVersion)
		}
	}
	registered, err := s.registerCollaborator(context.Background(), intent.AgentID, name, intent.SessionID, parentID, category, db)
	if err != nil {
		if db != s.db {
			_ = db.Close()
		}
		return false
	}
	var sender EventSender
	var orchestrator *Orchestrator
	if intent.Child != nil {
		sender = s.SubAgentRunner().sender
	} else {
		s.orchMu.Lock()
		orchestrator = s.orchestrators[intent.AgentID]
		s.orchMu.Unlock()
		if orchestrator == nil {
			return false
		}
		// NewOrchestrator publishes this owner before starting its run loop.
		// A retry immediately after passive resume cannot depend on that loop
		// having asynchronously installed the session-wide sender already.
		sender = orchestrator.sender
	}
	supervisor, err := s.newRecoverySupervisor(db, intent.SessionID, intent.AgentID, intent.Generation, run, &checkpoint.Status, sender)
	if err != nil {
		if db != s.db {
			_ = db.Close()
		}
		return false
	}
	if intent.Child != nil {
		persist, discard := supervisor.Persist, supervisor.Discard
		supervisor.Persist = func(status recovery.Status) error {
			if err := persist(status); err != nil {
				return err
			}
			mirror := run.checkpoint(status)
			mirror.ContextOwnerID = ""
			return sessiondb.SaveRecoveryCheckpoint(s.db, s.sessionID, mirror)
		}
		supervisor.Discard = func(id string) error {
			if err := discard(id); err != nil {
				return err
			}
			return sessiondb.DeleteRecoveryCheckpoint(s.db, id)
		}
	}
	ctx := context.Background()
	var stop <-chan struct{}
	if intent.Child != nil {
		ctx = s.SubAgentRunner().sessionCtx
		handle.Cancel = func() {}
		if err := s.SubAgentRunner().mgr.Register(handle); err != nil {
			_ = db.Close()
			s.finishRecovery(intent.AgentID, checkpoint.Status.ID, intent.Generation)
			return false
		}
		s.SubAgentRunner().wg.Add(1)
	} else {
		stop = orchestrator.stopCh
		s.orchWg.Add(1)
	}
	go func() {
		if intent.Child != nil {
			defer s.SubAgentRunner().wg.Done()
		} else {
			defer s.orchWg.Done()
		}
		if db != s.db {
			defer func() { _ = db.Close() }()
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		if stop != nil {
			go func() {
				select {
				case <-stop:
					cancel()
				case <-ctx.Done():
				}
			}()
		}
		settle := func() error {
			if intent.Stopped {
				return supervisor.Cancel()
			}
			return supervisor.Resolve()
		}
		for {
			var err error
			if intent.Child != nil {
				err = s.SubAgentRunner().finalizeSubAgent(handle, *intent.Child, settle, registered.RuntimeEpoch)
			} else {
				err = sessiondb.FinishAgentTurn(db, intent.SessionID, intent.AgentID, intent.Generation, intent.TurnState)
				if err == nil && intent.Run.RunID != "" {
					request := intent.Run
					request.SettlementEpoch = registered.RuntimeEpoch
					_, err = finishCollaborationRun(s.CollaborationStore(), request)
				}
				if err == nil {
					err = settle()
				}
			}
			if err == nil {
				s.finishRecovery(intent.AgentID, checkpoint.Status.ID, intent.Generation)
				if sender != nil {
					sender.SendAgentEventWithID("done", map[string]any{"reason": "terminal_settled"}, intent.AgentID)
				}
				return
			}
			if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
				status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, fmt.Errorf("terminal publication: %w", err)))
				pending := run.checkpoint(status)
				if intent.Child != nil {
					pending.ContextOwnerID = ""
				}
				s.parkRecovery(pending)
				return
			}
		}
	}()
	return true
}
