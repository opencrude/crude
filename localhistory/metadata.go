package localhistory

import (
	"bytes"
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

	"github.com/sergi/go-diff/diffmatchpatch"
)

func (s *Store) recoverManifestIndex() error {
	entries, err := os.ReadDir(filepath.Join(s.root, "manifests"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
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
		plan, err := s.LoadPlan(strings.TrimSuffix(entry.Name(), ".json"))
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
CREATE TABLE IF NOT EXISTS history_snapshot_entries (
  snapshot_id TEXT NOT NULL REFERENCES history_snapshots(snapshot_id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  entry_type TEXT NOT NULL,
  mode INTEGER NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  symlink_target TEXT NOT NULL DEFAULT '',
  hardlink_target TEXT NOT NULL DEFAULT '',
  special_mode TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(snapshot_id,path)
);
CREATE INDEX IF NOT EXISTS idx_history_entries_path ON history_snapshot_entries(path,snapshot_id);
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
CREATE TABLE IF NOT EXISTS history_restore_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  plan_id TEXT NOT NULL,
  target_snapshot_id TEXT NOT NULL,
  safety_snapshot_id TEXT NOT NULL,
  after_snapshot_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  paths TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS history_restore_plans (
  plan_id TEXT PRIMARY KEY,
  target_snapshot_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  state TEXT NOT NULL DEFAULT 'preview',
  session_id TEXT NOT NULL DEFAULT '',
  message_id TEXT NOT NULL DEFAULT '',
  changes TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS history_restore_plans (
  plan_id TEXT PRIMARY KEY,
  target_snapshot_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  state TEXT NOT NULL DEFAULT 'preview',
  session_id TEXT NOT NULL DEFAULT '',
  message_id TEXT NOT NULL DEFAULT '',
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
	if err := ensureMetadataColumn(db, "history_snapshot_entries", "hardlink_target", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureMetadataColumn(db, "history_snapshot_entries", "special_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE history_snapshots SET kind='startup_reconcile',label='Startup reconciliation' WHERE kind='baseline' AND snapshot_id<>(SELECT snapshot_id FROM history_snapshots WHERE kind='baseline' ORDER BY created_at ASC LIMIT 1)`)
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
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO history_snapshot_entries(snapshot_id,path,entry_type,mode,hash,size,symlink_target,hardlink_target,special_mode) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for path, entry := range manifest.Entries {
		if _, err := stmt.Exec(manifest.ID, path, string(entry.Type), entry.Mode, entry.Hash, entry.Size, entry.SymlinkTarget, entry.HardlinkTarget, entry.SpecialMode); err != nil {
			_ = stmt.Close()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Timeline(limit int) ([]SnapshotRecord, error) {
	if limit <= 0 || limit > 10000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT snapshot_id,parent_snapshot_id,created_at,kind,label,logical_bytes
FROM history_snapshots ORDER BY created_at DESC LIMIT ?`, limit)
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
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT s.snapshot_id,s.parent_snapshot_id,s.created_at,s.kind,s.label,s.logical_bytes FROM history_snapshots s JOIN history_snapshot_entries e ON e.snapshot_id=s.snapshot_id WHERE e.path=? ORDER BY s.created_at DESC LIMIT ?`, path, limit)
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
(session_id,message_id,before_snapshot_id,opened_at,state) VALUES(?,?,?,?, 'active')
ON CONFLICT(session_id,message_id) DO UPDATE SET before_snapshot_id=excluded.before_snapshot_id,
opened_at=excluded.opened_at,after_snapshot_id='',closed_at=0,state='active',changed_paths='[]',collision_paths='[]'`,
		sessionID, messageID, beforeSnapshotID, openedAt.UnixNano())
	return err
}

func (s *Store) CloseMessageScope(sessionID, messageID, afterSnapshotID string, changed, collisions []string, closedAt time.Time) error {
	changedJSON, _ := json.Marshal(sortedUnique(changed))
	collisionJSON, _ := json.Marshal(sortedUnique(collisions))
	result, err := s.db.Exec(`UPDATE history_message_scopes SET after_snapshot_id=?,closed_at=?,state='closed',
changed_paths=?,collision_paths=? WHERE session_id=? AND message_id=? AND state='active'`,
		afterSnapshotID, closedAt.UnixNano(), string(changedJSON), string(collisionJSON), sessionID, messageID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("localhistory: active message scope not found")
	}
	return nil
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
		payload, _ := json.Marshal(changed)
		_, err = s.db.Exec(`UPDATE history_message_scopes SET after_snapshot_id=?,closed_at=?,state='closed',changed_paths=?,collision_paths=? WHERE session_id=? AND message_id=?`, afterSnapshotID, closedAt.UnixNano(), string(payload), string(payload), scope.session, scope.message)
		if err != nil {
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

func (s *Store) PlanMessageRestore(ctx context.Context, sessionID, messageID string, opts SnapshotOptions) (RestorePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	defer unlock()
	scope, ok, err := s.MessageScope(sessionID, messageID)
	if err != nil {
		return RestorePlan{}, err
	}
	if !ok {
		return RestorePlan{}, errors.New("localhistory: message has no history scope")
	}
	if scope.State != "closed" || scope.AfterSnapshot == "" {
		return RestorePlan{}, errors.New("localhistory: message history is not complete")
	}
	plan, err := s.planRestore(ctx, scope.BeforeSnapshot, opts)
	if err != nil {
		return RestorePlan{}, err
	}
	after, err := s.LoadManifest(scope.AfterSnapshot)
	if err != nil {
		return RestorePlan{}, err
	}
	wanted := make(map[string]bool, len(scope.ChangedPaths))
	for _, path := range scope.ChangedPaths {
		wanted[path] = true
	}
	collisions := make(map[string]bool, len(scope.CollisionPaths))
	for _, path := range scope.CollisionPaths {
		collisions[path] = true
	}
	filtered := make([]Change, 0, len(wanted))
	for _, change := range plan.Changes {
		if !wanted[change.Path] {
			continue
		}
		if collisions[change.Path] {
			change.ConflictReason = "ambiguous concurrent ownership"
			change.ExactAllowed = change.UnsafeReason == ""
		}
		afterEntry, afterOK := after.Entries[change.Path]
		var afterPtr *Entry
		if afterOK {
			copy := afterEntry
			afterPtr = &copy
		}
		if !entryPointersEqual(change.Current, afterPtr) && change.ConflictReason == "" {
			original := change.Target
			if merged, ok := s.mergeMessageChange(ctx, change.Path, original, afterPtr, change.Current); ok {
				change.ExactTarget = original
				change.Target = merged
				change.AutoMerged = true
			} else {
				change.ConflictReason = "file changed after this message"
				change.ExactAllowed = change.UnsafeReason == ""
				change.ExactTarget = original
			}
		}
		filtered = append(filtered, change)
	}
	plan.ID = ""
	plan.SessionID = sessionID
	plan.MessageID = messageID
	plan.Changes = filtered
	plan.CreatedAt = time.Now().UnixNano()
	if err := s.savePlan(&plan); err != nil {
		return RestorePlan{}, err
	}
	return plan, nil
}

func (s *Store) mergeMessageChange(ctx context.Context, path string, before, after, current *Entry) (*Entry, bool) {
	if before == nil || after == nil || current == nil || before.Type != EntryFile || after.Type != EntryFile || current.Type != EntryFile {
		return nil, false
	}
	beforeData, err := s.readFileBytes(before.Hash)
	if err != nil {
		return nil, false
	}
	afterData, err := s.readFileBytes(after.Hash)
	if err != nil {
		return nil, false
	}
	currentPath, err := s.workspacePath(path)
	if err != nil {
		return nil, false
	}
	currentData, err := os.ReadFile(currentPath)
	if err != nil {
		return nil, false
	}
	if !utf8.Valid(beforeData) || !utf8.Valid(afterData) || !utf8.Valid(currentData) || bytes.IndexByte(beforeData, 0) >= 0 || bytes.IndexByte(afterData, 0) >= 0 || bytes.IndexByte(currentData, 0) >= 0 {
		return nil, false
	}
	dmp := diffmatchpatch.New()
	patches := dmp.PatchMake(string(afterData), string(beforeData))
	merged, applied := dmp.PatchApply(patches, string(currentData))
	for _, ok := range applied {
		if !ok {
			return nil, false
		}
	}
	entry, err := s.storeBytes(ctx, []byte(merged), os.FileMode(before.Mode))
	if err != nil {
		return nil, false
	}
	return &entry, true
}

func (s *Store) recordRestore(result RestoreResult) error {
	paths, _ := json.Marshal(result.Paths)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`INSERT INTO history_restore_events
(plan_id,target_snapshot_id,safety_snapshot_id,after_snapshot_id,created_at,paths) VALUES(?,?,?,?,?,?)`,
		result.PlanID, result.TargetSnapshotID, result.SafetySnapshotID, result.AfterSnapshotID, time.Now().UnixNano(), string(paths))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE history_restore_plans SET state='applied' WHERE plan_id=?`, result.PlanID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recordPlan(plan RestorePlan) error {
	changes, _ := json.Marshal(plan.Changes)
	_, err := s.db.Exec(`INSERT OR IGNORE INTO history_restore_plans(plan_id,target_snapshot_id,created_at,state,session_id,message_id,changes) VALUES(?,?,?,'preview',?,?,?)`, plan.ID, plan.TargetSnapshotID, plan.CreatedAt, plan.SessionID, plan.MessageID, string(changes))
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
