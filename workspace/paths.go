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
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/buildinfo"

	_ "modernc.org/sqlite"
)

// WorkspaceID returns the stable 16-hex workspace folder name for a project
// directory (SHA-256 of the absolute path, first 8 bytes).
func WorkspaceID(projectDir string) string {
	return workspaceID(projectDir)
}

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

// LocalHistoryDir returns the external per-workspace history store. It is
// deliberately outside the project tree so snapshots never create repository
// artifacts or trigger project watchers.
func LocalHistoryDir(projectDir string) (string, error) {
	dir, err := WorkspaceDir(projectDir)
	if err != nil {
		return "", err
	}
	historyDir := filepath.Join(dir, "local-history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		return "", fmt.Errorf("create local history dir: %w", err)
	}
	return historyDir, nil
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
	ProjectName  string // display name (basename of project dir); never the raw hash
	SessionCount int
	LastActive   int64 // unix nano of most recent session, 0 if unknown
}

// WorkspaceDisplayName returns a human-readable label for a workspace row.
// Prefer a stored ProjectName, then the project directory basename. Never
// returns the raw workspace hash id — empty/unknown metadata yields a
// stable placeholder instead.
func WorkspaceDisplayName(info WorkspaceInfo) string {
	name := strings.TrimSpace(info.ProjectName)
	if name != "" && name != info.ID {
		return name
	}
	if dir := strings.TrimSpace(info.ProjectDir); dir != "" {
		base := filepath.Base(dir)
		if base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return "unnamed workspace"
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
// and never block the UI for long. Display names are always human-readable
// (path basename or "unnamed workspace") — never the raw hash id.
func ListWorkspaceInfos() ([]WorkspaceInfo, error) {
	ids, err := ListWorkspaces()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	root, err := WorkspaceRoot()
	if err != nil {
		return nil, err
	}
	type workspaceResult struct {
		id   string
		info *WorkspaceInfo
	}
	jobs := make(chan string)
	results := make(chan workspaceResult, len(ids))
	workerCount := min(24, len(ids))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for id := range jobs {
				dbPath := filepath.Join(root, id, "workspace.db")
				results <- workspaceResult{id: id, info: readWorkspaceMetaBounded(dbPath, 150*time.Millisecond)}
			}
		}()
	}
	go func() {
		for _, id := range ids {
			jobs <- id
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	byPath := make(map[string]WorkspaceInfo, len(ids))
	for result := range results {
		info := WorkspaceInfo{ID: result.id}
		if result.info != nil {
			info = *result.info
			info.ID = result.id
		}
		projectDir := canonicalizePath(info.ProjectDir)
		if projectDir == "" {
			continue
		}
		stat, statErr := os.Stat(projectDir)
		if statErr != nil || !stat.IsDir() {
			// Recent workspaces are actionable resources, not tombstones. Keep
			// their DBs on disk for recovery but omit paths that no longer exist.
			continue
		}
		info.ProjectDir = projectDir
		info.ProjectName = WorkspaceDisplayName(info)
		if existing, ok := byPath[projectDir]; !ok || info.LastActive > existing.LastActive {
			byPath[projectDir] = info
		}
	}
	infos := make([]WorkspaceInfo, 0, len(byPath))
	for _, info := range byPath {
		infos = append(infos, info)
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].LastActive != infos[j].LastActive {
			return infos[i].LastActive > infos[j].LastActive
		}
		return infos[i].ProjectName < infos[j].ProjectName
	})
	return infos, nil
}

