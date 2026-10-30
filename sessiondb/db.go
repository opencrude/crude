package sessiondb

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    project_path    TEXT NOT NULL,
    project_name    TEXT NOT NULL,
    model           TEXT,
    provider        TEXT,
    account_id      TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    closed_at       INTEGER,
    message_count   INTEGER DEFAULT 0,
    turn_count      INTEGER DEFAULT 0,
    first_prompt    TEXT,
    last_prompt     TEXT,
    summary         TEXT,
    parent_session_id TEXT,
    agent_category  TEXT,
    agent_name      TEXT,
    depth           INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS messages (
      id                INTEGER PRIMARY KEY AUTOINCREMENT,
      internal_id       TEXT,
      role              TEXT NOT NULL,
      origin            TEXT NOT NULL,
      visibility        TEXT NOT NULL,
      collaboration_json TEXT,
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
    created_at        INTEGER NOT NULL,
    display_meta      TEXT
);

CREATE INDEX IF NOT EXISTS idx_messages_seq ON messages(sequence_num);
CREATE INDEX IF NOT EXISTS idx_messages_turn ON messages(turn_number, sequence_num);
CREATE INDEX IF NOT EXISTS idx_messages_internal_id ON messages(internal_id);

CREATE TABLE IF NOT EXISTS usage_requests (
    request_id          TEXT PRIMARY KEY,
    session_id          TEXT NOT NULL,
    agent_id            TEXT NOT NULL,
    parent_agent_id     TEXT NOT NULL DEFAULT '',
    turn_generation     TEXT NOT NULL DEFAULT '0',
    attempt             INTEGER NOT NULL DEFAULT 1,
    request_kind        TEXT NOT NULL DEFAULT 'normal',
	request_outcome     TEXT NOT NULL DEFAULT '',
    provider_id         TEXT NOT NULL DEFAULT '',
    account_id          TEXT NOT NULL DEFAULT '',
    model_id            TEXT NOT NULL DEFAULT '',
    prompt_tokens       INTEGER NOT NULL DEFAULT 0,
    completion_tokens   INTEGER NOT NULL DEFAULT 0,
    cached_tokens       INTEGER NOT NULL DEFAULT 0,
    cache_read_known    INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_known   INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens    INTEGER NOT NULL DEFAULT 0,
    reasoning_known     INTEGER NOT NULL DEFAULT 0,
    reasoning_in_output INTEGER NOT NULL DEFAULT 1,
    context_used        INTEGER NOT NULL DEFAULT 0,
    context_window      INTEGER NOT NULL DEFAULT 0,
    cost_usd            REAL NOT NULL DEFAULT 0.0,
    cost_known          INTEGER NOT NULL DEFAULT 0,
    cost_source         TEXT NOT NULL DEFAULT '',
    estimated           INTEGER NOT NULL DEFAULT 0,
    partial             INTEGER NOT NULL DEFAULT 0,
    cost_upper_bound    INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_requests_agent ON usage_requests(agent_id, turn_generation, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_requests_context_baseline
    ON usage_requests(session_id, agent_id, request_kind, updated_at DESC);

CREATE TABLE IF NOT EXISTS sub_agent_results (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_session_id TEXT NOT NULL,
    child_session_id  TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    agent_name        TEXT NOT NULL,
    agent_category    TEXT NOT NULL,
    status            TEXT NOT NULL,
    summary_gist      TEXT,
    report_id         TEXT,
    report_revision   INTEGER NOT NULL DEFAULT 0,
    session_log_path  TEXT,
    files_read        TEXT,
    files_written     TEXT,
    errors            TEXT,
    prompt_tokens     INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cost_usd          REAL DEFAULT 0.0,
    duration_ms       INTEGER DEFAULT 0,
    created_at        INTEGER NOT NULL,
    FOREIGN KEY (parent_session_id) REFERENCES sessions(id)
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
    account_id      TEXT NOT NULL DEFAULT '',
    model           TEXT,
    api_standard    TEXT,
    thinking_effort TEXT,
    context_compaction_percent INTEGER,
    inherit_source  TEXT,
    capability_selection TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (session_id, category),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE TABLE IF NOT EXISTS session_flags (
    session_id  TEXT PRIMARY KEY,
    multi_agent INTEGER,
    main_agent TEXT NOT NULL DEFAULT '',
    code_intelligence INTEGER,
    max_sub_agents INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

-- session_capabilities turns installed skills/MCP servers off for one session
-- and every agent it runs. Absent rows follow the application/workspace
-- registration.
CREATE TABLE IF NOT EXISTS session_capabilities (
    session_id    TEXT NOT NULL,
    capability_id TEXT NOT NULL,
    PRIMARY KEY (session_id, capability_id),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE TABLE IF NOT EXISTS agent_context_state (
    session_id     TEXT NOT NULL,
    agent_id       TEXT NOT NULL,
    revision       INTEGER NOT NULL,
    generation     INTEGER NOT NULL DEFAULT 1,
    reason         TEXT NOT NULL DEFAULT 'session_start',
    messages_json  BLOB NOT NULL,
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (session_id, agent_id),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_context_generations (
    session_id     TEXT NOT NULL,
    agent_id       TEXT NOT NULL,
    generation     INTEGER NOT NULL,
    reason         TEXT NOT NULL,
    messages_json  BLOB NOT NULL,
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (session_id, agent_id, generation),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

-- A context's messages are its messages_json array followed by its tail rows
-- in position order; writers keep the array empty (see message_lists.go).
CREATE TABLE IF NOT EXISTS agent_context_tail (
    session_id     TEXT NOT NULL,
    agent_id       TEXT NOT NULL,
    position       INTEGER NOT NULL,
    message_json   BLOB NOT NULL,
    PRIMARY KEY (session_id, agent_id, position),
    FOREIGN KEY (session_id, agent_id) REFERENCES agent_context_state(session_id, agent_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS immutable_message_identities (
    internal_id     TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    message_json    BLOB NOT NULL,
    created_at      INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS collaboration_admissions (
    delivery_id       TEXT PRIMARY KEY,
    event_id          TEXT NOT NULL,
    session_id        TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    runtime_epoch     INTEGER NOT NULL,
    delivery_version  INTEGER NOT NULL,
    context_commit_id TEXT NOT NULL,
    run_id            TEXT NOT NULL DEFAULT '',
    published_at      INTEGER NOT NULL DEFAULT 0,
    admitted_at       INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_collaboration_admissions_pending
    ON collaboration_admissions(agent_id, published_at, admitted_at);

CREATE TABLE IF NOT EXISTS context_compactions (
    operation_id     TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL,
    agent_id         TEXT NOT NULL,
    trigger_kind     TEXT NOT NULL,
    agent_role       TEXT NOT NULL,
    provider_id      TEXT NOT NULL DEFAULT '',
    account_id       TEXT NOT NULL DEFAULT '',
    model_id         TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL,
    stage_name       TEXT NOT NULL DEFAULT '',
    stage_index      INTEGER NOT NULL DEFAULT 0,
    completed_stages INTEGER NOT NULL DEFAULT 0,
    stage_count      INTEGER NOT NULL DEFAULT 4,
    retry_attempt    INTEGER NOT NULL DEFAULT 0,
    retry_limit      INTEGER NOT NULL DEFAULT 3,
    before_tokens    INTEGER NOT NULL DEFAULT 0,
    after_tokens     INTEGER NOT NULL DEFAULT 0,
    context_window   INTEGER NOT NULL DEFAULT 0,
    error_text       TEXT NOT NULL DEFAULT '',
    started_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    ended_at         INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_context_compactions_agent
    ON context_compactions(session_id, agent_id, started_at);

CREATE TABLE IF NOT EXISTS agent_turns (
    session_id  TEXT NOT NULL,
    agent_id    TEXT NOT NULL,
    generation INTEGER NOT NULL,
    origin      TEXT NOT NULL,
    state       TEXT NOT NULL,
    started_at  INTEGER NOT NULL,
    ended_at    INTEGER,
    PRIMARY KEY (session_id, agent_id, generation),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_recoveries (
    incident_id        TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    generation         INTEGER NOT NULL,
    state              TEXT NOT NULL,
    attempt            INTEGER NOT NULL DEFAULT 0,
    retry_at           INTEGER NOT NULL DEFAULT 0,
    summary            TEXT NOT NULL DEFAULT '',
    detail             TEXT NOT NULL DEFAULT '',
    action             TEXT NOT NULL DEFAULT '',
    pending_input_json BLOB NOT NULL,
    input_admitted     INTEGER NOT NULL DEFAULT 0,
    archive_json       BLOB NOT NULL,
    active_context_json BLOB NOT NULL,
    context_owner_id   TEXT NOT NULL DEFAULT '',
      context_revision   INTEGER NOT NULL DEFAULT 0,
    context_generation INTEGER NOT NULL DEFAULT 1,
    context_reason     TEXT NOT NULL DEFAULT 'recovery',
    owner_data         BLOB,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    ended_at           INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_recoveries_session
    ON agent_recoveries(session_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_agent_recoveries_agent
    ON agent_recoveries(session_id, agent_id, generation, updated_at);

-- A checkpoint's archive and active context are its archive_json and
-- active_context_json arrays followed by tail rows of the same field; writers
-- keep the arrays empty. version advances with every write of a field's list.
CREATE TABLE IF NOT EXISTS agent_recovery_tail (
    incident_id    TEXT NOT NULL,
    field          TEXT NOT NULL,
    position       INTEGER NOT NULL,
    message_json   BLOB NOT NULL,
    PRIMARY KEY (incident_id, field, position),
    FOREIGN KEY (incident_id) REFERENCES agent_recoveries(incident_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_recovery_lists (
    incident_id    TEXT NOT NULL,
    field          TEXT NOT NULL,
    version        INTEGER NOT NULL,
    PRIMARY KEY (incident_id, field),
    FOREIGN KEY (incident_id) REFERENCES agent_recoveries(incident_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS session_runtime_state (
    key        TEXT PRIMARY KEY,
    value      BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

-- session_status_rows are transcript lines the session owner records for a
-- change the user made to the session (Plan mode, YOLO, goal), so a reopened
-- transcript shows the same history as the live one.
CREATE TABLE IF NOT EXISTS session_status_rows (
    id         TEXT PRIMARY KEY,
    phase      TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

-- session_goal is the session's /goal: one objective pursued across turns
-- until an independent verifier confirms it or the user or a stop rule ends it.
CREATE TABLE IF NOT EXISTS session_goal (
    session_id   TEXT PRIMARY KEY,
    goal_id      TEXT NOT NULL,
    objective    TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('active','paused','blocked','budget_limited','complete')),
    reason       TEXT NOT NULL DEFAULT '',
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    token_budget INTEGER NOT NULL DEFAULT 0,
    time_used_ms INTEGER NOT NULL DEFAULT 0,
    waiting      INTEGER NOT NULL DEFAULT 0,
    verifier_id  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

-- Private single-owner permit; fork deliberately drops this sidecar.
CREATE TABLE IF NOT EXISTS session_goal_handover (
    session_id TEXT PRIMARY KEY,
    lifecycle_epoch INTEGER NOT NULL,
    record BLOB NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
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
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: resolve path: %w", err)
	}
	path := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		path = "/" + path
	}
	// modernc applies _pragma in order, including on replacement connections.
	// Waiting must be enabled before WAL takes its initial schema lock.
	// The argument is a filesystem path, never an unescaped SQLite URI. Literal
	// delimiters in a profile/session name cannot become connection options.
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)"}
	dsn := u.String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func Migrate(db *sql.DB) error {
	var hasSessions, hasMessageIdentities bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='sessions')`).Scan(&hasSessions); err != nil {
		return fmt.Errorf("sessiondb: inspect schema: %w", err)
	}
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='immutable_message_identities')`).Scan(&hasMessageIdentities); err != nil {
		return fmt.Errorf("sessiondb: inspect immutable message identity schema: %w", err)
	}
	if hasSessions && !hasMessageIdentities {
		return fmt.Errorf("sessiondb: current immutable message identity schema required; align local data manually")
	}
	// Initialize one schema atomically. Autocommit used to durably commit every
	// table/index separately, making native Windows/race fixture
	// startup dominate the full suite and leaving partial schemas on failure.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sessiondb: begin schema initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(schema)
	if err != nil {
		return fmt.Errorf("sessiondb: migrate: %w", err)
	}
	if _, err := tx.Exec(`SELECT capability_selection FROM session_agent_settings LIMIT 0`); err != nil {
		return fmt.Errorf("sessiondb: current capability schema required; align local data manually: %w", err)
	}
	if _, err := tx.Exec(`SELECT internal_id,collaboration_json FROM messages LIMIT 0`); err != nil {
		return fmt.Errorf("sessiondb: current message provenance schema required; align local data manually: %w", err)
	}
	if _, err := tx.Exec(`SELECT internal_id,session_id,agent_id,message_json FROM immutable_message_identities LIMIT 0`); err != nil {
		return fmt.Errorf("sessiondb: current immutable message identity schema required; align local data manually: %w", err)
	}
	// Current-format databases already contain every column. Re-parsing the
	// entire schema for 21 mostly failing ALTERs on each open was both a legacy
	// upgrade path and a dominant cost under the race detector. Never upgrade
	// an outdated database implicitly; validate it without changing its data.
	checks := []string{
		`SELECT display_meta,content_type,tool_error,provider_items FROM messages LIMIT 0`,
		`SELECT parent_session_id,agent_category,agent_name,depth,account_id FROM sessions LIMIT 0`,
		`SELECT api_standard,inherit_source,context_compaction_percent,account_id FROM session_agent_settings LIMIT 0`,
		`SELECT provider_id,model_id,account_id FROM context_compactions LIMIT 0`,
		`SELECT request_kind,request_outcome,account_id FROM usage_requests LIMIT 0`,
		`SELECT code_intelligence,max_sub_agents FROM session_flags LIMIT 0`,
	}
	for _, query := range checks {
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("sessiondb: current schema required; align local data manually: %w", err)
		}
	}
	if _, err := tx.Exec(`SELECT report_id,report_revision FROM sub_agent_results LIMIT 0`); err != nil {
		return fmt.Errorf("sessiondb: current sub-agent result schema required; align local data manually: %w", err)
	}
	return tx.Commit()
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
