package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Size caps (bytes). Content beyond these is rejected (memories must stay
// prompt-sized); history mirror text is truncated instead.
const (
	MaxTitleBytes    = 256
	MaxContentBytes  = 64 << 10
	maxTurnTextBytes = 4 << 10
)

// MetaFTSFault is the memory_meta key recording why FTS5 was disabled.
const MetaFTSFault = "fts_fault"

var (
	ErrRevisionConflict = errors.New("memory: revision conflict")
	ErrRetired          = errors.New("memory: record is retired")
	ErrExists           = errors.New("memory: record already exists")
	ErrUnsupported      = errors.New("memory: claim lacks supporting evidence")
)

// Store provides durable memory operations over an already-open SQLite
// database (crude wires it to workspace.db). It is safe for concurrent use;
// the underlying pool is expected to be single-connection (sessiondb.Open
// sets MaxOpenConns(1)), so transactions are kept short.
type Store struct {
	db    *sql.DB
	ftsOK bool
}

// Attach initializes the current memory schema onto db and returns a Store. FTS5
// availability is probed during initialization: when unavailable, the store
// degrades to scan-based retrieval and records the fault in memory_meta —
// Attach only fails on base-schema errors.
func Attach(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("memory: nil db")
	}
	st := &Store{db: db}
	if err := st.initialize(); err != nil {
		return nil, err
	}
	return st, nil
}

// FTSEnabled reports whether FTS5 retrieval is active (false means the
// store fell back to scan-based retrieval; the reason is in memory_meta).
func (st *Store) FTSEnabled() bool { return st.ftsOK }

// DB exposes the underlying handle for read-only integrations (tests, UI).
func (st *Store) DB() *sql.DB { return st.db }

const baseSchema = `
CREATE TABLE IF NOT EXISTS workspace_memories (
    id                TEXT PRIMARY KEY,
    revision          INTEGER NOT NULL CHECK(revision > 0),
    kind              TEXT NOT NULL DEFAULT 'fact',
    title             TEXT NOT NULL,
    content           TEXT NOT NULL,
    source_session_id TEXT NOT NULL DEFAULT '',
    pinned            INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    entities          TEXT NOT NULL DEFAULT '',
    scope_paths       TEXT NOT NULL DEFAULT '[]',
    applies_when      TEXT NOT NULL DEFAULT '',
    evidence          TEXT NOT NULL DEFAULT '[]',
    actor             TEXT NOT NULL,
    importance        INTEGER NOT NULL DEFAULT 40,
    status            TEXT NOT NULL DEFAULT 'active',
    superseded_by     TEXT NOT NULL DEFAULT '',
    source            TEXT NOT NULL DEFAULT 'tool',
    provenance        TEXT NOT NULL DEFAULT '',
    use_count         INTEGER NOT NULL DEFAULT 0,
    last_used_at      INTEGER NOT NULL DEFAULT 0,
    norm              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_workspace_memories_updated
    ON workspace_memories(pinned DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_workspace_memories_status
    ON workspace_memories(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS memory_revisions (
    memory_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    record TEXT NOT NULL,
    session_id TEXT NOT NULL,
    op TEXT NOT NULL,
    PRIMARY KEY(memory_id, revision)
);
CREATE TRIGGER IF NOT EXISTS memory_revisions_no_update BEFORE UPDATE ON memory_revisions
    BEGIN SELECT RAISE(ABORT, 'memory revisions are immutable'); END;
CREATE TRIGGER IF NOT EXISTS memory_revisions_no_delete BEFORE DELETE ON memory_revisions
    BEGIN SELECT RAISE(ABORT, 'memory revisions are immutable'); END;

CREATE TABLE IF NOT EXISTS memory_observations (
    session_id TEXT NOT NULL,
    turn INTEGER NOT NULL,
    position INTEGER NOT NULL,
    message_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    observation TEXT NOT NULL,
    PRIMARY KEY(session_id, turn, position)
);
CREATE INDEX IF NOT EXISTS idx_memory_observations_origin
    ON memory_observations(session_id, tool_call_id, message_id);
CREATE TABLE IF NOT EXISTS memory_episodes (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    turn INTEGER NOT NULL,
    episode TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TRIGGER IF NOT EXISTS memory_episodes_no_update BEFORE UPDATE ON memory_episodes
    BEGIN SELECT RAISE(ABORT, 'memory episodes are immutable'); END;
CREATE TRIGGER IF NOT EXISTS memory_episodes_no_delete BEFORE DELETE ON memory_episodes
    BEGIN SELECT RAISE(ABORT, 'memory episodes are immutable'); END;

CREATE TABLE IF NOT EXISTS memory_exposures (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    at INTEGER NOT NULL,
    FOREIGN KEY(memory_id, revision) REFERENCES memory_revisions(memory_id, revision)
);
CREATE TABLE IF NOT EXISTS memory_feedback (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    actor TEXT NOT NULL,
    kind TEXT NOT NULL,
    evidence TEXT NOT NULL,
    at INTEGER NOT NULL,
    FOREIGN KEY(memory_id, revision) REFERENCES memory_revisions(memory_id, revision)
);

CREATE TABLE IF NOT EXISTS memory_history_turns (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id     TEXT NOT NULL,
    turn           INTEGER NOT NULL,
    user_text      TEXT NOT NULL DEFAULT '',
    assistant_text TEXT NOT NULL DEFAULT '',
    tool_names     TEXT NOT NULL DEFAULT '',
    files          TEXT NOT NULL DEFAULT '',
    norm           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    UNIQUE(session_id, turn)
);
CREATE INDEX IF NOT EXISTS idx_memory_history_session
    ON memory_history_turns(session_id, turn);

CREATE TABLE IF NOT EXISTS memory_session_summaries (
    session_id TEXT PRIMARY KEY,
    title      TEXT NOT NULL DEFAULT '',
    summary    TEXT NOT NULL DEFAULT '',
    key_files  TEXT NOT NULL DEFAULT '',
    outcome    TEXT NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_journal (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at         INTEGER NOT NULL,
    actor      TEXT NOT NULL,
    op         TEXT NOT NULL,
    memory_id  TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_memory_journal_memory
    ON memory_journal(memory_id, at);

CREATE TABLE IF NOT EXISTS memory_meta (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
`

