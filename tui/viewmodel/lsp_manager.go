package viewmodel

import (
	"context"
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

	fileDiagnostics map[string][]editor.LSPDiagnostic // cached diagnostics per file; survives tab close
}

func NewLSPManager(logger Logger) *LSPManager {
	return &LSPManager{
		clients:         make(map[string]*editor.LSPIntegration),
		logger:          logger,
		pendingText:     make(map[string]string),
		fileDiagnostics: make(map[string][]editor.LSPDiagnostic),
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

// clientForRoot returns the client handling a given file path based on
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

// clientForRootURI returns all clients whose key begins with rootURI.
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
	rootURI := m.activeRootURI
	m.mu.Unlock()

	// Open every source file in the project so the server reports
	// diagnostics for the entire workspace, not just open tabs.
	m.openProjectFiles(rootURI, cmd)
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
		".git":        true,
		"node_modules": true,
		"target":      true,
		"vendor":      true,
		".venv":       true,
		"venv":        true,
		"__pycache__": true,
		".next":       true,
		"dist":        true,
		"build":       true,
		".cache":      true,
		".terraform":  true,
		"bower_components": true,
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

func (m *LSPManager) HasActiveFile() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFilePath != ""
}

func (m *LSPManager) Stop() {
	m.mu.Lock()
	clients := make([]*editor.LSPIntegration, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[string]*editor.LSPIntegration)
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

func (m *LSPManager) setFileDiagnostics(filePath string, diags []editor.LSPDiagnostic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp := filepath.Clean(filePath)
	if diags == nil {
		delete(m.fileDiagnostics, fp)
	} else {
		m.fileDiagnostics[fp] = diags
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
