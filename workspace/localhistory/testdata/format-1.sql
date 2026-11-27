-- Local History database format 1: the schema of Crude v0.8.15 and earlier
-- (git show v0.8.15:workspace/localhistory/metadata.go). Frozen test data; never edit.
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
);