func (st *Store) initialize() error {
	tx, err := st.db.Begin()
	if err != nil {
		return fmt.Errorf("memory: begin initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(baseSchema); err != nil {
		return fmt.Errorf("memory: base schema: %w", err)
	}
	if _, err := tx.Exec(`SELECT ` + recordSelectColumns + `, norm FROM workspace_memories LIMIT 0`); err != nil {
		return fmt.Errorf("memory: current schema required; align local data manually: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit initialization: %w", err)
	}
	st.migrateFTS()
	return nil
}

// ftsSpec describes one FTS5 index: the virtual table, its column list,
// the base table, and the trigger statements that keep it in sync.
type ftsSpec struct {
	table    string
	create   string
	triggers []string
	backfill string
}

var ftsSpecs = []ftsSpec{
	{
		table: "workspace_memories_fts",
		create: `CREATE VIRTUAL TABLE IF NOT EXISTS workspace_memories_fts USING fts5(
			id UNINDEXED, title, content, entities, norm
		)`,
		triggers: []string{
			`CREATE TRIGGER IF NOT EXISTS workspace_memories_fts_ai
			 AFTER INSERT ON workspace_memories BEGIN
			   INSERT INTO workspace_memories_fts(id, title, content, entities, norm)
			   VALUES (new.id, new.title, new.content, new.entities, new.norm);
			 END`,
			`CREATE TRIGGER IF NOT EXISTS workspace_memories_fts_ad
			 AFTER DELETE ON workspace_memories BEGIN
			   DELETE FROM workspace_memories_fts WHERE id = old.id;
			 END`,
			`CREATE TRIGGER IF NOT EXISTS workspace_memories_fts_au
			 AFTER UPDATE ON workspace_memories BEGIN
			   DELETE FROM workspace_memories_fts WHERE id = old.id;
			   INSERT INTO workspace_memories_fts(id, title, content, entities, norm)
			   VALUES (new.id, new.title, new.content, new.entities, new.norm);
			 END`,
		},
		backfill: `INSERT INTO workspace_memories_fts(id, title, content, entities, norm)
			SELECT id, title, content, entities, norm FROM workspace_memories`,
	},
	{
		table: "memory_history_fts",
		create: `CREATE VIRTUAL TABLE IF NOT EXISTS memory_history_fts USING fts5(
			rid UNINDEXED, user_text, assistant_text, tool_names, files, norm
		)`,
		triggers: []string{
			`CREATE TRIGGER IF NOT EXISTS memory_history_fts_ai
			 AFTER INSERT ON memory_history_turns BEGIN
			   INSERT INTO memory_history_fts(rid, user_text, assistant_text, tool_names, files, norm)
			   VALUES (new.id, new.user_text, new.assistant_text, new.tool_names, new.files, new.norm);
			 END`,
			`CREATE TRIGGER IF NOT EXISTS memory_history_fts_ad
			 AFTER DELETE ON memory_history_turns BEGIN
			   DELETE FROM memory_history_fts WHERE rid = old.id;
			 END`,
			`CREATE TRIGGER IF NOT EXISTS memory_history_fts_au
			 AFTER UPDATE ON memory_history_turns BEGIN
			   DELETE FROM memory_history_fts WHERE rid = old.id;
			   INSERT INTO memory_history_fts(rid, user_text, assistant_text, tool_names, files, norm)
			   VALUES (new.id, new.user_text, new.assistant_text, new.tool_names, new.files, new.norm);
			 END`,
		},
		backfill: `INSERT INTO memory_history_fts(rid, user_text, assistant_text, tool_names, files, norm)
			SELECT id, user_text, assistant_text, tool_names, files, norm FROM memory_history_turns`,
	},
	{
		table: "memory_summaries_fts",
		create: `CREATE VIRTUAL TABLE IF NOT EXISTS memory_summaries_fts USING fts5(
			session_id UNINDEXED, title, summary, key_files, outcome
		)`,
		triggers: []string{
			`CREATE TRIGGER IF NOT EXISTS memory_summaries_fts_ai
			 AFTER INSERT ON memory_session_summaries BEGIN
			   INSERT INTO memory_summaries_fts(session_id, title, summary, key_files, outcome)
			   VALUES (new.session_id, new.title, new.summary, new.key_files, new.outcome);
			 END`,
			`CREATE TRIGGER IF NOT EXISTS memory_summaries_fts_ad
			 AFTER DELETE ON memory_session_summaries BEGIN
			   DELETE FROM memory_summaries_fts WHERE session_id = old.session_id;
			 END`,
			`CREATE TRIGGER IF NOT EXISTS memory_summaries_fts_au
			 AFTER UPDATE ON memory_session_summaries BEGIN
			   DELETE FROM memory_summaries_fts WHERE session_id = old.session_id;
			   INSERT INTO memory_summaries_fts(session_id, title, summary, key_files, outcome)
			   VALUES (new.session_id, new.title, new.summary, new.key_files, new.outcome);
			 END`,
		},
		backfill: `INSERT INTO memory_summaries_fts(session_id, title, summary, key_files, outcome)
			SELECT session_id, title, summary, key_files, outcome FROM memory_session_summaries`,
	},
}

// migrateFTS probes FTS5 and installs the index tables + sync triggers.
// Any failure disables FTS for this process and records an observable
// fault in memory_meta — retrieval then uses the scan fallback. It never
// fails the open: memory must degrade, not break the workspace.
func (st *Store) migrateFTS() {
	for _, spec := range ftsSpecs {
		existed, err := st.tableExists(spec.table)
		if err != nil {
			st.disableFTS(fmt.Sprintf("inspect %s: %v", spec.table, err))
			return
		}
		if _, err := st.db.Exec(spec.create); err != nil {
			st.disableFTS(fmt.Sprintf("create %s: %v", spec.table, err))
			return
		}
		for _, trigger := range spec.triggers {
			if _, err := st.db.Exec(trigger); err != nil {
				st.disableFTS(fmt.Sprintf("trigger on %s: %v", spec.table, err))
				return
			}
		}
		if !existed {
			if _, err := st.db.Exec(spec.backfill); err != nil {
				st.disableFTS(fmt.Sprintf("backfill %s: %v", spec.table, err))
				return
			}
		}
	}
	st.ftsOK = true
	_ = st.SetMeta(context.Background(), MetaFTSFault, "")
}

func (st *Store) disableFTS(reason string) {
	st.ftsOK = false
	_ = st.SetMeta(context.Background(), MetaFTSFault, reason)
}

func (st *Store) tableExists(name string) (bool, error) {
	var found string
	err := st.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func newRecordID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("memory: generate id: %w", err)
	}
	return "mem-" + hex.EncodeToString(value[:]), nil
}

