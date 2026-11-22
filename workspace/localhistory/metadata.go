package localhistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Store) recoverManifestIndex() error {
	// Retained evidence must never be repaired by orphan recovery. Its durable
	// reference promises the original indexed, immutable snapshot, not a new
	// reconstruction of missing metadata.
	if _, err := s.retainedSafetySnapshots(context.Background()); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "manifests"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		// Snapshot identity and every entry commit in one transaction. Only
		// orphan manifests need recovery; replaying committed trees makes open
		// scale with all historical files instead of the snapshot count.
		var indexed bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM history_snapshots WHERE snapshot_id=?)`, id).Scan(&indexed); err != nil {
			return err
		}
		if indexed {
			continue
		}
		manifest, err := s.LoadManifest(id)
		if err != nil {
			return fmt.Errorf("localhistory: recover manifest %s: %w", id, err)
		}
		if err := s.recordSnapshot(manifest); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recoverPlanIndex() error {
	entries, err := os.ReadDir(filepath.Join(s.root, "plans"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		var indexed bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM history_restore_plans WHERE plan_id=?)`, id).Scan(&indexed); err != nil {
			return err
		}
		if indexed {
			continue
		}
		plan, err := s.LoadPlan(id)
		if err != nil {
			return err
		}
		if err := s.recordPlan(plan); err != nil {
			return err
		}
	}
	return nil
}

