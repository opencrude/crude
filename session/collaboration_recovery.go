package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
)

func collaborationAdmissionReceipts(db *sql.DB, sessionID, agentID string) ([]sessiondb.CollaborationAdmission, []collaboration.AdmissionReceipt, error) {
	pending, err := sessiondb.PendingCollaborationAdmissions(db, agentID)
	if err != nil {
		return nil, nil, fmt.Errorf("load durable collaboration receipts: %w", err)
	}
	receipts := make([]collaboration.AdmissionReceipt, 0, len(pending))
	for _, admission := range pending {
		if admission.AgentID != agentID || admission.SessionID != sessionID {
			return nil, nil, errors.New("collaboration receipt has a foreign owner")
		}
		receipts = append(receipts, collaboration.AdmissionReceipt{
			DeliveryID: admission.DeliveryID, EventID: admission.EventID,
			RuntimeEpoch: admission.RuntimeEpoch, DeliveryVersion: admission.DeliveryVersion,
			ContextCommitID: admission.ContextCommitID,
		})
	}
	return pending, receipts, nil
}

// Recovered children are review surfaces, not executable jobs. Acquiring their
// exact locks lets the workspace settle orphaned state without starting a model.
func (s *Session) recoverChildCollaborators(ctx context.Context, parentID string) error {
	runner := s.SubAgentRunner()
	store := s.CollaborationStore()
	if runner == nil || store == nil {
		return nil
	}
	for _, child := range runner.Manager().CompletedResults() {
		existing, err := store.Agent(ctx, string(child.AgentID))
		if errors.Is(err, collaboration.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if existing.ParentID != parentID {
			return errors.New("recovered child belongs to another parent")
		}
		unsettled, err := store.HasUnsettledAdmission(ctx, existing.ID)
		if err != nil {
			return err
		}
		if existing.CurrentRunID == "" && !unsettled {
			continue
		}
		path := s.completedSubAgentDBPath(child.SessionID)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("child recovery requires the existing regular evidence database %q: %v", path, err)
		}
		db, err := sessiondb.OpenAndMigrate(path)
		if err != nil {
			return fmt.Errorf("open child recovery evidence: %w", err)
		}
		pending, receipts, err := collaborationAdmissionReceipts(db, child.SessionID, string(child.AgentID))
		if err == nil {
			var registered collaboration.Agent
			registered, err = store.RegisterAgent(ctx, collaboration.AgentRegistration{
				ID: existing.ID, Name: existing.Name, SessionID: existing.SessionID,
				ParentID: existing.ParentID, Category: existing.Category, State: existing.State,
				OwnerInstance: s.collaborationInstance, Admissions: receipts,
			})
			if err == nil {
				for _, admission := range pending {
					if err = sessiondb.MarkCollaborationAdmissionPublished(db, admission.DeliveryID, admission.ContextCommitID); err != nil {
						break
					}
				}
				detachErr := store.DetachAgent(ctx, existing.ID, s.collaborationInstance, registered.RuntimeEpoch)
				if err == nil {
					err = detachErr
				}
			}
		}
		closeErr := db.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *Session) admitPassiveCollaboration(o *Orchestrator, sender EventSender) error {
	release, available := s.beginIdleWork()
	if !available {
		return errors.New("session is reserved for core shutdown")
	}
	defer release()
	if o.passiveThrough == 0 {
		return nil
	}
	store := s.CollaborationStore()
	if store == nil {
		return errors.New("passive collaboration runtime is unavailable")
	}
	for {
		deliveries, err := store.ReservePending(context.Background(), collaboration.ReserveRequest{
			RecipientID: o.ID, RuntimeEpoch: o.RuntimeEpoch, ThroughSequence: o.passiveThrough,
			Limit: 20, ExcludeKinds: []collaboration.EventKind{collaboration.EventLifecycleControl, collaboration.EventHumanSteering},
		})
		if errors.Is(err, collaboration.ErrUnavailable) {
			return err // Suspension keeps the passive boundary closed until explicit resume.
		}
		if err != nil {
			return err
		}
		if len(deliveries) == 0 {
			return nil
		}
		s.msgsMu.Lock()
		revision := s.contextRevision
		s.msgsMu.Unlock()
		// Only this admission suffix belongs here. Including current context lets
		// an audit history reference select an unrelated immutable human message.
		messages := make([]provider.ChatMessage, 0, len(deliveries))
		for _, delivery := range deliveries {
			messages = append(messages, collaborationMessage(delivery))
		}
		if _, err := s.commitCollaborationAdmissions(s.db, s.sessionID, s.sessionID, o.ID, "", o.RuntimeEpoch, revision, collaborationDeltas(deliveries, messages, o.ID), deliveries); err != nil {
			s.releaseCollaborationReservations(o.ID, o.RuntimeEpoch, deliveries, "passive resume admission failed")
			return err
		}
		for _, delivery := range deliveries {
			sender.SendAgentEventWithID("collaboration_message", delivery.Event, o.ID)
		}
	}
}
