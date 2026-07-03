package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crude/tui/editor"
)

// clientKey returns a unique key for a rootURI+cmd pair, allowing
// multiple LSP servers (one per language) in the same project root.
func clientKey(rootURI, cmd string) string { return rootURI + "|" + cmd }

// LSPManager owns all LSP server connections for the application.
// It lives for the full app lifetime, maintains diagnostics for every
// file in the project (not just open tabs), and routes them to the
// editor / file tree / status bar.
type LSPManager struct {
	mu      sync.Mutex
	clients map[string]*editor.LSPIntegration // keyed by clientKey(rootURI, cmd)
	logger  Logger

	activeFilePath string
	pendingText    map[string]string // latest text for files pending async start

	onDiagnostics func(filePath string, diags []editor.LSPDiagnostic)

	activeRootURI   string
	activeConnected bool
	activeError     string
	activeStatus    editor.LSPStatus

	// Project-wide state
	fileDiagnostics map[string][]editor.LSPDiagnostic // cached per file; survives tab close
	docSymbols      map[string][]editor.LSPSymbol     // per-file document outline cache
	fileLastSync    map[string]int64                  // per-file last diagnostic update (unix nanos)
	issueCount      map[string]int                    // diagnostic message -> occurrence count, for "top issue"
	symbolsCached   bool                              // true once at least one file has been outlined

	// syncing is true while the server is doing a bulk operation (e.g. opening
	// every project file after startup, or processing a flurry of didChange
	// notifications). The status bar shows the dot animating while this is
	// true so the user can see "the LSP is working" without waiting for a
	// specific request to be in flight.
	syncing bool

	// lastBusyAt is the UnixNano of the most recent non-idle event
	// (starting, syncing, busy, new diagnostics). The status bar uses
	// this to keep the dot animated for AnimGrace after the last
	// activity, even if the instantaneous state is "ready".
	lastBusyAt int64

	// In-flight request tracking (drives the busy indicator on the status bar).
	inflight int
}

func NewLSPManager(logger Logger) *LSPManager {
	return &LSPManager{
		clients:         make(map[string]*editor.LSPIntegration),
		logger:          logger,
		pendingText:     make(map[string]string),
		fileDiagnostics: make(map[string][]editor.LSPDiagnostic),
		docSymbols:      make(map[string][]editor.LSPSymbol),
		fileLastSync:    make(map[string]int64),
		issueCount:      make(map[string]int),
	}
}

// StartForWorkspace starts LSP servers for all supported project roots
// found under workspaceDir.  This is called after the session picker
// so the server is ready before any file is opened.
func (m *LSPManager) StartForWorkspace(workspaceDir string) {
	roots := m.detectProjectRoots(workspaceDir)
	for _, root := range roots {
		m.startServerForRoot(root)
	}
}

// detectProjectRoots scans workspaceDir for project markers and returns
// the directories that should have LSP servers started.
func (m *LSPManager) detectProjectRoots(workspaceDir string) []string {
	var roots []string
	dir := workspaceDir
	for _, marker := range []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "composer.json"} {
		path := filepath.Join(dir, marker)
		if _, err := os.Stat(path); err == nil {
			roots = append(roots, "file://"+dir)
			break
		}
	}
	if len(roots) == 0 {
		roots = append(roots, "file://"+dir)
	}
	return roots
}

// startServerForRoot starts an LSP server for each language that has
// at least one source file in the project.  This avoids launching
// servers for languages the project doesn't use.
func (m *LSPManager) startServerForRoot(rootURI string) {
	rootPath := strings.TrimPrefix(rootURI, "file://")
	if rootPath == "" {
		return
	}

	// Scan the project once to find which extensions actually exist.
	allExts := []string{".go", ".rs", ".py", ".ts", ".js", ".tsx", ".jsx", ".css", ".json"}
	present := scanPresentExtensions(rootPath, allExts)

	// Build reverse map: cmd -> extensions it handles.
	type cmdEntry struct {
		cmd  string
		args []string
		exts []string
	}
	cmdMap := map[string]*cmdEntry{}
	for _, ext := range allExts {
		cmd, args, ok := editor.ServerConfigForExt(ext)
		if !ok {
			continue
		}
		if e, exists := cmdMap[cmd]; exists {
			e.exts = append(e.exts, ext)
		} else {
			cmdMap[cmd] = &cmdEntry{cmd: cmd, args: args, exts: []string{ext}}
		}
	}

	for _, entry := range cmdMap {
		// Only start this server if at least one of its file extensions
		// was found in the project.
		hasFiles := false
		for _, ext := range entry.exts {
			if present[ext] {
				hasFiles = true
				break
			}
		}
		if !hasFiles {
			continue
		}

		client := editor.NewLSPIntegration(entry.cmd, entry.args, rootURI)
		client.OnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
			m.setFileDiagnostics(fp, diags)
			if cb := m.captureOnDiagnostics(); cb != nil {
				cb(fp, diags)
			}
		})

		key := clientKey(rootURI, entry.cmd)
		m.mu.Lock()
		if _, exists := m.clients[key]; exists {
			m.mu.Unlock()
			continue
		}
		m.clients[key] = client
		m.activeStatus = editor.LSPStarting
		m.activeRootURI = rootURI
		m.lastBusyAt = time.Now().UnixNano()
		m.mu.Unlock()

		go m.startClient(client, entry.cmd)
	}
}

