package workspace

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"crude/sessiondb"
)

// WorkspaceDB manages the workspace-level SQLite database. Unlike session
// databases (which store per-conversation data), the workspace database
// stores project-scoped metadata that persists across sessions:
//
// - Workspace settings (theme overrides, editor preferences, etc.)
// - List of all AI sessions that have run in this workspace
// - Workspace-level analytics and usage statistics
//
// The workspace DB is separate from session DBs to keep the data model
// clean: a workspace contains many sessions, each with their own DB.
type WorkspaceDB struct {
	DB         *sql.DB
	Path       string
	Dir        string
	ProjectDir string
}

// OpenWorkspaceDB opens (or creates) the workspace-level database for the
// given project directory. The database file lives at:
//
//	~/.crude/workspaces/<hash>/workspace.db
//
// If the database doesn't exist, it's created and the schema is migrated.
func OpenWorkspaceDB(projectDir string) (*WorkspaceDB, error) {
	dbPath, err := WorkspaceDBPath(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace db path: %w", err)
	}

	wdb, err := openWorkspaceDBAt(dbPath)
	if err != nil {
		return nil, err
	}
	wdb.ProjectDir = projectDir
	// Persist project_dir so the workspace selector can display a name.
	_ = wdb.SetSetting("project_dir", projectDir)
	return wdb, nil
}

// openWorkspaceDBAt opens a workspace DB at an explicit path.
func openWorkspaceDBAt(dbPath string) (*WorkspaceDB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create workspace dir: %w", err)
	}

	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open workspace db: %w", err)
	}

	if err := migrateWorkspaceSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate workspace schema: %w", err)
	}

	return &WorkspaceDB{
		DB:   db,
		Path: dbPath,
		Dir:  dir,
	}, nil
}

// migrateWorkspaceSchema adds workspace-level tables that don't belong in
// the session database. This runs alongside the sessiondb migration.
func migrateWorkspaceSchema(db *sql.DB) error {
	// Workspace metadata: key-value store for workspace-level settings
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS workspace_metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch() * 1000000000)
		)
	`)
	if err != nil {
		return err
	}

	// Session registry: tracks all AI sessions that have run in this workspace.
	// This is the workspace's view of sessions — each session also has its own DB.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS workspace_sessions (
			session_id    TEXT PRIMARY KEY,
			project_dir   TEXT NOT NULL,
			project_name  TEXT NOT NULL DEFAULT '',
			model         TEXT NOT NULL DEFAULT '',
			provider      TEXT NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			closed_at     INTEGER NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			turn_count    INTEGER NOT NULL DEFAULT 0,
			first_prompt  TEXT,
			last_prompt   TEXT,
			total_cost    REAL NOT NULL DEFAULT 0.0
		)
	`)
	return err
}

// GetSetting retrieves a workspace-level setting by key.
// Returns empty string if not found.
func (wdb *WorkspaceDB) GetSetting(key string) string {
	var val string
	row := wdb.DB.QueryRow(`SELECT value FROM workspace_metadata WHERE key = ?`, key)
	_ = row.Scan(&val)
	return val
}