func validateKindDefaults(record *Record) error {
	record.Title = strings.TrimSpace(record.Title)
	record.Content = strings.TrimSpace(record.Content)
	record.Kind = strings.TrimSpace(record.Kind)
	if record.Kind == "" {
		record.Kind = KindFact
	}
	switch record.Kind {
	case KindFact, KindDecision, KindConvention, KindPreference, KindLesson, KindReference:
	default:
		return fmt.Errorf("memory: unknown knowledge kind %q", record.Kind)
	}
	if record.Title == "" || record.Content == "" {
		return fmt.Errorf("memory title and content are required")
	}
	if len(record.Title) > MaxTitleBytes {
		return fmt.Errorf("memory title exceeds %d bytes", MaxTitleBytes)
	}
	if len(record.Content) > MaxContentBytes {
		return fmt.Errorf("memory content exceeds %d bytes", MaxContentBytes)
	}
	if len(record.AppliesWhen) > 2048 || len(record.ScopePaths) > 32 {
		return fmt.Errorf("memory: applicability exceeds bounds")
	}
	for _, path := range record.ScopePaths {
		if len(path) > 512 || Redact(path) != path {
			return fmt.Errorf("memory: scope path exceeds bounds")
		}
	}
	if Redact(record.Title) != record.Title || Redact(record.Content) != record.Content ||
		Redact(record.AppliesWhen) != record.AppliesWhen || Redact(record.Provenance) != record.Provenance {
		return fmt.Errorf("memory: secrets are not permitted in knowledge")
	}
	if err := validateEvidence(record.Evidence); err != nil {
		return err
	}
	if record.Kind == KindLesson && !SupportsLesson(record.Evidence) {
		return ErrUnsupported
	}
	if record.Kind == KindLesson && strings.TrimSpace(record.AppliesWhen) == "" {
		return fmt.Errorf("memory: technical lessons require applies_when")
	}
	if record.Source == SourceAuto && !supportsKnowledge(record.Kind, record.Evidence) {
		return ErrUnsupported
	}
	if record.Importance <= 0 {
		record.Importance = DefaultImportance
	}
	if record.Importance > 100 {
		record.Importance = 100
	}
	if record.Source == "" {
		record.Source = SourceTool
	}
	return nil
}

