package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

// Store provides durable memory operations over an already-open SQLite
// database (crude wires it to workspace.db). It is safe for concurrent use;
// the underlying pool is expected to be single-connection (sessiondb.Open
// sets MaxOpenConns(1)), so transactions are kept short.
type Store struct {
	db    *sql.DB
	ftsOK bool
}

// Attach migrates the memory schema onto db and returns a Store. FTS5
// availability is probed during migration: when unavailable, the store
// degrades to scan-based retrieval and records the fault in memory_meta —
// Attach only fails on base-schema errors.
func Attach(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("memory: nil db")
	}
	st := &Store{db: db}
	if err := st.migrate(); err != nil {
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
    kind              TEXT NOT NULL DEFAULT 'fact',
    title             TEXT NOT NULL,
    content           TEXT NOT NULL,
    source_session_id TEXT NOT NULL DEFAULT '',
    pinned            INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    entities          TEXT NOT NULL DEFAULT '',
    scope_paths       TEXT NOT NULL DEFAULT '[]',
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

// memoryColumns is the additive column set for workspace_memories. Legacy
// DBs (v0 schema: id/kind/title/content/source_session_id/pinned/created_at/
// updated_at) upgrade idempotently via PRAGMA table_info + ALTER TABLE.
var memoryColumns = []struct {
	Name string
	DDL  string
}{
	{"entities", "TEXT NOT NULL DEFAULT ''"},
	{"scope_paths", "TEXT NOT NULL DEFAULT '[]'"},
	{"importance", "INTEGER NOT NULL DEFAULT 40"},
	{"status", "TEXT NOT NULL DEFAULT 'active'"},
	{"superseded_by", "TEXT NOT NULL DEFAULT ''"},
	{"source", "TEXT NOT NULL DEFAULT 'tool'"},
	{"provenance", "TEXT NOT NULL DEFAULT ''"},
	{"use_count", "INTEGER NOT NULL DEFAULT 0"},
	{"last_used_at", "INTEGER NOT NULL DEFAULT 0"},
	{"norm", "TEXT NOT NULL DEFAULT ''"},
}

func (st *Store) migrate() error {
	if _, err := st.db.Exec(baseSchema); err != nil {
		return fmt.Errorf("memory: base schema: %w", err)
	}
	if err := st.ensureMemoryColumns(); err != nil {
		return err
	}
	// Index on additive columns must be created after the columns exist
	// (legacy v0 tables lack them until ensureMemoryColumns runs).
	if _, err := st.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_workspace_memories_status
			ON workspace_memories(status, updated_at DESC)
	`); err != nil {
		return fmt.Errorf("memory: status index: %w", err)
	}
	if err := st.backfillNorm(); err != nil {
		return err
	}
	st.migrateFTS()
	return nil
}

func (st *Store) ensureMemoryColumns() error {
	existing, err := tableColumns(st.db, "workspace_memories")
	if err != nil {
		return fmt.Errorf("memory: inspect columns: %w", err)
	}
	for _, col := range memoryColumns {
		if existing[col.Name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE workspace_memories ADD COLUMN %s %s", col.Name, col.DDL)
		if _, err := st.db.Exec(stmt); err != nil {
			return fmt.Errorf("memory: add column %s: %w", col.Name, err)
		}
	}
	return nil
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]bool)
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// backfillNorm computes the normalized shadow column for legacy rows
// written before the norm column existed.
func (st *Store) backfillNorm() error {
	rows, err := st.db.Query(`
		SELECT id, title, content, entities FROM workspace_memories
		WHERE norm = '' LIMIT 1024
	`)
	if err != nil {
		return fmt.Errorf("memory: norm backfill query: %w", err)
	}
	type normRow struct{ id, norm string }
	var updates []normRow
	for rows.Next() {
		var id, title, content, entities string
		if err := rows.Scan(&id, &title, &content, &entities); err != nil {
			_ = rows.Close()
			return fmt.Errorf("memory: norm backfill scan: %w", err)
		}
		updates = append(updates, normRow{id: id, norm: NormalizeText(title, content, entities)})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("memory: norm backfill rows: %w", err)
	}
	for _, u := range updates {
		if u.norm == "" {
			continue
		}
		if _, err := st.db.Exec(
			`UPDATE workspace_memories SET norm = ? WHERE id = ?`, u.norm, u.id,
		); err != nil {
			return fmt.Errorf("memory: norm backfill update: %w", err)
		}
	}
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

// MemoryID derives the stable dedup ID for a kind+title pair (same scheme
// as the v0 store so legacy IDs remain valid).
func MemoryID(kind, title string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.ToLower(strings.TrimSpace(title))))
	return "mem-" + hex.EncodeToString(sum[:8])
}

func validateKindDefaults(record *Record) error {
	record.Title = strings.TrimSpace(record.Title)
	record.Content = strings.TrimSpace(record.Content)
	record.Kind = strings.TrimSpace(record.Kind)
	if record.Kind == "" {
		record.Kind = KindFact
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
	if record.Importance <= 0 {
		record.Importance = DefaultImportance
	}
	if record.Importance > 100 {
		record.Importance = 100
	}
	if record.Status == "" {
		record.Status = StatusActive
	}
	if record.Source == "" {
		record.Source = SourceTool
	}
	return nil
}

// Remember upserts a record (ADD-only semantics: identical kind+title
// revises the same row and re-activates it; nothing else is touched).
// Entities and the FTS shadow column are derived mechanically when absent.
func (st *Store) Remember(ctx context.Context, record Record) (Record, error) {
	if err := validateKindDefaults(&record); err != nil {
		return Record{}, err
	}
	if record.ID == "" {
		record.ID = MemoryID(record.Kind, record.Title)
	}
	if record.Entities == "" {
		record.Entities = strings.Join(
			ExtractEntities(record.Title, record.Content, strings.Join(record.ScopePaths, " ")), " ")
	}
	now := time.Now().UnixNano()
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	norm := NormalizeText(record.Title, record.Content, record.Entities,
		strings.Join(record.ScopePaths, " "))
	scopeJSON, err := json.Marshal(record.ScopePaths)
	if err != nil {
		return Record{}, fmt.Errorf("memory: marshal scope paths: %w", err)
	}

	var existingID string
	err = st.db.QueryRowContext(ctx,
		`SELECT id FROM workspace_memories WHERE id = ?`, record.ID).Scan(&existingID)
	existed := err == nil
	if err != nil && err != sql.ErrNoRows {
		return Record{}, fmt.Errorf("memory: check existing: %w", err)
	}

	_, err = st.db.ExecContext(ctx, `
		INSERT INTO workspace_memories
			(id, kind, title, content, source_session_id, pinned, created_at, updated_at,
			 entities, scope_paths, importance, status, superseded_by, source, provenance,
			 use_count, last_used_at, norm)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, 0, 0, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			title = excluded.title,
			content = excluded.content,
			source_session_id = excluded.source_session_id,
			pinned = excluded.pinned,
			updated_at = excluded.updated_at,
			entities = excluded.entities,
			scope_paths = excluded.scope_paths,
			importance = excluded.importance,
			status = excluded.status,
			superseded_by = '',
			source = excluded.source,
			provenance = excluded.provenance,
			norm = excluded.norm
	`, record.ID, record.Kind, record.Title, record.Content, record.SourceSessionID,
		record.Pinned, record.CreatedAt, record.UpdatedAt,
		record.Entities, string(scopeJSON), record.Importance, record.Status,
		record.Source, record.Provenance, norm)
	if err != nil {
		return Record{}, fmt.Errorf("remember workspace memory: %w", err)
	}
	op := "remember"
	if existed {
		op = "revise"
	}
	st.journal(ctx, actorForSource(record.Source), op, record.ID, record.SourceSessionID,
		TruncateBytes(record.Title, 120))
	return record, nil
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
	use_count, last_used_at`

func scanRecord(scanner interface{ Scan(...any) error }) (Record, error) {
	var record Record
	var scopeJSON string
	err := scanner.Scan(
		&record.ID, &record.Kind, &record.Title, &record.Content,
		&record.SourceSessionID, &record.Pinned, &record.CreatedAt, &record.UpdatedAt,
		&record.Entities, &scopeJSON, &record.Importance, &record.Status,
		&record.SupersededBy, &record.Source, &record.Provenance,
		&record.UseCount, &record.LastUsedAt,
	)
	if err != nil {
		return Record{}, err
	}
	if scopeJSON != "" && scopeJSON != "[]" {
		_ = json.Unmarshal([]byte(scopeJSON), &record.ScopePaths)
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
func (st *Store) Supersede(ctx context.Context, id, byID, actor, sessionID string) error {
	result, err := st.db.ExecContext(ctx, `
		UPDATE workspace_memories
		SET status = ?, superseded_by = ?, updated_at = ?
		WHERE id = ? AND status != ?
	`, StatusSuperseded, byID, time.Now().UnixNano(), id, StatusSuperseded)
	if err != nil {
		return fmt.Errorf("supersede workspace memory: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("supersede workspace memory result: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("workspace memory %q was not found", id)
	}
	st.journal(ctx, actor, "supersede", id, sessionID, byID)
	return nil
}

// Update replaces a record's content: when kind+title map to the same ID
// the row is revised in place; otherwise a new record is created and the
// old one is superseded with a link to its replacement.
func (st *Store) Update(
	ctx context.Context, id string, replacement Record, actor, sessionID string,
) (Record, error) {
	existing, err := st.Get(ctx, id)
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
	if len(replacement.ScopePaths) == 0 {
		replacement.ScopePaths = existing.ScopePaths
	}
	replacement.Pinned = replacement.Pinned || existing.Pinned
	replacement.SourceSessionID = sessionID
	replacement.ID = MemoryID(replacement.Kind, replacement.Title)
	if replacement.ID == id {
		replacement.CreatedAt = existing.CreatedAt
	}
	saved, err := st.Remember(ctx, replacement)
	if err != nil {
		return Record{}, err
	}
	if saved.ID != id {
		if err := st.Supersede(ctx, id, saved.ID, actor, sessionID); err != nil {
			return Record{}, err
		}
	}
	return saved, nil
}

// SetPinned toggles the pinned flag on an active/archived record.
func (st *Store) SetPinned(ctx context.Context, id string, pinned bool, actor string) error {
	result, err := st.db.ExecContext(ctx, `
		UPDATE workspace_memories SET pinned = ?, updated_at = ? WHERE id = ?
	`, pinned, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("pin workspace memory: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pin workspace memory result: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("workspace memory %q was not found", id)
	}
	op := "pin"
	if !pinned {
		op = "unpin"
	}
	st.journal(ctx, actor, op, id, "", "")
	return nil
}

// Archive demotes records out of the auto-injection set (decay). Archived
// records still match explicit searches.
func (st *Store) Archive(ctx context.Context, ids []string, actor, detail string) error {
	now := time.Now().UnixNano()
	for _, id := range ids {
		if _, err := st.db.ExecContext(ctx, `
			UPDATE workspace_memories SET status = ?, updated_at = ?
			WHERE id = ? AND status = ?
		`, StatusArchived, now, id, StatusActive); err != nil {
			return fmt.Errorf("archive workspace memory: %w", err)
		}
		st.journal(ctx, actor, "archive", id, "", detail)
	}
	return nil
}

// Touch records usage reinforcement for injected/recalled memories.
func (st *Store) Touch(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UnixNano()
	for _, id := range ids {
		if _, err := st.db.ExecContext(ctx, `
			UPDATE workspace_memories
			SET use_count = use_count + 1, last_used_at = ?
			WHERE id = ?
		`, now, id); err != nil {
			return fmt.Errorf("touch workspace memory: %w", err)
		}
	}
	return nil
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
	var value string
	var updatedAt int64
	err := st.db.QueryRowContext(ctx,
		`SELECT value, updated_at FROM memory_meta WHERE key = ?`, key).Scan(&value, &updatedAt)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("memory lock read: %w", err)
	}
	if err == nil && value != "" && now.UnixNano()-updatedAt < ttl.Nanoseconds() {
		return false, nil
	}
	holder := fmt.Sprintf("%d:%d", now.UnixNano(), os.Getpid())
	if err := st.SetMeta(ctx, key, holder); err != nil {
		return false, err
	}
	return true, nil
}

// Unlock releases a named cooperative lock.
func (st *Store) Unlock(ctx context.Context, name string) error {
	return st.SetMeta(ctx, "lock:"+name, "")
}