func migrateMetadata(db *sql.DB) error {
	_, err := db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA journal_size_limit=67108864;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS history_snapshots (
  snapshot_id TEXT PRIMARY KEY,
  parent_snapshot_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  kind TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  logical_bytes INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_history_snapshots_time ON history_snapshots(created_at DESC);
CREATE TABLE IF NOT EXISTS history_index_paths (
  path_id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS history_snapshot_keys (
  seq INTEGER PRIMARY KEY,
  snapshot_id TEXT NOT NULL UNIQUE REFERENCES history_snapshots(snapshot_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS history_snapshot_paths (
  snapshot_seq INTEGER NOT NULL REFERENCES history_snapshot_keys(seq) ON DELETE CASCADE,
  path_id INTEGER NOT NULL,
  PRIMARY KEY(snapshot_seq,path_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS history_retained_snapshots (
  reference TEXT PRIMARY KEY,
  snapshot_id TEXT NOT NULL REFERENCES history_snapshots(snapshot_id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS history_safety_protection_events (
  event_id TEXT PRIMARY KEY,
  reference TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK(revision > 0),
  snapshot_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK(action IN ('release','reprotect')),
  actor_client_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  payload TEXT NOT NULL,
  UNIQUE(reference,revision),
  UNIQUE(actor_client_id,request_id)
);
CREATE TABLE IF NOT EXISTS history_message_scopes (
  session_id TEXT NOT NULL,
  message_id TEXT NOT NULL,
  before_snapshot_id TEXT NOT NULL,
  after_snapshot_id TEXT NOT NULL DEFAULT '',
  opened_at INTEGER NOT NULL,
  closed_at INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'active',
  changed_paths TEXT NOT NULL DEFAULT '[]',
  collision_paths TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY(session_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_history_scopes_closed ON history_message_scopes(session_id, closed_at DESC);
CREATE TABLE IF NOT EXISTS history_agent_operations (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  operation_id TEXT NOT NULL UNIQUE,
  session_id TEXT NOT NULL,
  message_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  source TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'prepared',
  created_at INTEGER NOT NULL,
  FOREIGN KEY(session_id, message_id) REFERENCES history_message_scopes(session_id, message_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_history_agent_operations_scope ON history_agent_operations(session_id, message_id, agent_id, sequence);
CREATE TABLE IF NOT EXISTS history_agent_operation_runs (
  run_id TEXT NOT NULL,
  operation_id TEXT NOT NULL UNIQUE REFERENCES history_agent_operations(operation_id) ON DELETE CASCADE,
  PRIMARY KEY(run_id, operation_id)
);
CREATE TABLE IF NOT EXISTS history_agent_operation_paths (
  operation_id TEXT NOT NULL REFERENCES history_agent_operations(operation_id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  path TEXT NOT NULL,
  before_entry TEXT NOT NULL DEFAULT '',
  after_entry TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(operation_id, path)
);
CREATE INDEX IF NOT EXISTS idx_history_agent_operation_paths_path ON history_agent_operation_paths(path, operation_id);
CREATE TABLE IF NOT EXISTS history_restore_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  plan_id TEXT NOT NULL,
  target_snapshot_id TEXT NOT NULL,
  safety_snapshot_id TEXT NOT NULL,
  after_snapshot_id TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  message_id TEXT NOT NULL DEFAULT '',
  agent_id TEXT NOT NULL DEFAULT '',
  notified INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  paths TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS history_restore_transactions (
  plan_id TEXT PRIMARY KEY,
  safety_snapshot_id TEXT NOT NULL,
  paths TEXT NOT NULL,
  applied_entries TEXT NOT NULL DEFAULT '{}',
  state TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS history_restore_plans (
  plan_id TEXT PRIMARY KEY,
  target_snapshot_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  state TEXT NOT NULL DEFAULT 'preview',
  session_id TEXT NOT NULL DEFAULT '',
  message_id TEXT NOT NULL DEFAULT '',
  agent_id TEXT NOT NULL DEFAULT '',
  changes TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS history_retention_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at INTEGER NOT NULL,
  removed_snapshots INTEGER NOT NULL,
  removed_objects INTEGER NOT NULL,
  freed_bytes INTEGER NOT NULL
);`)
	if err != nil {
		return fmt.Errorf("localhistory: migrate metadata: %w", err)
	}
	for _, column := range []struct{ table, name, ddl string }{
		{"history_agent_operations", "status", "TEXT NOT NULL DEFAULT 'prepared'"},
		{"history_restore_plans", "session_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_restore_plans", "message_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_restore_plans", "agent_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_restore_events", "session_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_restore_events", "message_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_restore_events", "agent_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_restore_events", "notified", "INTEGER NOT NULL DEFAULT 0"},
		{"history_restore_transactions", "applied_entries", "TEXT NOT NULL DEFAULT '{}'"},
	} {
		if err := ensureMetadataColumn(db, column.table, column.name, column.ddl); err != nil {
			return err
		}
	}
	_, err = db.Exec(`UPDATE history_snapshots SET kind='startup_reconcile',label='Startup reconciliation' WHERE kind='baseline' AND snapshot_id<>(SELECT snapshot_id FROM history_snapshots WHERE kind='baseline' ORDER BY created_at ASC,rowid ASC LIMIT 1)`)
	if err != nil {
		return fmt.Errorf("localhistory: normalize baselines: %w", err)
	}
	return nil
}

func ensureMetadataColumn(db *sql.DB, table, name, ddl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var column, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &column, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + ddl)
	return err
}

// recordSnapshot indexes a saved manifest. The content-addressed manifest is
// the authority for every entry; the index keeps only which paths a snapshot
// contains, as small integer rows clustered by snapshot. Recording appends one
// contiguous range and retention deletes one, so neither scales with the
// whole history. A full copy of every entry per snapshot made the metadata
// hundreds of megabytes and held the store for seconds.
func (s *Store) recordSnapshot(manifest Manifest) error {
	var logical int64
	for _, entry := range manifest.Entries {
		if entry.Type == EntryFile {
			logical += entry.Size
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`INSERT OR IGNORE INTO history_snapshots
(snapshot_id,parent_snapshot_id,created_at,kind,label,logical_bytes) VALUES(?,?,?,?,?,?)`,
		manifest.ID, manifest.ParentID, manifest.CreatedAt, manifest.Kind, manifest.Label, logical)
	if err != nil {
		return err
	}
	key, err := tx.Exec(`INSERT OR IGNORE INTO history_snapshot_keys(snapshot_id) VALUES(?)`, manifest.ID)
	if err != nil {
		return err
	}
	if added, err := key.RowsAffected(); err != nil {
		return err
	} else if added == 0 {
		// Indexed snapshots are immutable; never replay their paths.
		return tx.Commit()
	}
	seq, err := key.LastInsertId()
	if err != nil {
		return err
	}
	if err := insertSnapshotPaths(tx, seq, manifest.Entries); err != nil {
		return err
	}
	return tx.Commit()
}

// insertSnapshotPaths links every path in one statement per batch. JSON text
// carries only valid UTF-8 exactly; any other path name is linked on its own.
func insertSnapshotPaths(tx *sql.Tx, seq int64, entries map[string]Entry) error {
	batch := make([]string, 0, len(entries))
	var exact []string
	for path := range entries {
		if utf8.ValidString(path) {
			batch = append(batch, path)
		} else {
			exact = append(exact, path)
		}
	}
	linked := int64(0)
	if len(batch) > 0 {
		encoded, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO history_index_paths(path) SELECT value FROM json_each(?)`, string(encoded)); err != nil {
			return err
		}
		result, err := tx.Exec(`INSERT INTO history_snapshot_paths(snapshot_seq,path_id)
SELECT ?,p.path_id FROM json_each(?) j JOIN history_index_paths p ON p.path=j.value`, seq, string(encoded))
		if err != nil {
			return err
		}
		if linked, err = result.RowsAffected(); err != nil {
			return err
		}
	}
	for _, path := range exact {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO history_index_paths(path) VALUES(?)`, path); err != nil {
			return err
		}
		result, err := tx.Exec(`INSERT INTO history_snapshot_paths(snapshot_seq,path_id) SELECT ?,path_id FROM history_index_paths WHERE path=?`, seq, path)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		linked += n
	}
	if linked != int64(len(entries)) {
		return fmt.Errorf("localhistory: indexed %d of %d snapshot paths", linked, len(entries))
	}
	return nil
}

func (s *Store) Timeline(limit int) ([]SnapshotRecord, error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	defer s.endOperation()
	return s.timeline(limit)
}

func (s *Store) timeline(limit int) ([]SnapshotRecord, error) {
	query := `SELECT snapshot_id,parent_snapshot_id,created_at,kind,label,logical_bytes
FROM history_snapshots ORDER BY created_at DESC,rowid DESC`
	var args []any
	if limit >= 0 {
		if limit == 0 || limit > 10000 {
			limit = 200
		}
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotRecord
	for rows.Next() {
		var record SnapshotRecord
		if err := rows.Scan(&record.ID, &record.ParentID, &record.CreatedAt, &record.Kind, &record.Label, &record.LogicalBytes); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) FileHistory(path string, limit int) ([]SnapshotRecord, error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	defer s.endOperation()
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT s.snapshot_id,s.parent_snapshot_id,s.created_at,s.kind,s.label,s.logical_bytes
FROM history_index_paths p
JOIN history_snapshot_paths e ON e.path_id=p.path_id
JOIN history_snapshot_keys k ON k.seq=e.snapshot_seq
JOIN history_snapshots s ON s.snapshot_id=k.snapshot_id
WHERE p.path=? ORDER BY s.created_at DESC,s.rowid DESC LIMIT ?`, path, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotRecord
	for rows.Next() {
		var record SnapshotRecord
		if err := rows.Scan(&record.ID, &record.ParentID, &record.CreatedAt, &record.Kind, &record.Label, &record.LogicalBytes); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) BeginMessageScope(sessionID, messageID, beforeSnapshotID string, openedAt time.Time) error {
	if sessionID == "" || messageID == "" || !validID(beforeSnapshotID) {
		return errors.New("localhistory: invalid message scope")
	}
	_, err := s.db.Exec(`INSERT INTO history_message_scopes
(session_id,message_id,before_snapshot_id,opened_at,state) VALUES(?,?,?,?, 'active')`,
		sessionID, messageID, beforeSnapshotID, openedAt.UnixNano())
	return err
}

func (s *Store) CloseMessageScope(sessionID, messageID, afterSnapshotID string, changed, collisions []string, closedAt time.Time) error {
	if !validID(afterSnapshotID) {
		return errors.New("localhistory: invalid message scope snapshot")
	}
	return s.finalizeMessageScope(sessionID, messageID, afterSnapshotID, changed, collisions, closedAt, "active")
}

func (s *Store) finalizeMessageScope(sessionID, messageID, afterSnapshotID string, changed, collisions []string, closedAt time.Time, expectedState string) error {
	changedJSON, _ := json.Marshal(sortedUnique(changed))
	collisionJSON, _ := json.Marshal(sortedUnique(collisions))
	result, err := s.db.Exec(`UPDATE history_message_scopes SET after_snapshot_id=?,closed_at=?,state='closed',
	changed_paths=?,collision_paths=? WHERE session_id=? AND message_id=? AND state=?`,
		afterSnapshotID, closedAt.UnixNano(), string(changedJSON), string(collisionJSON), sessionID, messageID, expectedState)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("localhistory: %s message scope not found", expectedState)
	}
	return nil
}

func cleanRelativeHistoryPath(path string) (string, bool) {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return "", false
	}
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return "", false
	}
	return path, true
}

func (s *Store) MarkInterruptedScopes(closedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE history_message_scopes SET state='interrupted',closed_at=? WHERE state='active'`, closedAt.UnixNano())
	return err
}

func (s *Store) RecoverInterruptedScopes(afterSnapshotID string, closedAt time.Time) error {
	rows, err := s.db.Query(`SELECT session_id,message_id,before_snapshot_id FROM history_message_scopes WHERE state='interrupted' AND after_snapshot_id=''`)
	if err != nil {
		return err
	}
	type pending struct{ session, message, before string }
	var scopes []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.session, &p.message, &p.before); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, scope := range scopes {
		changed, err := s.DiffSnapshotPaths(scope.before, afterSnapshotID)
		if err != nil {
			return err
		}
		if err := s.finalizeMessageScope(scope.session, scope.message, afterSnapshotID, changed, changed, closedAt, "interrupted"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MessageScope(sessionID, messageID string) (MessageScope, bool, error) {
	var scope MessageScope
	var changedJSON, collisionJSON string
	err := s.db.QueryRow(`SELECT session_id,message_id,before_snapshot_id,after_snapshot_id,opened_at,closed_at,state,
changed_paths,collision_paths FROM history_message_scopes WHERE session_id=? AND message_id=?`, sessionID, messageID).
		Scan(&scope.SessionID, &scope.MessageID, &scope.BeforeSnapshot, &scope.AfterSnapshot, &scope.OpenedAt,
			&scope.ClosedAt, &scope.State, &changedJSON, &collisionJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageScope{}, false, nil
	}
	if err != nil {
		return MessageScope{}, false, err
	}
	_ = json.Unmarshal([]byte(changedJSON), &scope.ChangedPaths)
	_ = json.Unmarshal([]byte(collisionJSON), &scope.CollisionPaths)
	return scope, true, nil
}

func (s *Store) LatestUndoableScope(sessionID string) (MessageScope, bool, error) {
	var messageID string
	err := s.db.QueryRow(`SELECT message_id FROM history_message_scopes
WHERE session_id=? AND state='closed' AND after_snapshot_id<>'' AND changed_paths<>'[]'
ORDER BY closed_at DESC LIMIT 1`, sessionID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageScope{}, false, nil
	}
	if err != nil {
		return MessageScope{}, false, err
	}
	return s.MessageScope(sessionID, messageID)
}

func (s *Store) UndoableScopes(sessionID string, limit int) ([]MessageScope, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT message_id FROM history_message_scopes WHERE session_id=? AND state='closed' AND after_snapshot_id<>'' AND changed_paths<>'[]' ORDER BY closed_at DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]MessageScope, 0, len(ids))
	for _, id := range ids {
		scope, ok, err := s.MessageScope(sessionID, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, scope)
		}
	}
	return out, nil
}

func (s *Store) recordRestore(result RestoreResult) error {
	paths, _ := json.Marshal(result.Paths)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`INSERT INTO history_restore_events
(plan_id,target_snapshot_id,safety_snapshot_id,after_snapshot_id,session_id,message_id,agent_id,notified,created_at,paths)
VALUES(?,?,?,?,?,?,?,0,?,?)`, result.PlanID, result.TargetSnapshotID, result.SafetySnapshotID, result.AfterSnapshotID,
		result.SessionID, result.MessageID, result.AgentID, time.Now().UnixNano(), string(paths))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE history_restore_plans SET state='applied' WHERE plan_id=?`, result.PlanID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE history_restore_transactions SET state='committed',updated_at=? WHERE plan_id=?`, time.Now().UnixNano(), result.PlanID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recordPlan(plan RestorePlan) error {
	changes, _ := json.Marshal(plan.Changes)
	_, err := s.db.Exec(`INSERT OR IGNORE INTO history_restore_plans(plan_id,target_snapshot_id,created_at,state,session_id,message_id,agent_id,changes) VALUES(?,?,?,'preview',?,?,?,?)`, plan.ID, plan.TargetSnapshotID, plan.CreatedAt, plan.SessionID, plan.MessageID, plan.AgentID, string(changes))
	return err
}

func (s *Store) PendingRestoreNotifications(sessionID string) ([]RestoreResult, error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	defer s.endOperation()
	rows, err := s.db.Query(`SELECT plan_id,target_snapshot_id,safety_snapshot_id,after_snapshot_id,session_id,message_id,agent_id,paths
FROM history_restore_events WHERE notified=0 AND session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []RestoreResult
	for rows.Next() {
		var result RestoreResult
		var pathsJSON string
		if err := rows.Scan(&result.PlanID, &result.TargetSnapshotID, &result.SafetySnapshotID, &result.AfterSnapshotID, &result.SessionID, &result.MessageID, &result.AgentID, &pathsJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(pathsJSON), &result.Paths); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *Store) MarkRestoreNotified(planID string) error {
	_, err := s.db.Exec(`UPDATE history_restore_events SET notified=1 WHERE plan_id=?`, planID)
	return err
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