func (st *Store) Remember(ctx context.Context, record Record) (Record, error) {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err = rememberTx(ctx, tx, record)
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, err
	}
	return record, nil
}

func rememberTx(ctx context.Context, tx *sql.Tx, record Record) (Record, error) {
	if err := validateKindDefaults(&record); err != nil {
		return Record{}, err
	}
	if record.Revision != 0 {
		return Record{}, fmt.Errorf("remember: use Update with an expected revision")
	}
	if record.ID == "" {
		var err error
		record.ID, err = newRecordID()
		if err != nil {
			return Record{}, err
		}
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memories WHERE id = ?`, record.ID).Scan(&exists); err != nil {
		return Record{}, err
	}
	if exists != 0 {
		return Record{}, ErrExists
	}
	now := time.Now().UnixNano()
	record.CreatedAt = now
	record.UpdatedAt = now
	record.Revision = 1
	record.Status = StatusActive
	record.SupersededBy = ""
	record.UseCount, record.LastUsedAt = 0, 0
	record.Actor = actorForSource(record.Source)
	deriveEntities(&record)
	if err := writeRecordTx(ctx, tx, record, 0, "remember", record.SourceSessionID); err != nil {
		return Record{}, err
	}
	return record, nil
}

func deriveEntities(record *Record) {
	record.Entities = strings.Join(ExtractEntities(record.Title, record.Content,
		strings.Join(record.ScopePaths, " "), record.AppliesWhen), " ")
}

func writeRecordTx(ctx context.Context, tx *sql.Tx, record Record, expected int64, op, sessionID string) error {
	if (op == "remember" || op == "update") && (record.Kind == KindLesson || record.Source == SourceAuto) {
		observations, err := groundedEvidenceTx(ctx, tx, record.Evidence)
		if err != nil {
			return err
		}
		if record.Kind == KindLesson && !supportedLessonObservations(observations) {
			return fmt.Errorf("%w: lesson requires matching failed validation, intervening change and successful validation", ErrUnsupported)
		}
	}
	scopeJSON, err := json.Marshal(record.ScopePaths)
	if err != nil {
		return err
	}
	evidenceJSON, err := json.Marshal(record.Evidence)
	if err != nil {
		return err
	}
	norm := NormalizeText(record.Title, record.Content, record.Entities,
		strings.Join(record.ScopePaths, " "), record.AppliesWhen)
	values := []any{record.Revision, record.Kind, record.Title, record.Content,
		record.SourceSessionID, record.Pinned, record.CreatedAt, record.UpdatedAt,
		record.Entities, string(scopeJSON), record.Importance, record.Status,
		record.SupersededBy, record.Source, record.Provenance, record.UseCount,
		record.LastUsedAt, record.AppliesWhen, string(evidenceJSON), record.Actor, norm}
	if expected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_memories
			(id, revision, kind, title, content, source_session_id, pinned, created_at,
			updated_at, entities, scope_paths, importance, status, superseded_by,
			source, provenance, use_count, last_used_at, applies_when, evidence, actor, norm)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append([]any{record.ID}, values...)...)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE workspace_memories SET
			revision=?, kind=?, title=?, content=?, source_session_id=?, pinned=?,
			created_at=?, updated_at=?, entities=?, scope_paths=?, importance=?,
			status=?, superseded_by=?, source=?, provenance=?, use_count=?, last_used_at=?,
			applies_when=?, evidence=?, actor=?, norm=? WHERE id=? AND revision=? AND status != ?`,
			append(values, record.ID, expected, StatusSuperseded)...)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				return ErrRevisionConflict
			}
		}
	}
	if err != nil {
		return fmt.Errorf("memory: write record: %w", err)
	}
	snapshot, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_revisions
		(memory_id, revision, record, session_id, op) VALUES (?, ?, ?, ?, ?)`,
		record.ID, record.Revision, string(snapshot), sessionID, op); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_meta(key, value, updated_at) VALUES ('knowledge_generation', '1', ?)
		ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1, updated_at=excluded.updated_at`, time.Now().UnixNano()); err != nil {
		return err
	}
	return journalTx(ctx, tx, record.Actor, op, record.ID, sessionID, fmt.Sprintf("revision:%d %s", record.Revision, record.Title))
}

