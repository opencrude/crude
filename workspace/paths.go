// Package workspace is the per-project state machine for the Crude IDE.
//
// One Workspace instance represents a single IDE session rooted at a project
// directory. It owns everything that is scoped to that project:
//
// - AI agent sessions and orchestrators
// - Editor buffers and tab state
// - LSP server connections and diagnostics cache
// - File tree model
// - Workspace-specific settings (inheriting from app config)
// - Per-workspace SQLite database
//
// Workspace follows the JetBrains model: each project gets a dedicated folder
// under ~/.crude/workspaces/<hash>/, identified by a SHA-256 hash of the
// absolute project directory path. This folder contains the workspace database,
// session data, and any workspace-specific configuration.
//
// Communication with the TUI layer happens via two mechanisms:
// - TUI → Workspace: direct method calls (e.g., ws.OpenFile(path))
// - Workspace → TUI: event bus publishes (e.g., bus.Publish(FileOpened{path}))
//
// The workspace package intentionally has NO knowledge of rendering or input
// handling — those concerns live in the tui/ layer.
package workspace

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crude/buildinfo"

	_ "modernc.org/sqlite"
)

// workspaceID computes a stable, filesystem-safe identifier for a project
// directory by taking the first 16 hex chars of its SHA-256 hash.
// The same directory always produces the same ID, regardless of symlinks
// or trailing slashes (we canonicalize first).
func workspaceID(projectDir string) string {
	// Canonicalize to absolute path to avoid hash collisions from
	// relative paths or trailing slashes.
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = projectDir
	}
	h := sha256.Sum256([]byte(abs))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

// WorkspaceRoot returns the base directory for all workspace folders.
// Layout: ~/.crude/workspaces/
func WorkspaceRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, buildinfo.DataDir(), "workspaces"), nil
}

// WorkspaceDir returns the dedicated folder for a specific project.
// Layout: ~/.crude/workspaces/<hash>/
// The folder is created if it doesn't exist.
func WorkspaceDir(projectDir string) (string, error) {
	root, err := WorkspaceRoot()
	if err != nil {
		return "", err
	}
	id := workspaceID(projectDir)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	return dir, nil
}