// scanPresentExtensions walks rootPath and returns a set of file extensions
// found among source files, skipping dependency/build directories.
func scanPresentExtensions(rootPath string, candidates []string) map[string]bool {
	present := make(map[string]bool, len(candidates))
	for _, e := range candidates {
		present[e] = false
	}

	skipDirs := map[string]bool{
		".git":              true,
		"node_modules":      true,
		"target":            true,
		"vendor":            true,
		".venv":             true,
		"venv":              true,
		"__pycache__":       true,
		".next":             true,
		"dist":              true,
		"build":             true,
		".cache":            true,
		".terraform":        true,
		"bower_components":  true,
	}

	_ = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() != filepath.Base(rootPath) && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if _, ok := present[ext]; ok {
				present[ext] = true
				// Short-circuit once all candidates are found.
				allFound := true
				for _, v := range present {
					if !v {
						allFound = false
						break
					}
				}
				if allFound {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	// Filter to only those present.
	result := make(map[string]bool)
	for ext, found := range present {
		if found {
			result[ext] = true
		}
	}
	return result
}

// clientForFile returns the client handling a given file path based on
// its root URI and file extension.
func (m *LSPManager) clientForFile(filePath string) *editor.LSPIntegration {
	rootURI := editor.RootURIForFile(filePath)
	ext := strings.ToLower(filepath.Ext(filePath))
	cmd, _, ok := editor.ServerConfigForExt(ext)
	if !ok {
		return nil
	}
	key := clientKey(rootURI, cmd)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clients[key]
}

// clientsForRootURI returns all clients whose key begins with rootURI.
func (m *LSPManager) clientsForRootURI(rootURI string) []*editor.LSPIntegration {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*editor.LSPIntegration
	for k, c := range m.clients {
		if strings.HasPrefix(k, rootURI+"|") {
			out = append(out, c)
		}
	}
	return out
}

func (m *LSPManager) SetOnDiagnostics(fn func(filePath string, diags []editor.LSPDiagnostic)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDiagnostics = fn
}

// OpenFile registers a file with the LSP server.  The server must already
// be running (StartForWorkspace should have been called).
func (m *LSPManager) OpenFile(ctx context.Context, filePath string, text string) {
	m.mu.Lock()
	m.activeFilePath = filePath
	m.pendingText[filePath] = text
	ext := strings.ToLower(filepath.Ext(filePath))
	rootURI := editor.RootURIForFile(filePath)

	client := m.clients[clientKey(rootURI, editor.MustServerCmd(ext))]
	if client == nil {
		m.mu.Unlock()
		return
	}

	m.activeRootURI = rootURI
	m.activeError = ""
	m.mu.Unlock()

	langID := editor.LanguageID(ext)
	client.OpenFile(ctx, filePath, langID, text)
}

// startClient creates its own context so the caller's short-lived context
// (which may already be cancelled) does not kill the LSP handshake.
// After the server connects it opens every source file in the project.
func (m *LSPManager) startClient(client *editor.LSPIntegration, cmd string) {
	startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := exec.LookPath(cmd); err != nil {
		m.removeClient(client)
		m.mu.Lock()
		m.activeStatus = editor.LSPFailed
		m.activeError = cmd + " not found in PATH"
		m.activeConnected = false
		m.mu.Unlock()
		return
	}

	if err := client.Start(startCtx); err != nil {
		m.removeClient(client)
		m.mu.Lock()
		m.activeStatus = editor.LSPFailed
		m.activeError = cmd + " failed to start: " + err.Error()
		m.activeConnected = false
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.activeStatus = editor.LSPConnected
	m.activeConnected = true
	m.activeError = ""
	m.lastBusyAt = time.Now().UnixNano()
	rootURI := m.activeRootURI
	m.mu.Unlock()

	// Open every source file in the project so the server reports
	// diagnostics for the entire workspace, not just open tabs. The
	// syncing flag tells the status bar to animate the dot while the
	// server is chewing through hundreds of didOpen notifications.
	m.setSyncing(true)
	m.openProjectFiles(rootURI, cmd)
	m.setSyncing(false)
}

// openProjectFiles scans the project root for files handled by the given
// server command and sends didOpen for each one.
func (m *LSPManager) openProjectFiles(rootURI string, cmd string) {
	rootPath := strings.TrimPrefix(rootURI, "file://")
	if rootPath == "" {
		return
	}

	// Determine which extensions this server handles.
	var exts []string
	for ext, cfg := range editor.AllServerConfigs() {
		if cfg.Cmd == cmd {
			exts = append(exts, ext)
		}
	}
	if len(exts) == 0 {
		return
	}

	extSet := make(map[string]bool, len(exts))
	for _, e := range exts {
		extSet[e] = true
	}

	files := scanProjectFiles(rootPath, extSet)

	// Find the right client.
	key := clientKey(rootURI, cmd)
	m.mu.Lock()
	client := m.clients[key]
	m.mu.Unlock()
	if client == nil {
		return
	}

	// Batch-open files with limited concurrency so we don't flood the
	// server process with hundreds of simultaneous notifications.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for _, fp := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ext := strings.ToLower(filepath.Ext(f))
			langID := editor.LanguageID(ext)
			if langID == "" {
				return
			}

			// Use pending text if available (e.g. an active editor tab),
			// otherwise read from disk.
			m.mu.Lock()
			text, pending := m.pendingText[f]
			m.mu.Unlock()
			if !pending {
				data, err := os.ReadFile(f)
				if err != nil {
					return
				}
				text = string(data)
			}

			client.OpenFile(context.Background(), f, langID, text)
		}(fp)
	}
	wg.Wait()
}

// scanProjectFiles walks rootPath and returns all files whose extension
// is in extSet.  Common dependency/build directories are skipped.
func scanProjectFiles(rootPath string, extSet map[string]bool) []string {
	var files []string

	// Directories we never descend into.
	skipDirs := map[string]bool{
		".git":              true,
		"node_modules":      true,
		"target":            true,
		"vendor":            true,
		".venv":             true,
		"venv":              true,
		"__pycache__":       true,
		".next":             true,
		"dist":              true,
		"build":             true,
		".cache":            true,
		".terraform":        true,
		"bower_components":  true,
	}

	_ = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() != filepath.Base(rootPath) && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if extSet[ext] {
				files = append(files, path)
			}
		}
		return nil
	})
	return files
}

