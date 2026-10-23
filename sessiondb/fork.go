package sessiondb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// ForkIdentity is the new session's own identity; everything else is copied.
type ForkIdentity struct {
	SessionID string
	AgentName string
	CreatedAt int64
}

// forkDroppedTables hold state owned by the source's live runtime, not by its
// conversation: an unfinished run's recovery, deliveries admitted to the
// source's agent, its file claims, the results of its own children and the
// usage it was billed for. A fork that kept them would resume someone else's
// work or fail to bind children whose parent is the source.
var forkDroppedTables = []string{
	"agent_recoveries", "collaboration_admissions", "file_claims", "sub_agent_results", "usage_requests",
}

// forkDroppedRuntimeKeys are per-session runtime records of the same kind:
// input queued for the source's agent and its unsent composer draft.
var forkDroppedRuntimeKeys = []string{"queued_inputs", "composer_draft"}

// Fork writes a consistent copy of the live session database src to path
// under identity. The copy keeps the transcript, the model context, plans,
// settings, references and the selected execution; Fork never changes src.
// A failed fork leaves no file behind.
func Fork(src *sql.DB, path string, identity ForkIdentity) (err error) {
	if identity.SessionID == "" || identity.AgentName == "" {
		return errors.New("sessiondb: fork requires a session identity and agent name")
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		return fmt.Errorf("sessiondb: fork target %q already exists", path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("sessiondb: fork target: %w", statErr)
	}
	// VACUUM INTO reads one snapshot, including committed WAL content, so the
	// copy is consistent while the source stays open.
	if _, err := src.Exec(`VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("sessiondb: copy for fork: %w", err)
	}
	defer func() {
		if err != nil {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(path + suffix)
			}
		}
	}()
	db, err := Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("sessiondb: close fork: %w", closeErr)
		}
	}()
	return rewriteForkIdentity(db, identity)
}

func rewriteForkIdentity(db *sql.DB, identity ForkIdentity) error {
	var source string
	if err := db.QueryRow(`SELECT id FROM sessions`).Scan(&source); err != nil {
		return fmt.Errorf("sessiondb: fork source identity: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sessiondb: begin fork: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Identity columns reference each other; check the keys once, at commit.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("sessiondb: fork foreign keys: %w", err)
	}
	for _, table := range forkDroppedTables {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("sessiondb: fork drop %s: %w", table, err)
		}
	}
	for _, key := range forkDroppedRuntimeKeys {
		if _, err := tx.Exec(`DELETE FROM session_runtime_state WHERE key = ?`, key); err != nil {
			return fmt.Errorf("sessiondb: fork drop %s: %w", key, err)
		}
	}
	// A fork keeps the goal but never starts working on it by itself, and the
	// source's verifier does not report to the fork.
	if _, err := tx.Exec(`UPDATE session_goal SET waiting = 1, verifier_id = '' WHERE status = 'active'`); err != nil {
		return fmt.Errorf("sessiondb: fork goal: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET id = ?, agent_name = ?, created_at = ?, closed_at = NULL WHERE id = ?`,
		identity.SessionID, identity.AgentName, identity.CreatedAt, source); err != nil {
		return fmt.Errorf("sessiondb: fork session row: %w", err)
	}
	for column, from := range map[string]string{"session_id": source, "agent_id": source} {
		tables, err := tablesWithColumn(tx, column)
		if err != nil {
			return err
		}
		for _, table := range tables {
			// The root agent's context is keyed by its session's identity.
			if _, err := tx.Exec(`UPDATE `+table+` SET `+column+` = ? WHERE `+column+` = ?`, identity.SessionID, from); err != nil {
				return fmt.Errorf("sessiondb: fork %s.%s: %w", table, column, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessiondb: commit fork: %w", err)
	}
	return nil
}

func tablesWithColumn(tx *sql.Tx, column string) ([]string, error) {
	rows, err := tx.Query(`SELECT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND p.name = ? ORDER BY m.name`, column)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: fork schema: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}
