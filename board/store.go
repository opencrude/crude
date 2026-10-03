package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Store struct {
	db        *sql.DB
	projectID string
}

const schema = `
CREATE TABLE IF NOT EXISTS board_workspace (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    project_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS boards (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    coordinator_id TEXT NOT NULL,
    last_sequence INTEGER NOT NULL DEFAULT 0,
    human_read_sequence INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS board_members (
    board_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    name TEXT NOT NULL,
    parent_id TEXT NOT NULL,
    availability TEXT NOT NULL CHECK (availability IN ('active', 'finished', 'unknown')),
    accepted_sequence INTEGER NOT NULL DEFAULT 0,
    notice_sequence INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (board_id, agent_id),
    FOREIGN KEY (board_id) REFERENCES boards(id)
);
CREATE TABLE IF NOT EXISTS board_messages (
    id TEXT PRIMARY KEY,
    board_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    sender_id TEXT NOT NULL,
    provider_message_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    recipient_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    body TEXT NOT NULL,
    message_json TEXT NOT NULL,
    payload_bytes INTEGER NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    UNIQUE (board_id, sequence),
    UNIQUE (project_id, sender_id, provider_message_id, tool_call_id),
    FOREIGN KEY (board_id, sender_id) REFERENCES board_members(board_id, agent_id)
);
CREATE INDEX IF NOT EXISTS idx_board_messages_thread
    ON board_messages(board_id, thread_id, sequence);
CREATE INDEX IF NOT EXISTS idx_board_messages_addressed
    ON board_messages(board_id, recipient_id, sequence);
CREATE INDEX IF NOT EXISTS idx_board_messages_active
    ON board_messages(board_id, archived, sequence);
CREATE TABLE IF NOT EXISTS board_read_offers (
    board_id TEXT NOT NULL,
    reader_id TEXT NOT NULL,
    since_sequence INTEGER NOT NULL,
    through_sequence INTEGER NOT NULL,
    PRIMARY KEY (board_id, reader_id, through_sequence),
    FOREIGN KEY (board_id) REFERENCES boards(id)
);
`