func (m *LSPManager) CloseFile(ctx context.Context, filePath string) {
	m.mu.Lock()
	m.activeFilePath = ""
	delete(m.pendingText, filePath)
	m.mu.Unlock()

	for _, client := range m.clients {
		client.CloseFile(ctx, filePath)
	}
}

func (m *LSPManager) UpdateFile(ctx context.Context, filePath, text string) {
	m.mu.Lock()
	m.pendingText[filePath] = text
	m.mu.Unlock()

	client := m.clientForFile(filePath)
	if client != nil {
		client.UpdateFile(ctx, filePath, text)
	}
}

func (m *LSPManager) Status() (connected bool, status editor.LSPStatus, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeConnected, m.activeStatus, m.activeError
}

// ServerReady returns true when at least one LSP client has reached
// the connected state. Used by the right-click context menu to
// enable LSP-backed items.
func (m *LSPManager) ServerReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeConnected && m.activeStatus == editor.LSPConnected
}

// IsConfigured returns true if StartForWorkspace has been called and
// produced a client (or attempted to). Distinguishes "never tried"
// (LSPIdle, no clients) from "configured but not connected" (e.g.
// LSPStarting, LSPFailed, or LSPConnected with activeConnected=true
// but no successful client). Used to enable "Restart LSP" only when
// there's actually something to restart.
func (m *LSPManager) IsConfigured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.clients) > 0 {
		return true
	}
	return m.activeStatus != editor.LSPIdle
}