func actorForSource(source string) string {
	switch source {
	case SourceUser:
		return ActorUser
	case SourceAuto:
		return ActorAuto
	case SourceConsolidation:
		return ActorConsolidation
	default:
		return ActorTool
	}
}

const recordSelectColumns = `
	id, kind, title, content, source_session_id, pinned, created_at, updated_at,
	entities, scope_paths, importance, status, superseded_by, source, provenance,
	use_count, last_used_at, revision, applies_when, evidence, actor`

func scanRecord(scanner interface{ Scan(...any) error }) (Record, error) {
	var record Record
	var scopeJSON, evidenceJSON string
	err := scanner.Scan(
		&record.ID, &record.Kind, &record.Title, &record.Content,
		&record.SourceSessionID, &record.Pinned, &record.CreatedAt, &record.UpdatedAt,
		&record.Entities, &scopeJSON, &record.Importance, &record.Status,
		&record.SupersededBy, &record.Source, &record.Provenance,
		&record.UseCount, &record.LastUsedAt,
		&record.Revision, &record.AppliesWhen, &evidenceJSON, &record.Actor,
	)
	if err != nil {
		return Record{}, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &record.ScopePaths); err != nil {
		return Record{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &record.Evidence); err != nil {
		return Record{}, err
	}
	return record, nil
}

// Get returns one record by ID regardless of status.
func (st *Store) Get(ctx context.Context, id string) (Record, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+recordSelectColumns+` FROM workspace_memories WHERE id = ?`, id)
	record, err := scanRecord(row)
	if err == sql.ErrNoRows {
		return Record{}, fmt.Errorf("workspace memory %q was not found", id)
	}
	if err != nil {
		return Record{}, fmt.Errorf("get workspace memory: %w", err)
	}
	return record, nil
}