// SetSetting stores a workspace-level setting as a key-value pair.
// Updates the timestamp on every write.
func (wdb *WorkspaceDB) SetSetting(key, value string) error {
	_, err := wdb.DB.Exec(`
		INSERT INTO workspace_metadata (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, time.Now().UnixNano())
	return err
}

// RegisterSession records a new AI session in the workspace's session registry.
// Called when a new session is created within this workspace.
func (wdb *WorkspaceDB) RegisterSession(sessionID, projectPath, projectName, model, provider string) error {
	_, err := wdb.DB.Exec(`
		INSERT OR IGNORE INTO workspace_sessions (session_id, project_dir, project_name, model, provider, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sessionID, projectPath, projectName, model, provider, time.Now().UnixNano())
	return err
}

// CloseSession marks a session as closed in the workspace registry.
func (wdb *WorkspaceDB) CloseSession(sessionID string) error {
	_, err := wdb.DB.Exec(`
		UPDATE workspace_sessions SET closed_at = ? WHERE session_id = ?
	`, time.Now().UnixNano(), sessionID)
	return err
}

// UpdateSessionMeta updates the live session metadata for a given session.
// This is called periodically to keep the workspace registry in sync with
// the per-session database.
func (wdb *WorkspaceDB) UpdateSessionMeta(sessionID string, turnCount, messageCount int, firstPrompt, lastPrompt string, totalCost float64) error {
	_, err := wdb.DB.Exec(`
		UPDATE workspace_sessions SET
			turn_count = ?, message_count = ?,
			first_prompt = ?, last_prompt = ?,
			total_cost = ?
		WHERE session_id = ?
	`, turnCount, messageCount, firstPrompt, lastPrompt, totalCost, sessionID)
	return err
}

// UpdateSessionCost performs a lightweight live update of the cumulative
// session cost only. This is the push path: the workspace already tracks
// SessionCost in memory (updated by RecordAgentUsage) and pushes it here
// without re-opening the per-session database.
func (wdb *WorkspaceDB) UpdateSessionCost(sessionID string, totalCost float64) error {
	_, err := wdb.DB.Exec(`
		UPDATE workspace_sessions SET total_cost = ? WHERE session_id = ?
	`, totalCost, sessionID)
	return err
}

// ListSessions returns all sessions registered in this workspace, most recent first.
func (wdb *WorkspaceDB) ListSessions() ([]sessiondb.SessionMeta, error) {
	rows, err := wdb.DB.Query(`
		SELECT session_id, project_dir, project_name, model, provider,
		       created_at, closed_at, message_count, turn_count,
		       first_prompt, last_prompt
		FROM workspace_sessions
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []sessiondb.SessionMeta
	for rows.Next() {
		var m sessiondb.SessionMeta
		if err := rows.Scan(
			&m.ID, &m.ProjectPath, &m.ProjectName, &m.Model, &m.Provider,
			&m.CreatedAt, &m.ClosedAt, &m.MessageCount, &m.TurnCount,
			&m.FirstPrompt, &m.LastPrompt,
		); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

// SyncSessionMeta reads metadata from the per-session DB and writes it back
// to the workspace_sessions registry row, keeping the registry self-consistent
// without requiring enrichment on every ListSessions call.
//
// This is the pull path: it reads the session DB (the source of truth for
// turn_count, message_count, first/last prompt, and cumulative cost) and
// mirrors the values into the workspace registry. Used at session close and
// during enrichment. For live cost updates during a running session, prefer
// UpdateSessionCost (the push path) to avoid re-opening the session DB.
func (wdb *WorkspaceDB) SyncSessionMeta(projectDir, sessionID string) error {
	if wdb == nil || wdb.DB == nil {
		return nil
	}
	dbPath, err := SessionDBPath(projectDir, sessionID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return err
	}
	defer sdb.Close()
	return wdb.syncSessionMetaFromDB(sdb, sessionID)
}

// syncSessionMetaFromDB is the testable core of SyncSessionMeta: it reads
// metadata + cumulative cost from an already-open session DB and writes the
// merged row back to the workspace_sessions registry. Split out so tests can
// exercise the read/write contract without going through the workspace
// folder layout.
func (wdb *WorkspaceDB) syncSessionMetaFromDB(sdb *sql.DB, sessionID string) error {
	meta, err := sessiondb.GetMeta(sdb)
	if err != nil {
		return err
	}
	totalCost, err := sessiondb.GetCumulativeCost(sdb)
	if err != nil {
		return err
	}
	return wdb.UpdateSessionMeta(sessionID, meta.TurnCount, meta.MessageCount,
		nullStr(&meta.FirstPrompt), nullStr(&meta.LastPrompt), totalCost)
}

// Close shuts down the workspace database, flushing any pending writes.
func (wdb *WorkspaceDB) Close() error {
	if wdb.DB != nil {
		return wdb.DB.Close()
	}
	return nil
}