// readWorkspaceMetaBounded is readWorkspaceMeta with a hard wall-clock limit so
// a locked/corrupt workspace.db cannot stall the launcher or tests.
//
// Both the read AND the goroutine's channel send are bound to the same
// context: if the read outlives the limit, the query is cancelled (releasing
// the DB handle), the send aborts, and the goroutine exits — it can never
// leak an open sqlite handle (which would keep the DB file locked on
// Windows) past the timeout.
func readWorkspaceMetaBounded(dbPath string, limit time.Duration) *WorkspaceInfo {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	type result struct{ info *WorkspaceInfo }
	ch := make(chan result, 1)
	go func() {
		select {
		case ch <- result{info: readWorkspaceMeta(ctx, dbPath)}:
		case <-ctx.Done():
		}
	}()
	select {
	case r := <-ch:
		return r.info
	case <-ctx.Done():
		// Timed out — try a metadata-only fast path that avoids scanning
		// workspace_sessions (the usual stall under a live writer lock).
		return readWorkspaceMetaLight(dbPath)
	}
}

// readWorkspaceMetaLight reads only workspace_metadata.project_dir so the
// launcher can still show a path-derived name when the full meta read stalls.
func readWorkspaceMetaLight(dbPath string) *WorkspaceInfo {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_busy_timeout=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()

	var dir string
	if err := db.QueryRow(`SELECT value FROM workspace_metadata WHERE key = 'project_dir'`).Scan(&dir); err != nil || dir == "" {
		return nil
	}
	return &WorkspaceInfo{
		ProjectDir:  dir,
		ProjectName: filepath.Base(dir),
	}
}

// readWorkspaceMeta opens a workspace.db read-only and extracts project info.
// It must not migrate or otherwise write — listing known workspaces from the
// launcher must not mutate ~/.crude (including other projects' DBs).
// All queries are context-bound so a stalled read can be cancelled (which
// also releases the file handle; see readWorkspaceMetaBounded).
func readWorkspaceMeta(ctx context.Context, dbPath string) *WorkspaceInfo {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	// Short busy timeout + read-only: a live IDE holding the WAL must not stall
	// the launcher (or unit tests) for the default 5s sessiondb busy wait.
	// Avoid immutable=1 so recent WAL-backed project_dir writes remain visible.
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_busy_timeout=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	wdb := &WorkspaceDB{DB: db, Path: dbPath, Dir: filepath.Dir(dbPath)}

	info := &WorkspaceInfo{}
	// Prefer an explicit metadata key if present.
	if p := wdb.GetSettingContext(ctx, "project_dir"); p != "" {
		info.ProjectDir = p
		info.ProjectName = filepath.Base(p)
	}
	// Fall back to the most recent session's project_dir. Prefer project_name
	// when the column exists; legacy registries omit it.
	cols, colErr := tableColumnsContext(ctx, db, "workspace_sessions")
	if colErr != nil {
		// Locked/corrupt DB — still return a stub entry from the folder name.
		return info
	}
	var rows *sql.Rows
	var qerr error
	if cols["project_name"] {
		rows, qerr = db.QueryContext(ctx, `
			SELECT project_dir, project_name, created_at
			FROM workspace_sessions
			ORDER BY created_at DESC
			LIMIT 1
		`)
	} else {
		rows, qerr = db.QueryContext(ctx, `
			SELECT project_dir, '' AS project_name, created_at
			FROM workspace_sessions
			ORDER BY created_at DESC
			LIMIT 1
		`)
	}
	if qerr == nil {
		if rows.Next() {
			var dir, name string
			var created int64
			if rows.Scan(&dir, &name, &created) == nil {
				if info.ProjectDir == "" {
					info.ProjectDir = dir
				}
				if info.ProjectName == "" {
					if name != "" {
						info.ProjectName = name
					} else if dir != "" {
						info.ProjectName = filepath.Base(dir)
					}
				}
				info.LastActive = created
			}
		}
		// Close BEFORE the count query: the pool is capped at one
		// connection, and an open Rows holds it — the count would wait on
		// itself (self-deadlock) until the deferred Close runs.
		_ = rows.Close()
	}
	// Session count (exclude rows whose session DB was marked unavailable).
	if cols["db_missing"] {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_sessions WHERE db_missing = 0`).Scan(&info.SessionCount)
	} else {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_sessions`).Scan(&info.SessionCount)
	}
	info.ProjectName = WorkspaceDisplayName(*info)
	return info
}