// AllDiagnostics returns a snapshot of every cached diagnostic across
// all files the LSP server has reported on. The returned map is a
// copy — mutating it does not affect internal state. Used by the
// status bar's "Goto first error" action and similar cross-file
// navigation features.
func (m *LSPManager) AllDiagnostics() map[string][]editor.LSPDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]editor.LSPDiagnostic, len(m.fileDiagnostics))
	for k, v := range m.fileDiagnostics {
		out[k] = v
	}
	return out
}

// ClearDiagnostics removes all cached diagnostics, document symbols,
// per-file sync timestamps, and the top-issue tally. Call this when
// a server restart should produce a clean slate rather than showing
// stale results from the previous session.
func (m *LSPManager) ClearDiagnostics() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fileDiagnostics = make(map[string][]editor.LSPDiagnostic)
	m.docSymbols = make(map[string][]editor.LSPSymbol)
	m.fileLastSync = make(map[string]int64)
	m.issueCount = make(map[string]int)
	m.symbolsCached = false
}

// FileLastSync returns the UnixNano timestamp of the most recent
// diagnostic publish for the given file, or 0 if the server has
// never reported on it. Lets navigation features skip stale entries.
func (m *LSPManager) FileLastSync(filePath string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileLastSync[filepath.Clean(filePath)]
}

// ProjectDiagnosticCounts aggregates diagnostics across all tracked files.
func (m *LSPManager) ProjectDiagnosticCounts() (errors, warnings, hints int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, diags := range m.fileDiagnostics {
		for _, d := range diags {
			switch d.Severity {
			case editor.DiagError:
				errors++
			case editor.DiagWarning:
				warnings++
			default:
				hints++
			}
		}
	}
	return
}

// ProjectInsights returns a whole-project snapshot suitable for the bottom-left
// status bar. Always reflects the state of every file the LSP server knows
// about, regardless of which file (if any) is open in the editor.
func (m *LSPManager) ProjectInsights() editor.ProjectInsights {
	m.mu.Lock()
	defer m.mu.Unlock()

	ins := editor.ProjectInsights{
		ServerName:     m.activeServerNameLocked(),
		ServerReady:    m.activeStatus == editor.LSPConnected && !m.isFailedLocked() && !m.syncing,
		ServerBusy:     m.inflight > 0,
		ServerStarting: m.activeStatus == editor.LSPStarting,
		ServerSyncing:  m.syncing,
		ServerFailed:   m.isFailedLocked(),
		ServerError:    m.activeError,
		FilesTracked:   len(m.fileDiagnostics),
		LastSync:       m.lastSyncLocked(),
		LastBusyAt:     m.lastBusyAt,
	}

	for _, diags := range m.fileDiagnostics {
		if len(diags) > 0 {
			ins.FilesAffected++
		}
		for _, d := range diags {
			switch d.Severity {
			case editor.DiagError:
				ins.Errors++
			case editor.DiagWarning:
				ins.Warnings++
			case editor.DiagInfo:
				ins.Info++
			default:
				ins.Hints++
			}
		}
	}

	for _, syms := range m.docSymbols {
		ins.SymbolsCount += len(syms)
	}

	// Find the most common diagnostic message.
	ins.TopIssue = m.topIssueLocked()
	return ins
}

func (m *LSPManager) activeServerNameLocked() string {
	for _, c := range m.clients {
		return c.ServerCmd()
	}
	return ""
}

func (m *LSPManager) isFailedLocked() bool {
	return m.activeStatus == editor.LSPFailed || (m.activeError != "" && m.activeStatus != editor.LSPConnected)
}

func (m *LSPManager) lastSyncLocked() int64 {
	var latest int64
	for _, t := range m.fileLastSync {
		if t > latest {
			latest = t
		}
	}
	return latest
}

func (m *LSPManager) topIssueLocked() string {
	var (
		bestMsg string
		bestCnt int
	)
	for msg, cnt := range m.issueCount {
		if cnt > bestCnt {
			bestCnt = cnt
			bestMsg = msg
		}
	}
	if bestMsg == "" {
		return ""
	}
	return fmt.Sprintf("%s (×%d)", truncateForTop(bestMsg, 40), bestCnt)
}

func truncateForTop(s string, max int) string {
	// Trim multi-line messages to their first line — the status bar is
	// single-line so a multi-line error would break layout.
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}

func (m *LSPManager) HasActiveFile() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFilePath != ""
}

// NotifyWatchedFilesChanged sends workspace/didChangeWatchedFiles to the LSP
// server for a file that changed on disk. changeType matches LSP FileChangeType:
// 1=Created, 2=Changed, 3=Deleted.
func (m *LSPManager) NotifyWatchedFilesChanged(ctx context.Context, filePath string, changeType int) {
	client := m.clientForFile(filePath)
	if client != nil {
		client.NotifyWatchedFilesChanged(ctx, filePath, changeType)
	}
}

