package sessiondb

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type RuntimeStateUpdate struct {
	Key   string
	Value []byte
}

func saveRuntimeStateTx(tx *sql.Tx, update RuntimeStateUpdate) error {
	if update.Key == "" {
		return fmt.Errorf("sessiondb: runtime state key is required")
	}
	_, err := tx.Exec(`INSERT INTO session_runtime_state (key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, update.Key, update.Value, time.Now().UnixNano())
	return err
}

func SaveRuntimeState(ctx context.Context, db *sql.DB, key string, value []byte) error {
	if db == nil {
		return nil
	}
	if key == "" {
		return fmt.Errorf("sessiondb: runtime state key is required")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO session_runtime_state (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("sessiondb: save runtime state %q: %w", key, err)
	}
	return nil
}

func LoadRuntimeState(ctx context.Context, db *sql.DB, key string) ([]byte, error) {
	if db == nil {
		return nil, nil
	}
	var value []byte
	err := db.QueryRowContext(
		ctx, `SELECT value FROM session_runtime_state WHERE key = ?`, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sessiondb: load runtime state %q: %w", key, err)
	}
	return append([]byte(nil), value...), nil
}
