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
      content_type      TEXT NOT NULL DEFAULT 'text',
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

CREATE TABLE IF NOT EXISTS sub_agent_results (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_session_id TEXT NOT NULL,
    child_session_id  TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    agent_name        TEXT NOT NULL,
    agent_category    TEXT NOT NULL,
    status            TEXT NOT NULL,
    summary_gist      TEXT,
    result_path       TEXT,
    session_log_path  TEXT,
    files_read        TEXT,
    files_written     TEXT,
    errors            TEXT,
    prompt_tokens     INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cost_usd          REAL DEFAULT 0.0,
    duration_ms       INTEGER DEFAULT 0,
    created_at        INTEGER NOT NULL,
    FOREIGN KEY (parent_session_id) REFERENCES sessions(id),
    FOREIGN KEY (child_session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_sub_results_parent ON sub_agent_results(parent_session_id);
CREATE INDEX IF NOT EXISTS idx_sub_results_child ON sub_agent_results(child_session_id);

CREATE TABLE IF NOT EXISTS file_claims (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    agent_name      TEXT NOT NULL,
    path            TEXT NOT NULL,
    claimed_at      INTEGER NOT NULL,
    released_at     INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_file_claims_path ON file_claims(path);
CREATE INDEX IF NOT EXISTS idx_file_claims_session ON file_claims(session_id);

CREATE TABLE IF NOT EXISTS prompt_references (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    kind        TEXT NOT NULL,
    label       TEXT NOT NULL,
    title       TEXT,
    mime        TEXT,
    content     TEXT,
    file_path   TEXT,
    inline      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    deleted_at  INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_prompt_refs_session ON prompt_references(session_id, deleted_at, created_at);
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
	migrations := []string{
		"ALTER TABLE messages ADD COLUMN display_meta TEXT",
		"ALTER TABLE messages ADD COLUMN content_type TEXT NOT NULL DEFAULT 'text'",
		"ALTER TABLE sessions ADD COLUMN parent_session_id TEXT",
		"ALTER TABLE sessions ADD COLUMN agent_category TEXT",
		"ALTER TABLE sessions ADD COLUMN agent_name TEXT",
		"ALTER TABLE sessions ADD COLUMN depth INTEGER DEFAULT 0",
	}
	for _, m := range migrations {
		db.Exec(m)
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