// Supersede marks a record superseded (the "forget" operation — nothing is
// deleted). byID links to the replacement when there is one.
func (st *Store) Supersede(ctx context.Context, id string, expectedRevision int64, byID, actor, sessionID string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getExpectedTx(ctx, tx, id, expectedRevision)
	if err != nil {
		return err
	}
	if byID != "" {
		if byID == id {
			return fmt.Errorf("memory: cannot supersede itself")
		}
		by, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordSelectColumns+` FROM workspace_memories WHERE id=?`, byID))
		if err != nil || by.Status == StatusSuperseded {
			return fmt.Errorf("memory: replacement must exist and not be retired")
		}
	}
	if err := retireTx(ctx, tx, record, byID, actor, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (st *Store) Update(
	ctx context.Context, id string, replacement Record, actor, sessionID string,
) (Record, error) {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := getExpectedTx(ctx, tx, id, replacement.Revision)
	if err != nil {
		return Record{}, err
	}
	if replacement.Kind == "" {
		replacement.Kind = existing.Kind
	}
	if replacement.Title == "" {
		replacement.Title = existing.Title
	}
	if replacement.Importance <= 0 {
		replacement.Importance = existing.Importance
	}
	if replacement.ScopePaths == nil {
		replacement.ScopePaths = existing.ScopePaths
	}
	if replacement.Evidence == nil {
		replacement.Evidence = existing.Evidence
	}
	if replacement.AppliesWhen == "" {
		replacement.AppliesWhen = existing.AppliesWhen
	}
	if replacement.Source == "" {
		replacement.Source = existing.Source
	}
	replacement.SourceSessionID = sessionID
	replacement.ID = id
	replacement.CreatedAt = existing.CreatedAt
	replacement.Revision = existing.Revision + 1
	replacement.UpdatedAt = time.Now().UnixNano()
	replacement.Status = existing.Status
	replacement.SupersededBy = ""
	replacement.Actor = actor
	replacement.UseCount, replacement.LastUsedAt = 0, 0
	if err := validateKindDefaults(&replacement); err != nil {
		return Record{}, err
	}
	deriveEntities(&replacement)
	if err := writeRecordTx(ctx, tx, replacement, existing.Revision, "update", sessionID); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, err
	}
	return replacement, nil
}

func getExpectedTx(ctx context.Context, tx *sql.Tx, id string, expected int64) (Record, error) {
	if expected <= 0 {
		return Record{}, fmt.Errorf("%w: expected_revision is required", ErrRevisionConflict)
	}
	record, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordSelectColumns+` FROM workspace_memories WHERE id=?`, id))
	if err != nil {
		return Record{}, err
	}
	if record.Revision != expected {
		return Record{}, fmt.Errorf("%w: %s expected %d current %d", ErrRevisionConflict, id, expected, record.Revision)
	}
	if record.Status == StatusSuperseded {
		return Record{}, ErrRetired
	}
	return record, nil
}

func retireTx(ctx context.Context, tx *sql.Tx, record Record, byID, actor, sessionID string) error {
	expected := record.Revision
	record.Revision++
	record.Status, record.SupersededBy, record.Actor = StatusSuperseded, byID, actor
	record.UpdatedAt = time.Now().UnixNano()
	return writeRecordTx(ctx, tx, record, expected, "supersede", sessionID)
}

