package workspace

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crude/session"
	"crude/sessiondb"
)

// BackfillSessionRegistry reconstructs missing workspace_sessions rows from
// session database files present on disk.
//
// Discovery (in order, de-duplicated by session ID):
//  1. ~/.crude/workspaces/<hash>/sessions/<id>.db  (canonical workspace layout)
//  2. ~/.crude/sessions/<id>.db                     (legacy root), only when
//     session meta.project_path matches this workspace's RootPath
//
// Idempotent: existing registry rows are never overwritten with derived data.
// Rows marked db_missing whose DB reappears are unmarked only. Corrupt or
// unreadable session DBs are skipped with a warning; the backfill never fails
// the caller's open path.
//
// Called from NewWorkspace (workspace-open), not from the launcher's
// read-only ListWorkspaceInfos path, so listing other projects stays
// time-bounded and non-mutating.
func (w *Workspace) BackfillSessionRegistry() int {
	if w == nil || w.WorkspaceDB == nil || w.WorkspaceDB.DB == nil {
		return 0
	}
	paths := w.discoverSessionDBPaths()
	inserted := 0
	for _, dbPath := range paths {
		n, err := w.backfillOneSessionDB(dbPath)
		if err != nil {
			if w.Logger != nil {
				w.Logger.Warn("WORKSPACE", "registry_backfill_skip",
					"path", dbPath, "error", err.Error())
			}
			continue
		}
		inserted += n
	}
	if inserted > 0 && w.Logger != nil {
		w.Logger.Info("WORKSPACE", "registry_backfill_done",
			"inserted", inserted, "scanned", len(paths))
	}
	return inserted
}

// discoverSessionDBPaths returns candidate session DB paths for this workspace.
func (w *Workspace) discoverSessionDBPaths() []string {
	seen := make(map[string]bool)
	var out []string

	add := func(path string) {
		id := strings.TrimSuffix(filepath.Base(path), ".db")
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, path)
	}

	if w.WorkspaceDir != "" {
		local := filepath.Join(w.WorkspaceDir, "sessions")
		matches, err := filepath.Glob(filepath.Join(local, "*.db"))
		if err == nil {
			for _, p := range matches {
				add(p)
			}
		}
	}

	rootPath := canonicalizePath(w.RootPath)
	if legacyRoot, err := session.SessionsRoot(); err == nil {
		matches, gerr := filepath.Glob(filepath.Join(legacyRoot, "*.db"))
		if gerr == nil {
			for _, p := range matches {
				id := strings.TrimSuffix(filepath.Base(p), ".db")
				if id == "" || seen[id] {
					continue
				}
				meta, merr := readSessionMetaReadonly(p)
				if merr != nil {
					continue
				}
				if canonicalizePath(meta.ProjectPath) != rootPath {
					continue
				}
				add(p)
			}
		}
	}
	return out
}

func canonicalizePath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}

// backfillOneSessionDB inserts a registry row if missing. Returns 1 if inserted.
func (w *Workspace) backfillOneSessionDB(dbPath string) (int, error) {
	fi, err := os.Stat(dbPath)
	if err != nil {
		return 0, err
	}
	meta, err := readSessionMetaReadonly(dbPath)
	if err != nil {
		return 0, err
	}
	sid := meta.ID
	if sid == "" {
		sid = strings.TrimSuffix(filepath.Base(dbPath), ".db")
	}
	if sid == "" {
		return 0, fmt.Errorf("empty session id")
	}

	// Existing row: never overwrite richer data. Only clear db_missing if set.
	var missing int
	err = w.WorkspaceDB.DB.QueryRow(
		`SELECT db_missing FROM workspace_sessions WHERE session_id = ?`, sid,
	).Scan(&missing)
	switch {
	case err == nil:
		if missing != 0 {
			_ = w.WorkspaceDB.MarkSessionDBMissing(sid, false)
		}
		return 0, nil
	case err != sql.ErrNoRows:
		return 0, err
	}

	totalCost, _ := readCumulativeCostReadonly(dbPath)
	row := deriveRegistryRow(meta, sid, w.RootPath, fi.ModTime(), totalCost)
	if err := w.WorkspaceDB.insertRegistryRow(row); err != nil {
		return 0, err
	}
	return 1, nil
}

