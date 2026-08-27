package sessiondb

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

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
    tool_error        INTEGER NOT NULL DEFAULT 0,
    provider_items    TEXT,
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

CREATE TABLE IF NOT EXISTS session_agent_settings (
    session_id      TEXT NOT NULL,
    category        TEXT NOT NULL,
    provider        TEXT,
    model           TEXT,
    api_standard    TEXT,
    thinking_effort TEXT,
    inherit_source  TEXT,
    PRIMARY KEY (session_id, category),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE TABLE IF NOT EXISTS session_flags (
    session_id  TEXT PRIMARY KEY,
    multi_agent INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE TABLE IF NOT EXISTS session_runtime_state (
    key        TEXT PRIMARY KEY,
    value      BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS plans (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    objective   TEXT NOT NULL,
    status      TEXT NOT NULL,
    revision    INTEGER NOT NULL DEFAULT 0,
    explanation TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    closed_at   INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_plans_session ON plans(session_id, updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_plans_one_open ON plans(session_id)
    WHERE status IN ('active','paused');

CREATE TABLE IF NOT EXISTS plan_steps (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id       TEXT NOT NULL,
    step_id       TEXT NOT NULL,
    position      INTEGER NOT NULL,
    description   TEXT NOT NULL,
    status        TEXT NOT NULL,
    depends_on_json TEXT,
    result        TEXT,
    reason        TEXT,
    attempts      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER,
    UNIQUE (plan_id, step_id),
    FOREIGN KEY (plan_id) REFERENCES plans(id)
);

CREATE INDEX IF NOT EXISTS idx_plan_steps_plan ON plan_steps(plan_id, position);

CREATE TABLE IF NOT EXISTS plan_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id      TEXT NOT NULL,
    revision     INTEGER NOT NULL,
    event_type   TEXT NOT NULL,
    payload_json TEXT,
    created_at   INTEGER NOT NULL,
    FOREIGN KEY (plan_id) REFERENCES plans(id)
);

CREATE INDEX IF NOT EXISTS idx_plan_events_plan ON plan_events(plan_id, revision);
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
		"ALTER TABLE messages ADD COLUMN tool_error INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE messages ADD COLUMN provider_items TEXT",
		"ALTER TABLE session_agent_settings ADD COLUMN api_standard TEXT",
		"ALTER TABLE session_agent_settings ADD COLUMN inherit_source TEXT",
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("sessiondb: migration %q: %w", m, err)
		}
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

// OpenAndMigrateAt opens (or creates) a session DB at an explicit directory
// path derived from a test root directory. It exists outside _test.go so other
// packages' tests can build fixture stores against a real session DB.
func OpenAndMigrateAt(dir string) *sql.DB {
	db, err := OpenAndMigrate(filepath.Join(dir, "session.db"))
	if err != nil {
		panic(fmt.Sprintf("sessiondb: OpenAndMigrateAt: %v", err))
	}
	return db
}

// CreateSessionRow inserts a minimal session row so plan tables' foreign keys
// resolve. It is provided outside _test.go for foreign-package test fixtures.
func CreateSessionRow(db *sql.DB, id string) error {
	_, err := db.Exec(
		`INSERT INTO sessions (id, project_path, project_name, created_at)
		 VALUES (?, '/', 'test', ?)`, id, time.Now().UnixNano())
	return err
}