// WorkspaceDBPath returns the path to the workspace-level SQLite database.
// Layout: ~/.crude/workspaces/<hash>/workspace.db
func WorkspaceDBPath(projectDir string) (string, error) {
	dir, err := WorkspaceDir(projectDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspace.db"), nil
}

// SessionDir returns the dedicated directory for a single session inside
// a workspace. Layout: ~/.crude/workspaces/<hash>/sessions/<sessionID>/
func SessionDir(projectDir, sessionID string) (string, error) {
	wsDir, err := WorkspaceDir(projectDir)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(wsDir, "sessions", sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create session dir: %w", err)
	}
	return dir, nil
}

// SessionDBPath returns the path to a session database inside a workspace.
// Layout: ~/.crude/workspaces/<hash>/sessions/<sessionID>.db
func SessionDBPath(projectDir, sessionID string) (string, error) {
	wsDir, err := WorkspaceDir(projectDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(wsDir, "sessions", sessionID+".db"), nil
}

// WorkspaceInfo describes a known workspace for the launcher selector.
type WorkspaceInfo struct {
	ID           string // hash directory name
	ProjectDir   string // absolute project path (best-effort)
	ProjectName  string // display name (basename of project dir)
	SessionCount int
	LastActive   int64 // unix nano of most recent session, 0 if unknown
}

// ListWorkspaces enumerates all workspace folders under ~/.crude/workspaces/
// and returns their directory names (the hashes). This is used by the app
// layer to present a list of known workspaces for resumption.
func ListWorkspaces() ([]string, error) {
	root, err := WorkspaceRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// ListWorkspaceInfos enumerates workspaces with display metadata for the
// launcher. Best-effort: missing/corrupt/locked DBs still produce an entry
// with the hash as the name and never block the UI for long.
func ListWorkspaceInfos() ([]WorkspaceInfo, error) {
	ids, err := ListWorkspaces()
	if err != nil {
		return nil, err
	}
	root, err := WorkspaceRoot()
	if err != nil {
		return nil, err
	}
	infos := make([]WorkspaceInfo, 0, len(ids))
	for _, id := range ids {
		info := WorkspaceInfo{ID: id, ProjectName: id}
		dbPath := filepath.Join(root, id, "workspace.db")
		if meta := readWorkspaceMetaBounded(dbPath, 150*time.Millisecond); meta != nil {
			info = *meta
			info.ID = id
			if info.ProjectName == "" {
				info.ProjectName = id
			}
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// readWorkspaceMetaBounded is readWorkspaceMeta with a hard wall-clock limit so
// a locked/corrupt workspace.db cannot stall the launcher or tests.
func readWorkspaceMetaBounded(dbPath string, limit time.Duration) *WorkspaceInfo {
	type result struct{ info *WorkspaceInfo }
	ch := make(chan result, 1)
	go func() {
		ch <- result{info: readWorkspaceMeta(dbPath)}
	}()
	select {
	case r := <-ch:
		return r.info
	case <-time.After(limit):
		return nil
	}
}

// readWorkspaceMeta opens a workspace.db read-only and extracts project info.
// It must not migrate or otherwise write — listing known workspaces from the
// launcher must not mutate ~/.crude (including other projects' DBs).
func readWorkspaceMeta(dbPath string) *WorkspaceInfo {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	// Short busy timeout + read-only: a live IDE holding the WAL must not stall
	// the launcher (or unit tests) for the default 5s sessiondb busy wait.
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&immutable=1&_busy_timeout=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	wdb := &WorkspaceDB{DB: db, Path: dbPath, Dir: filepath.Dir(dbPath)}

	info := &WorkspaceInfo{}
	// Prefer an explicit metadata key if present.
	if p := wdb.GetSetting("project_dir"); p != "" {
		info.ProjectDir = p
		info.ProjectName = filepath.Base(p)
	}
	// Fall back to the most recent session's project_dir. Prefer project_name
	// when the column exists; legacy registries omit it.
	cols, colErr := tableColumns(db, "workspace_sessions")
	if colErr != nil {
		// Locked/corrupt DB — still return a stub entry from the folder name.
		return info
	}
	var rows *sql.Rows
	var qerr error
	if cols["project_name"] {
		rows, qerr = db.Query(`
			SELECT project_dir, project_name, created_at
			FROM workspace_sessions
			ORDER BY created_at DESC
			LIMIT 1
		`)
	} else {
		rows, qerr = db.Query(`
			SELECT project_dir, '' AS project_name, created_at
			FROM workspace_sessions
			ORDER BY created_at DESC
			LIMIT 1
		`)
	}
	if qerr == nil {
		defer rows.Close()
		if rows.Next() {
			var dir, name string
			var created int64
			if rows.Scan(&dir, &name, &created) == nil {
				if info.ProjectDir == "" {
					info.ProjectDir = dir
				}
				if info.ProjectName == "" || info.ProjectName == info.ID {
					if name != "" {
						info.ProjectName = name
					} else if dir != "" {
						info.ProjectName = filepath.Base(dir)
					}
				}
				info.LastActive = created
			}
		}
	}
	// Session count (exclude rows whose session DB was marked unavailable).
	if cols["db_missing"] {
		_ = db.QueryRow(`SELECT COUNT(*) FROM workspace_sessions WHERE db_missing = 0`).Scan(&info.SessionCount)
	} else {
		_ = db.QueryRow(`SELECT COUNT(*) FROM workspace_sessions`).Scan(&info.SessionCount)
	}
	if info.ProjectName == "" {
		info.ProjectName = info.ID
	}
	return info
}

// DeleteWorkspace removes a workspace folder and all its sessions.
// projectDirOrID may be an absolute project path (hash is derived) or a
// workspace hash ID directly.
func DeleteWorkspace(projectDirOrID string) error {
	root, err := WorkspaceRoot()
	if err != nil {
		return err
	}
	id := projectDirOrID
	// If it looks like a path, hash it.
	if strings.Contains(projectDirOrID, string(os.PathSeparator)) || strings.HasPrefix(projectDirOrID, "/") {
		id = workspaceID(projectDirOrID)
	}
	dir := filepath.Join(root, id)
	return os.RemoveAll(dir)
}