// SyncFileFromDisk re-reads a file from disk and pushes the new content to the
// LSP server. This is the mechanism that makes background diagnostics react to
// external edits (e.g. nano, git checkout) with zero user interaction — like
// VSCode and JetBrains.
//
// Per the LSP spec, files opened via didOpen are tracked in-memory by the
// server, and didChangeWatchedFiles is ignored for them. Since we bulk-open
// all project files at startup (openProjectFiles), the only way to make the
// server re-analyze after an external edit is to push the new content via
// didChange (or didOpen for newly-created files).
//
// excludeFilePath should be set to the active editor file path (if any) so
// that the editor's own Reload + UpdateFile path handles it instead. This
// avoids clobbering the editor's unsaved edits.
func (m *LSPManager) SyncFileFromDisk(ctx context.Context, filePath, excludeFilePath string, changeType int) {
	if filePath == excludeFilePath {
		return
	}
	client := m.clientForFile(filePath)
	if client != nil {
		client.SyncFileFromDisk(ctx, filePath, changeType)
	}
}

func (m *LSPManager) Stop() {
	m.mu.Lock()
	clients := make([]*editor.LSPIntegration, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[string]*editor.LSPIntegration)
	// Reset lifecycle state so the status bar and other consumers
	// don't keep reporting a server that's been intentionally
	// stopped. Cached diagnostics, symbols, and sync timestamps
	// are intentionally NOT cleared here — ClearDiagnostics() does
	// that explicitly when the caller wants a fully clean state.
	m.activeConnected = false
	m.activeStatus = editor.LSPIdle
	m.activeError = ""
	m.syncing = false
	m.inflight = 0
	m.lastBusyAt = time.Now().UnixNano()
	m.mu.Unlock()

	for _, c := range clients {
		c.Stop()
	}
}

func (m *LSPManager) captureOnDiagnostics() func(string, []editor.LSPDiagnostic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onDiagnostics
}

func (m *LSPManager) removeClient(client *editor.LSPIntegration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, c := range m.clients {
		if c == client {
			delete(m.clients, key)
			return
		}
	}
}

// setSyncing flips the syncing flag. The status bar reads it to decide
// whether to show the animated dot for the project-wide "LSP working"
// indicator. Held briefly under m.mu.
func (m *LSPManager) setSyncing(v bool) {
	m.mu.Lock()
	if v {
		m.lastBusyAt = time.Now().UnixNano()
	}
	m.syncing = v
	m.mu.Unlock()
}

func (m *LSPManager) setFileDiagnostics(filePath string, diags []editor.LSPDiagnostic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp := filepath.Clean(filePath)

	// Subtract the old diagnostic messages from the global issue counter so
	// the "top issue" only reflects the current snapshot. Skipping this
	// would let stale messages keep "winning" the top slot forever.
	if prev, ok := m.fileDiagnostics[fp]; ok {
		for _, d := range prev {
			if msg := strings.TrimSpace(d.Message); msg != "" {
				if c := m.issueCount[msg]; c <= 1 {
					delete(m.issueCount, msg)
				} else {
					m.issueCount[msg] = c - 1
				}
			}
		}
	}

	if diags == nil {
		delete(m.fileDiagnostics, fp)
		delete(m.fileLastSync, fp)
	} else {
		m.fileDiagnostics[fp] = diags
		m.fileLastSync[fp] = time.Now().UnixNano()
		m.lastBusyAt = time.Now().UnixNano()
		for _, d := range diags {
			if msg := strings.TrimSpace(d.Message); msg != "" {
				m.issueCount[msg]++
			}
		}
	}
}

func (m *LSPManager) FileDiagnostics(filePath string) []editor.LSPDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileDiagnostics[filepath.Clean(filePath)]
}

func (m *LSPManager) HasFileDiagnostics(filePath string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.fileDiagnostics[filepath.Clean(filePath)]
	return ok
}

// InflightRequests returns the number of LSP requests currently in flight.
// The status bar uses this to show the busy indicator.
func (m *LSPManager) InflightRequests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inflight
}

// ─── Request routing ────────────────────────────────────────────────────────
//
// The methods below route a request to the correct LSP client (chosen by file
// extension + project root) and track in-flight counts for the busy indicator.
// All routes share the same bookkeeping helpers to keep the busy state
// accurate even when a request fails or panics.

