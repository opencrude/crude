package sessiondb

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"crude/types/control"
	"crude/types/handover"
)

// GoalHandoverRecord is private storage, never operation output. The complete
// control preimage and terminal receipt fence the wait owned by this permit.
type GoalHandoverRecord struct {
	Reservation                                handover.Reservation
	State                                      handover.State
	GoalControl                                string
	SettingsFingerprint                        string
	SourceTarget                               control.Target
	EmptyTurns, FailingTurns, VerifierFailures int
	Findings                                   string
}

var ErrHandoverConflict = errors.New("goal handover changed; inspect the original operation")

// GoalControl excludes accounting and publication timestamps. Those saves must
// not revoke a permit, unlike changes to objective, budget, status or its owner.
func GoalControl(record GoalRecord) string {
	raw, _ := json.Marshal(struct {
		ID, Objective, Status, Reason, Verifier string
		Budget                                  int64
		Waiting                                 bool
	}{record.Goal.ID, record.Goal.Objective, string(record.Goal.Status), record.Goal.Reason, record.VerifierID,
		record.Goal.TokenBudget, record.Goal.Waiting})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func LoadGoalHandover(db *sql.DB, sessionID string) (GoalHandoverRecord, bool, error) {
	if db == nil {
		return GoalHandoverRecord{}, false, nil
	}
	var raw []byte
	var epoch uint64
	err := db.QueryRow(`SELECT lifecycle_epoch, record FROM session_goal_handover WHERE session_id = ?`, sessionID).Scan(&epoch, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return GoalHandoverRecord{}, false, nil
	}
	if err != nil {
		return GoalHandoverRecord{}, false, fmt.Errorf("load goal handover: %w", err)
	}
	var record GoalHandoverRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, false, fmt.Errorf("decode goal handover: %w", err)
	}
	if record.State.LifecycleEpoch != epoch {
		return record, false, ErrHandoverConflict
	}
	return record, true, nil
}

// CommitGoalHandover commits the sidecar and optional goal together. expected=0
// creates the first epoch. All subsequent writes CAS the durable epoch. When
// expectedGoal is nonempty, also recheck the goal's exact control preimage inside
// the transaction; accounting-only saves do not interfere.
func CommitGoalHandover(db *sql.DB, sessionID string, expected uint64, expectedGoal string, next GoalHandoverRecord, goal *GoalRecord) error {
	if db == nil {
		return errors.New("goal handover requires durable session storage")
	}
	if next.State.LifecycleEpoch != expected+1 {
		return ErrHandoverConflict
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Make the CAS the transaction's first statement: concurrent consumers
	// serialize as writers rather than upgrading competing read snapshots.
	if expected == 0 {
		result, err := tx.Exec(`INSERT INTO session_goal_handover(session_id,lifecycle_epoch,record) VALUES (?,?,?)
			ON CONFLICT(session_id) DO NOTHING`, sessionID, next.State.LifecycleEpoch, raw)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrHandoverConflict
		}
	} else {
		result, err := tx.Exec(`UPDATE session_goal_handover SET lifecycle_epoch=?,record=? WHERE session_id=? AND lifecycle_epoch=?`,
			next.State.LifecycleEpoch, raw, sessionID, expected)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrHandoverConflict
		}
	}
	if expectedGoal != "" {
		current, ok, err := loadGoal(tx, sessionID)
		if err != nil {
			return err
		}
		if !ok || GoalControl(current) != expectedGoal {
			return ErrHandoverConflict
		}
	}
	if goal != nil {
		if err := saveGoal(tx, sessionID, *goal); err != nil {
			return err
		}
	}
	return tx.Commit()
}