// registryInsert holds a fully-derived workspace_sessions row for backfill.
type registryInsert struct {
	SessionID    string
	ProjectDir   string
	ProjectName  string
	Model        string
	Provider     string
	CreatedAt    int64
	ClosedAt     int64
	MessageCount int
	TurnCount    int
	FirstPrompt  string
	LastPrompt   string
	TotalCost    float64
}

// deriveRegistryRow maps session DB meta (+ fallbacks) into a registry row.
//
// Field derivations:
//   - session_id: SessionMeta.ID; else basename of the .db file (caller)
//   - project_dir: meta.ProjectPath if non-empty; else workspace RootPath
//   - project_name: meta.ProjectName if non-empty; else filepath.Base(project_dir)
//   - model / provider: from meta (may be empty — session DB is source of truth)
//   - created_at: meta.CreatedAt if > 0; else file mtime as UnixNano (last resort)
//   - closed_at: meta.ClosedAt when Valid; else 0
//   - message_count / turn_count / first_prompt / last_prompt: from meta
//   - total_cost: latest cost_snapshots.total_cost, or 0
//   - db_missing: always 0 on insert (DB was just read successfully)
func deriveRegistryRow(meta sessiondb.SessionMeta, sid, rootPath string, mtime time.Time, totalCost float64) registryInsert {
	projectDir := meta.ProjectPath
	if projectDir == "" {
		projectDir = rootPath
	}
	projectName := meta.ProjectName
	if projectName == "" {
		projectName = filepath.Base(projectDir)
	}
	created := meta.CreatedAt
	if created <= 0 {
		created = mtime.UnixNano()
	}
	var closed int64
	if meta.ClosedAt.Valid {
		closed = meta.ClosedAt.Int64
	}
	return registryInsert{
		SessionID:    sid,
		ProjectDir:   projectDir,
		ProjectName:  projectName,
		Model:        meta.Model,
		Provider:     meta.Provider,
		CreatedAt:    created,
		ClosedAt:     closed,
		MessageCount: meta.MessageCount,
		TurnCount:    meta.TurnCount,
		FirstPrompt:  nullStr(&meta.FirstPrompt),
		LastPrompt:   nullStr(&meta.LastPrompt),
		TotalCost:    totalCost,
	}
}

func (wdb *WorkspaceDB) insertRegistryRow(r registryInsert) error {
	if wdb == nil || wdb.DB == nil {
		return fmt.Errorf("workspace db not open")
	}
	_, err := wdb.DB.Exec(`
		INSERT OR IGNORE INTO workspace_sessions (
			session_id, project_dir, project_name, model, provider,
			created_at, closed_at, message_count, turn_count,
			first_prompt, last_prompt, total_cost, db_missing
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, r.SessionID, r.ProjectDir, r.ProjectName, r.Model, r.Provider,
		r.CreatedAt, r.ClosedAt, r.MessageCount, r.TurnCount,
		nullIfEmpty(r.FirstPrompt), nullIfEmpty(r.LastPrompt), r.TotalCost)
	if err != nil {
		return fmt.Errorf("insert registry row %s: %w", r.SessionID, err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// readSessionMetaReadonly opens a session DB read-only and returns GetMeta.
// Does not migrate or write.
func readSessionMetaReadonly(dbPath string) (sessiondb.SessionMeta, error) {
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_busy_timeout=1000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return sessiondb.SessionMeta{}, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	return sessiondb.GetMeta(db)
}

func readCumulativeCostReadonly(dbPath string) (float64, error) {
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_busy_timeout=1000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	return sessiondb.GetCumulativeCost(db)
}