// trackInflight increments the in-flight counter for the duration of fn.
func (m *LSPManager) trackInflight(fn func() error) error {
	m.mu.Lock()
	m.inflight++
	m.lastBusyAt = time.Now().UnixNano()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inflight--
		if m.inflight < 0 {
			m.inflight = 0
		}
		m.mu.Unlock()
	}()
	return fn()
}

// RequestDefinition routes a "go to definition" request to the right client.
func (m *LSPManager) RequestDefinition(ctx context.Context, filePath string, line, col int) []editor.LSPLocation {
	var out []editor.LSPLocation
	_ = m.trackInflight(func() error {
		client := m.clientForFile(filePath)
		if client == nil {
			return nil
		}
		out = client.RequestDefinition(ctx, filePath, line, col)
		return nil
	})
	return out
}

// FindReferences routes a "find references" request to the right client.
func (m *LSPManager) FindReferences(ctx context.Context, filePath string, line, col int, includeDeclaration bool) []editor.LSPLocation {
	var out []editor.LSPLocation
	_ = m.trackInflight(func() error {
		client := m.clientForFile(filePath)
		if client == nil {
			return nil
		}
		out = client.FindReferences(ctx, filePath, line, col, includeDeclaration)
		return nil
	})
	return out
}

// RequestDocumentSymbols routes a "document symbols" request to the right
// client and caches the result for the document outline panel.
func (m *LSPManager) RequestDocumentSymbols(ctx context.Context, filePath string) []editor.LSPSymbol {
	var out []editor.LSPSymbol
	_ = m.trackInflight(func() error {
		client := m.clientForFile(filePath)
		if client == nil {
			return nil
		}
		out = client.RequestDocumentSymbols(ctx, filePath)
		if out != nil {
			m.mu.Lock()
			m.docSymbols[filepath.Clean(filePath)] = out
			m.symbolsCached = true
			m.mu.Unlock()
		}
		return nil
	})
	return out
}

// DocumentSymbols returns the cached document outline for a file, if any.
// Callers should not assume the cache is fresh — refresh via
// RequestDocumentSymbols when the file's content has changed materially.
func (m *LSPManager) DocumentSymbols(filePath string) []editor.LSPSymbol {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.docSymbols[filepath.Clean(filePath)]
}

// AllDocumentSymbols returns the cached outlines for every file. Used by the
// workspace symbols fuzzy search so we can search across the whole project
// without hitting the LSP server again.
func (m *LSPManager) AllDocumentSymbols() map[string][]editor.LSPSymbol {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]editor.LSPSymbol, len(m.docSymbols))
	for k, v := range m.docSymbols {
		out[k] = v
	}
	return out
}

// RequestHover routes a hover request to the right client.
func (m *LSPManager) RequestHover(ctx context.Context, filePath string, line, col int) (string, error) {
	var (
		out string
		err error
	)
	_ = m.trackInflight(func() error {
		client := m.clientForFile(filePath)
		if client == nil {
			return fmt.Errorf("no LSP client for %s", filePath)
		}
		out, err = client.RequestHover(ctx, filePath, line, col)
		return nil
	})
	return out, err
}

// RequestRename routes a rename request to the right client.
func (m *LSPManager) RequestRename(ctx context.Context, filePath string, line, col int, newName string) (interface{}, error) {
	var (
		out interface{}
		err error
	)
	_ = m.trackInflight(func() error {
		client := m.clientForFile(filePath)
		if client == nil {
			return fmt.Errorf("no LSP client for %s", filePath)
		}
		out, err = client.RequestRename(ctx, filePath, line, col, newName)
		return nil
	})
	return out, err
}

// RequestCompletions routes a completion request to the right client.
func (m *LSPManager) RequestCompletions(ctx context.Context, filePath string, line, col int) []editor.CompletionItem {
	var out []editor.CompletionItem
	_ = m.trackInflight(func() error {
		client := m.clientForFile(filePath)
		if client == nil {
			return nil
		}
		out = client.RequestCompletions(ctx, filePath, line, col)
		return nil
	})
	return out
}

// InvalidateFile removes all cached state for a file. Call this when a file
// is deleted, renamed, or its content has been replaced wholesale.
func (m *LSPManager) InvalidateFile(filePath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp := filepath.Clean(filePath)
	delete(m.fileDiagnostics, fp)
	delete(m.fileLastSync, fp)
	delete(m.docSymbols, fp)
}
