package sessiondb

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    project_path    TEXT NOT NULL,
    project_name    TEXT NOT NULL,
    model           TEXT,
    provider        TEXT,
    created_at      INTEGER NOT NULL,
    closed_at       INTEGER,
    message_count   INTEGER DEFAULT 0,
    turn_count      INTEGER DEFAULT 0,
    first_prompt    TEXT,
    last_prompt     TEXT,
    summary         TEXT
);

CREATE TABLE IF NOT EXISTS messages (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    role              TEXT NOT NULL,
    content           TEXT,
    reasoning_content TEXT,
    reasoning         TEXT,
    tool_calls        TEXT,
    tool_name         TEXT,
    tool_result       TEXT,
    tool_call_id      TEXT,
    cache_control     TEXT,
    token_count       INTEGER DEFAULT 0,
    sequence_num      INTEGER NOT NULL,
    turn_number       INTEGER NOT NULL,
    agent_id          TEXT,
    created_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_seq ON messages(sequence_num);
CREATE INDEX IF NOT EXISTS idx_messages_turn ON messages(turn_number, sequence_num);

CREATE TABLE IF NOT EXISTS cost_snapshots (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    total_prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    total_completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_cached_tokens     INTEGER NOT NULL DEFAULT 0,
    total_cost              REAL NOT NULL DEFAULT 0.0,
    by_model                TEXT,
    snapshot_at             INTEGER NOT NULL
);
`

func Open(dbPath string) (*sql.DB, error) {
	dsn := dbPath + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("sessiondb: migrate: %w", err)
	}
	return nil
}

func OpenAndMigrate(dbPath string) (*sql.DB, error) {
	db, err := Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