func (st *Store) SetPinned(ctx context.Context, id string, expectedRevision int64, pinned bool, actor string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getExpectedTx(ctx, tx, id, expectedRevision)
	if err != nil {
		return err
	}
	record.Pinned, record.Actor = pinned, actor
	record.Revision++
	op := "pin"
	if !pinned {
		op = "unpin"
	}
	if err := writeRecordTx(ctx, tx, record, expectedRevision, op, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// Archive demotes records out of the auto-injection set (decay). Archived
// records still match explicit searches.
func (st *Store) Archive(ctx context.Context, refs []RevisionRef, actor, detail string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, ref := range refs {
		record, err := getExpectedTx(ctx, tx, ref.ID, ref.Revision)
		if err != nil {
			return err
		}
		if record.Status != StatusActive || record.Pinned {
			continue
		}
		expected := record.Revision
		record.Revision++
		record.Status, record.Actor = StatusArchived, actor
		if err := writeRecordTx(ctx, tx, record, expected, "archive", ""); err != nil {
			return err
		}
		if err := journalTx(ctx, tx, actor, "archive_reason", ref.ID, "", detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (st *Store) Revisions(ctx context.Context, id string) ([]Revision, error) {
	rows, err := st.db.QueryContext(ctx, `SELECT record, session_id, op FROM memory_revisions WHERE memory_id=? ORDER BY revision`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var revisions []Revision
	for rows.Next() {
		var revision Revision
		var snapshot string
		if err := rows.Scan(&snapshot, &revision.SessionID, &revision.Op); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(snapshot), &revision.Record); err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func journalTx(ctx context.Context, tx *sql.Tx, actor, op, memoryID, sessionID, detail string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO memory_journal (at, actor, op, memory_id, session_id, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().UnixNano(), actor, op, memoryID, sessionID, TruncateBytes(Redact(detail), 512))
	return err
}

// CountByStatus returns the number of records with the given status.
func (st *Store) CountByStatus(ctx context.Context, status string) (int, error) {
	var count int
	err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspace_memories WHERE status = ?`, status).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count workspace memories: %w", err)
	}
	return count, nil
}

// journal appends an audit entry. Journal failures must never fail the
// primary operation; they are recorded as a meta fault instead.
func (st *Store) journal(ctx context.Context, actor, op, memoryID, sessionID, detail string) {
	_, err := st.db.ExecContext(ctx, `
		INSERT INTO memory_journal (at, actor, op, memory_id, session_id, detail)
		VALUES (?, ?, ?, ?, ?, ?)
	`, time.Now().UnixNano(), actor, op, memoryID, sessionID, TruncateBytes(detail, 512))
	if err != nil {
		_ = st.SetMeta(ctx, "journal_fault", err.Error())
	}
}

// JournalForMemory returns the audit trail for one memory, oldest first.
func (st *Store) JournalForMemory(ctx context.Context, memoryID string, limit int) ([]JournalEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT id, at, actor, op, memory_id, session_id, detail
		FROM memory_journal WHERE memory_id = ? ORDER BY at ASC LIMIT ?
	`, memoryID, limit)
	if err != nil {
		return nil, fmt.Errorf("memory journal query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var entries []JournalEntry
	for rows.Next() {
		var e JournalEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Op, &e.MemoryID, &e.SessionID, &e.Detail); err != nil {
			return nil, fmt.Errorf("memory journal scan: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// GetMeta returns a memory_meta value ("" when unset).
func (st *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var value string
	err := st.db.QueryRowContext(ctx,
		`SELECT value FROM memory_meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("memory meta get: %w", err)
	}
	return value, nil
}

// SetMeta stores a memory_meta key/value pair.
func (st *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := st.db.ExecContext(ctx, `
		INSERT INTO memory_meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("memory meta set: %w", err)
	}
	return nil
}

// TryLock acquires a named cooperative lock (background jobs). A lock older
// than ttl is treated as stale and stolen. Returns false when another
// holder is fresh.
func (st *Store) TryLock(ctx context.Context, name string, ttl time.Duration) (bool, error) {
	key := "lock:" + name
	now := time.Now()
	holder := fmt.Sprintf("%d:%d", now.UnixNano(), os.Getpid())
	result, err := st.db.ExecContext(ctx, `INSERT INTO memory_meta(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at
		WHERE memory_meta.value='' OR memory_meta.updated_at<=?`, key, holder, now.UnixNano(), now.Add(-ttl).UnixNano())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// Unlock releases a named cooperative lock.
func (st *Store) Unlock(ctx context.Context, name string) error {
	return st.SetMeta(ctx, "lock:"+name, "")
}