func Attach(db *sql.DB, projectID string) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalid)
	}
	if err := validateIdentity("project_id", projectID, 256); err != nil {
		return nil, err
	}
	store := &Store{db: db, projectID: projectID}
	err := transact(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(schema); err != nil {
			return fmt.Errorf("board: migrate: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO board_workspace(singleton, project_id) VALUES (1, ?) ON CONFLICT(singleton) DO NOTHING`, projectID); err != nil {
			return err
		}
		var attachedProject string
		if err := tx.QueryRow(`SELECT project_id FROM board_workspace WHERE singleton = 1`).Scan(&attachedProject); err != nil {
			return err
		}
		if attachedProject != projectID {
			return ErrProjectMismatch
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}

func transact(ctx context.Context, db *sql.DB, operation func(*sql.Tx) error) error {
	deadline := time.Now().Add(5 * time.Second)
	delay := 2 * time.Millisecond
	for {
		err := func() error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			if err := operation(tx); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil || !sqliteBusy(err) || time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 25*time.Millisecond {
			delay *= 2
		}
	}
}

func sqliteBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && (coded.Code()&255 == 5 || coded.Code()&255 == 6)
}

func (store *Store) transaction(ctx context.Context, write bool, operation func(*sql.Tx) error) error {
	return transact(ctx, store.db, func(tx *sql.Tx) error {
		if write {
			if _, err := tx.ExecContext(ctx, `UPDATE board_workspace SET project_id = project_id WHERE singleton = 1`); err != nil {
				return err
			}
		}
		return operation(tx)
	})
}

type boardState struct {
	coordinatorID string
	lastSequence  int64
	humanRead     int64
}

func (store *Store) state(ctx context.Context, tx *sql.Tx, boardID, projectID string) (boardState, error) {
	if projectID != store.projectID {
		return boardState{}, ErrProjectMismatch
	}
	var state boardState
	var boardProject string
	err := tx.QueryRowContext(ctx, `SELECT project_id, coordinator_id, last_sequence, human_read_sequence FROM boards WHERE id = ?`, boardID).
		Scan(&boardProject, &state.coordinatorID, &state.lastSequence, &state.humanRead)
	if errors.Is(err, sql.ErrNoRows) {
		return boardState{}, ErrNotFound
	}
	if err != nil {
		return boardState{}, err
	}
	if boardProject != projectID {
		return boardState{}, ErrProjectMismatch
	}
	return state, nil
}

func findMember(ctx context.Context, tx *sql.Tx, boardID, agentID string) (Member, int64, error) {
	var member Member
	var accepted int64
	err := tx.QueryRowContext(ctx, `SELECT agent_id, name, parent_id, availability, accepted_sequence FROM board_members WHERE board_id = ? AND agent_id = ?`, boardID, agentID).
		Scan(&member.ID, &member.Name, &member.ParentID, &member.Availability, &accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, 0, ErrNotMember
	}
	return member, accepted, err
}

func roster(ctx context.Context, tx *sql.Tx, boardID string) ([]Member, error) {
	rows, err := tx.QueryContext(ctx, `SELECT agent_id, name, parent_id, availability FROM board_members WHERE board_id = ? ORDER BY agent_id`, boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	members := make([]Member, 0)
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.ID, &member.Name, &member.ParentID, &member.Availability); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (store *Store) Create(ctx context.Context, id, coordinatorID string) error {
	if err := validateIdentity("board_id", id, 256); err != nil {
		return err
	}
	if coordinatorID != "" {
		if err := validateMemberID(coordinatorID); err != nil {
			return err
		}
	}
	return store.transaction(ctx, true, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO boards(id, project_id, coordinator_id, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
			id, store.projectID, coordinatorID, time.Now().UTC().UnixNano()); err != nil {
			return err
		}
		state, err := store.state(ctx, tx, id, store.projectID)
		if err != nil {
			return err
		}
		if state.coordinatorID != coordinatorID {
			return ErrConflict
		}
		return nil
	})
}

func (store *Store) Join(ctx context.Context, boardID string, member Member) error {
	if err := validateMemberID(member.ID); err != nil {
		return err
	}
	if member.Name == "" {
		member.Name = member.ID
	}
	if err := validateIdentity("name", member.Name, 128); err != nil {
		return err
	}
	if member.Availability == "" {
		member.Availability = AvailabilityUnknown
	}
	switch member.Availability {
	case AvailabilityActive, AvailabilityFinished, AvailabilityUnknown:
	default:
		return fmt.Errorf("%w: availability must be active, finished, or unknown", ErrInvalid)
	}
	return store.transaction(ctx, true, func(tx *sql.Tx) error {
		state, err := store.state(ctx, tx, boardID, store.projectID)
		if err != nil {
			return err
		}
		if member.ParentID != "" {
			if member.ParentID == member.ID || member.ID == state.coordinatorID {
				return fmt.Errorf("%w: invalid inherited membership", ErrInvalid)
			}
			if _, _, err := findMember(ctx, tx, boardID, member.ParentID); err != nil {
				return fmt.Errorf("board: inherited parent: %w", err)
			}
		}
		existing, _, err := findMember(ctx, tx, boardID, member.ID)
		if err != nil && !errors.Is(err, ErrNotMember) {
			return err
		}
		if err == nil && existing.ParentID != member.ParentID {
			return fmt.Errorf("%w: parent membership is immutable", ErrConflict)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO board_members(board_id, agent_id, name, parent_id, availability) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(board_id, agent_id) DO UPDATE SET name = excluded.name, availability = excluded.availability`,
			boardID, member.ID, member.Name, member.ParentID, member.Availability); err != nil {
			return err
		}
		members, err := roster(ctx, tx, boardID)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(members)
		if err != nil {
			return err
		}
		if len(members) > MaxMembers || len(encoded) > MaxRosterBytes {
			return ErrTooLarge
		}
		return nil
	})
}

func (store *Store) Roster(ctx context.Context, boardID string) ([]Member, error) {
	var members []Member
	err := store.transaction(ctx, false, func(tx *sql.Tx) error {
		if _, err := store.state(ctx, tx, boardID, store.projectID); err != nil {
			return err
		}
		var err error
		members, err = roster(ctx, tx, boardID)
		return err
	})
	return members, err
}

func (store *Store) Archive(ctx context.Context, boardID, projectID, threadID string) error {
	return store.transaction(ctx, true, func(tx *sql.Tx) error {
		if _, err := store.state(ctx, tx, boardID, projectID); err != nil {
			return err
		}
		message, err := messageByID(ctx, tx, boardID, threadID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE board_messages SET archived = 1 WHERE board_id = ? AND thread_id = ?`, boardID, message.ThreadID)
		return err
	})
}

func archiveOverflow(ctx context.Context, tx *sql.Tx, boardID string) error {
	for {
		var count, size int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(payload_bytes), 0) FROM board_messages WHERE board_id = ? AND archived = 0`, boardID).Scan(&count, &size); err != nil {
			return err
		}
		if count <= MaxActiveMessages && size <= MaxActiveBytes {
			return nil
		}
		var oldestThread string
		if err := tx.QueryRowContext(ctx, `SELECT thread_id FROM board_messages WHERE board_id = ? AND archived = 0 GROUP BY thread_id ORDER BY MAX(sequence), MIN(sequence) LIMIT 1`, boardID).Scan(&oldestThread); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE board_messages SET archived = 1 WHERE board_id = ? AND thread_id = ?`, boardID, oldestThread); err != nil {
			return err
		}
	}
}

func (store *Store) HumanUnread(ctx context.Context, boardID string) (int, error) {
	var count int
	err := store.transaction(ctx, false, func(tx *sql.Tx) error {
		state, err := store.state(ctx, tx, boardID, store.projectID)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM board_messages WHERE board_id = ? AND sequence > ?`, boardID, state.humanRead).Scan(&count)
	})
	return count, err
}

func validateMemberID(id string) error {
	if id == "ALL" || id == "main" {
		return fmt.Errorf("%w: reserved member identity", ErrInvalid)
	}
	return validateIdentity("agent_id", id, 128)
}

func validateIdentity(field, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: invalid %s", ErrInvalid, field)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%w: %s", ErrTooLarge, field)
	}
	return nil
}
