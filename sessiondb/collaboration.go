package sessiondb

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CollaborationAdmission struct {
	DeliveryID      string
	EventID         string
	SessionID       string
	AgentID         string
	RuntimeEpoch    uint64
	DeliveryVersion uint64
	ContextCommitID string
	RunID           string
	PublishedAt     int64
	AdmittedAt      int64
}

func SaveContextStateWithAdmissions(
	db *sql.DB,
	sessionID, contextOwnerID, agentID string,
	state ContextState,
	admissions []CollaborationAdmission,
) error {
	_, err := PublishContext(db, ContextPublication{SessionID: sessionID, AgentID: contextOwnerID, RuntimeAgentID: agentID,
		ExpectedRevision: state.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{Generation: state.Generation,
			Reason: state.Reason, Messages: state.Messages}}, Admissions: admissions})
	return err
}

func saveAdmissionsTx(tx *sql.Tx, sessionID, runtimeAgentID string, admissions []CollaborationAdmission) error {
	now := time.Now().UnixNano()
	for _, admission := range admissions {
		if admission.DeliveryID == "" || admission.EventID == "" || admission.ContextCommitID == "" ||
			admission.AgentID != runtimeAgentID || admission.SessionID != sessionID ||
			admission.RuntimeEpoch == 0 || admission.DeliveryVersion == 0 {
			return errors.New("sessiondb: invalid collaboration admission")
		}
		var existing CollaborationAdmission
		err := tx.QueryRow(`SELECT delivery_id,event_id,session_id,agent_id,runtime_epoch,
			delivery_version,context_commit_id,run_id,published_at,admitted_at
			FROM collaboration_admissions WHERE delivery_id=?`, admission.DeliveryID).
			Scan(&existing.DeliveryID, &existing.EventID, &existing.SessionID, &existing.AgentID,
				&existing.RuntimeEpoch, &existing.DeliveryVersion, &existing.ContextCommitID,
				&existing.RunID, &existing.PublishedAt, &existing.AdmittedAt)
		if err == nil {
			if existing.EventID != admission.EventID || existing.SessionID != admission.SessionID ||
				existing.AgentID != admission.AgentID || existing.RuntimeEpoch != admission.RuntimeEpoch ||
				existing.DeliveryVersion != admission.DeliveryVersion ||
				existing.ContextCommitID != admission.ContextCommitID || existing.RunID != admission.RunID {
				return errors.New("sessiondb: collaboration admission identity conflict")
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		admittedAt := admission.AdmittedAt
		if admittedAt == 0 {
			admittedAt = now
		}
		if _, err := tx.Exec(`INSERT INTO collaboration_admissions
			(delivery_id,event_id,session_id,agent_id,runtime_epoch,delivery_version,
			context_commit_id,run_id,published_at,admitted_at)
			VALUES(?,?,?,?,?,?,?,?,0,?)`,
			admission.DeliveryID, admission.EventID, admission.SessionID, admission.AgentID,
			admission.RuntimeEpoch, admission.DeliveryVersion, admission.ContextCommitID,
			admission.RunID, admittedAt); err != nil {
			return fmt.Errorf("sessiondb: insert collaboration admission: %w", err)
		}
	}
	return nil
}

func PendingCollaborationAdmissions(db *sql.DB, agentID string) ([]CollaborationAdmission, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT delivery_id,event_id,session_id,agent_id,runtime_epoch,
		delivery_version,context_commit_id,run_id,published_at,admitted_at
		FROM collaboration_admissions
		WHERE agent_id=? AND published_at=0 ORDER BY admitted_at,delivery_id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CollaborationAdmission
	for rows.Next() {
		var admission CollaborationAdmission
		if err := rows.Scan(&admission.DeliveryID, &admission.EventID, &admission.SessionID,
			&admission.AgentID, &admission.RuntimeEpoch, &admission.DeliveryVersion,
			&admission.ContextCommitID, &admission.RunID, &admission.PublishedAt,
			&admission.AdmittedAt); err != nil {
			return nil, err
		}
		result = append(result, admission)
	}
	return result, rows.Err()
}

func MarkCollaborationAdmissionPublished(db *sql.DB, deliveryID, contextCommitID string) error {
	if db == nil {
		return errors.New("sessiondb: nil database")
	}
	result, err := db.Exec(`UPDATE collaboration_admissions SET published_at=?
		WHERE delivery_id=? AND context_commit_id=? AND published_at=0`,
		time.Now().UnixNano(), deliveryID, contextCommitID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var publishedAt int64
		if err := db.QueryRow(`SELECT published_at FROM collaboration_admissions
			WHERE delivery_id=? AND context_commit_id=?`, deliveryID, contextCommitID).Scan(&publishedAt); err != nil {
			return err
		}
		if publishedAt == 0 {
			return errors.New("sessiondb: collaboration admission was not published")
		}
	}
	return nil
}
