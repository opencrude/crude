package sessiondb

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"crude/types"
)

// GoalRecord is a session's stored goal. VerifierID names the running
// verifier child; the shell view only learns that one is running.
type GoalRecord struct {
	Goal       types.Goal
	VerifierID string
}

func LoadGoal(db *sql.DB, sessionID string) (GoalRecord, bool, error) {
	if db == nil {
		return GoalRecord{}, false, nil
	}
	var record GoalRecord
	var status string
	var timeUsed, created, updated int64
	var waiting bool
	err := db.QueryRow(`SELECT goal_id, objective, status, reason, tokens_used, token_budget, time_used_ms, waiting, verifier_id, created_at, updated_at
		FROM session_goal WHERE session_id = ?`, sessionID).Scan(&record.Goal.ID, &record.Goal.Objective, &status, &record.Goal.Reason,
		&record.Goal.TokensUsed, &record.Goal.TokenBudget, &timeUsed, &waiting, &record.VerifierID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return GoalRecord{}, false, nil
	}
	if err != nil {
		return GoalRecord{}, false, fmt.Errorf("sessiondb: load goal: %w", err)
	}
	record.Goal.Status = types.GoalStatus(status)
	record.Goal.TimeUsed = time.Duration(timeUsed) * time.Millisecond
	record.Goal.Waiting = waiting
	record.Goal.Verifying = record.VerifierID != ""
	record.Goal.CreatedAt, record.Goal.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
	return record, true, nil
}

func SaveGoal(db *sql.DB, sessionID string, record GoalRecord) error {
	if db == nil {
		return nil
	}
	goal := record.Goal
	_, err := db.Exec(`INSERT INTO session_goal (session_id, goal_id, objective, status, reason, tokens_used, token_budget, time_used_ms, waiting, verifier_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET goal_id = excluded.goal_id, objective = excluded.objective, status = excluded.status,
			reason = excluded.reason, tokens_used = excluded.tokens_used, token_budget = excluded.token_budget, time_used_ms = excluded.time_used_ms,
			waiting = excluded.waiting, verifier_id = excluded.verifier_id, created_at = excluded.created_at, updated_at = excluded.updated_at`,
		sessionID, goal.ID, goal.Objective, string(goal.Status), goal.Reason, goal.TokensUsed, goal.TokenBudget, goal.TimeUsed.Milliseconds(),
		goal.Waiting, record.VerifierID, goal.CreatedAt.UnixNano(), goal.UpdatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("sessiondb: save goal: %w", err)
	}
	return nil
}

func DeleteGoal(db *sql.DB, sessionID string) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(`DELETE FROM session_goal WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("sessiondb: delete goal: %w", err)
	}
	return nil
}
