package collaboration

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// validateAdmissionReceipt checks the local, durable context-commit identity;
// a matching delivery is checked separately inside the takeover transaction.
func validateAdmissionReceipt(receipt AdmissionReceipt) error {
	for _, field := range []struct {
		name, value string
		limit       int
	}{
		{"delivery_id", receipt.DeliveryID, 256},
		{"event_id", receipt.EventID, 256},
		{"context_commit_id", receipt.ContextCommitID, 512},
	} {
		if err := validateIdentity(field.name, field.value, field.limit); err != nil {
			return err
		}
	}
	if receipt.RuntimeEpoch == 0 || receipt.DeliveryVersion == 0 {
		return fmt.Errorf("%w: receipt epoch/version", ErrInvalid)
	}
	return nil
}

// recoverRuntime runs only after acquisition of the exclusive process lock.
// Nothing from the old runtime is executed again: only uncommitted reservations
// become pending and nonterminal runs become terminal records.
func (store *Store) recoverRuntime(
	ctx context.Context, tx *sql.Tx, agent Agent, receipts []AdmissionReceipt, pendingTerminal *FinishRunRequest, now time.Time,
) error {
	type orphanDelivery struct {
		id, eventID, state, contextCommitID string
		version, epoch                      uint64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,event_id,state,version,reserved_epoch,context_commit_id
		FROM collaboration_deliveries WHERE recipient_id=? AND state='admitting'`, agent.ID)
	if err != nil {
		return err
	}
	deliveries := make(map[string]orphanDelivery)
	for rows.Next() {
		var delivery orphanDelivery
		if err := rows.Scan(&delivery.id, &delivery.eventID, &delivery.state,
			&delivery.version, &delivery.epoch, &delivery.contextCommitID); err != nil {
			_ = rows.Close()
			return err
		}
		deliveries[delivery.id] = delivery
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	committed := make(map[string]AdmissionReceipt, len(receipts))
	for _, receipt := range receipts {
		delivery, ok := deliveries[receipt.DeliveryID]
		if !ok {
			// A receipt whose workspace commit succeeded before the process
			// stopped needs one exact lookup, not a scan of admitted history.
			current, err := loadDelivery(ctx, tx, receipt.DeliveryID)
			if err != nil || current.RecipientID != agent.ID {
				return fmt.Errorf("%w: local admission receipt has no owned delivery", ErrConflict)
			}
			delivery = orphanDelivery{
				id: current.ID, eventID: current.EventID, state: string(current.State),
				contextCommitID: current.ContextCommitID, version: current.Version, epoch: current.ReservedEpoch,
			}
		}
		if delivery.eventID != receipt.EventID ||
			delivery.epoch != receipt.RuntimeEpoch ||
			(delivery.state == string(DeliveryAdmitting) && receipt.RuntimeEpoch != agent.RuntimeEpoch) {
			return fmt.Errorf("%w: local admission receipt identity mismatch", ErrConflict)
		}
		if previous, duplicate := committed[receipt.DeliveryID]; duplicate {
			if previous != receipt {
				return fmt.Errorf("%w: conflicting local admission receipts", ErrConflict)
			}
			continue
		}
		switch delivery.state {
		case string(DeliveryAdmitting):
			if delivery.version != receipt.DeliveryVersion || delivery.contextCommitID != "" {
				return fmt.Errorf("%w: admission receipt version mismatch", ErrConflict)
			}
		case string(DeliveryAdmitted):
			if delivery.version != receipt.DeliveryVersion+1 ||
				delivery.contextCommitID != receipt.ContextCommitID {
				return fmt.Errorf("%w: admitted receipt mismatch", ErrConflict)
			}
		default:
			return ErrConflict
		}
		committed[receipt.DeliveryID] = receipt
	}
	for _, delivery := range deliveries {
		if delivery.state != string(DeliveryAdmitting) {
			continue
		}
		if delivery.epoch != agent.RuntimeEpoch {
			return fmt.Errorf("%w: orphaned admission belongs to another epoch", ErrConflict)
		}
		if receipt, ok := committed[delivery.id]; ok {
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries
				SET state='admitted',version=version+1,context_commit_id=?,updated_at=?,admitted_at=?
				WHERE id=? AND recipient_id=? AND state='admitting' AND reserved_epoch=? AND version=?`,
				receipt.ContextCommitID, now.UnixNano(), now.UnixNano(), delivery.id,
				agent.ID, delivery.epoch, delivery.version); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_deliveries
			SET state='pending',version=version+1,reserved_epoch=0,updated_at=?
			WHERE id=? AND recipient_id=? AND state='admitting' AND reserved_epoch=? AND version=?`,
			now.UnixNano(), delivery.id, agent.ID, delivery.epoch, delivery.version); err != nil {
			return err
		}
	}

	runRows, err := tx.QueryContext(ctx, `SELECT id FROM collaboration_runs
		WHERE agent_id=? AND runtime_epoch=? AND state NOT IN ('succeeded','failed','cancelled','interrupted')
		ORDER BY started_at,id`, agent.ID, agent.RuntimeEpoch)
	if err != nil {
		return err
	}
	var ids []string
	for runRows.Next() {
		var id string
		if err := runRows.Scan(&id); err != nil {
			_ = runRows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := runRows.Err(); err != nil {
		_ = runRows.Close()
		return err
	}
	if err := runRows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		run, err := loadRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if pendingTerminal != nil && pendingTerminal.RunID == run.ID {
			if pendingTerminal.AgentID != agent.ID || pendingTerminal.RuntimeEpoch != run.RuntimeEpoch || pendingTerminal.ExpectedVersion != run.Version || !pendingTerminal.State.Terminal() {
				return fmt.Errorf("%w: captured terminal owner/version changed", ErrConflict)
			}
			// The local checkpoint proves execution finished. Preserve this exact
			// run for explicit storage-only settlement, not an orphaned replay.
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_claims SET version=version+1,released_at=? WHERE run_id=? AND agent_id=? AND released_at=0`, now.UnixNano(), run.ID, agent.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_runs
			SET state='interrupted',version=version+1,outcome='runtime interrupted',updated_at=?,ended_at=?
			WHERE id=? AND agent_id=? AND runtime_epoch=? AND version=?`,
			now.UnixNano(), now.UnixNano(), run.ID, agent.ID, agent.RuntimeEpoch, run.Version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_claims
			SET version=version+1,released_at=?
			WHERE run_id=? AND agent_id=? AND released_at=0`, now.UnixNano(), run.ID, agent.ID); err != nil {
			return err
		}
		if agent.ParentID != "" {
			_, err := store.publishTx(ctx, tx, PublishRequest{
				SenderID: agent.ID, SenderName: agent.Name, IdempotencyKey: "run-outcome:" + run.ID,
				RecipientIDs: []string{agent.ParentID}, Origin: OriginRuntime,
				Kind: EventRunOutcome, Body: fmt.Sprintf("%s · interrupted", agent.Name),
				RunID: run.ID, RequestID: run.RequestID,
			})
			if err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE collaboration_agents
		SET current_run_id='',current_request_id='' WHERE id=?`, agent.ID)
	return err
}
