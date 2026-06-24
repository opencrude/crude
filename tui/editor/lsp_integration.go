package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type LSPDiagnostic struct {
	Line     int
	Col      int
	Message  string
	Severity int
	EndLine  int
	EndCol   int
}

// LSPIntegration manages a single LSP server connection for one project root.
// Multiple files within the same root can be opened simultaneously.
type LSPIntegration struct {
	mu        sync.Mutex
	client    *lsp.Client
	serverCmd string
	serverArgs []string
	rootURI   string

	openFiles      map[string]*openFileState // filePath -> state
	versionCounter int

	onDiagnostics func(filePath string, diags []LSPDiagnostic)
}

type openFileState struct {
	uri        string
	languageID string
}

// OnDiagnostics sets the callback for receiving diagnostics for open files.
func (l *LSPIntegration) OnDiagnostics(fn func(filePath string, diags []LSPDiagnostic)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onDiagnostics = fn
}

func NewLSPIntegration(serverCmd string, serverArgs []string, rootURI string) *LSPIntegration {
	return &LSPIntegration{
		serverCmd:  serverCmd,
		serverArgs: serverArgs,
		rootURI:    rootURI,
		openFiles:  make(map[string]*openFileState),
	}
}

func (l *LSPIntegration) RootURI() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rootURI
}

func (l *LSPIntegration) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.client != nil {
		return nil
	}

	client, err := lsp.NewClient(lsp.ClientConfig{
		Command: l.serverCmd,
		Args:    l.serverArgs,
		RootURI: l.rootURI,
	})
	if err != nil {
		return err
	}

	client.RegisterNotificationHandler("textDocument/publishDiagnostics", func(_ context.Context, _ string, params json.RawMessage) {
		var diagParams protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(params, &diagParams); err != nil {
			return
		}
		l.updateDiagnostics(diagParams.URI, diagParams.Diagnostics)
	})

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := client.Initialize(initCtx, false); err != nil {
		client.Kill()
		return err
	}

	l.client = client
	return nil
}

func (l *LSPIntegration) OpenFile(ctx context.Context, filePath, languageID, text string) {
	l.mu.Lock()
	if l.client == nil {
		l.mu.Unlock()
		return
	}
	fp := filepath.Clean(filePath)
	uri := string(protocol.URIFromPath(fp))
	l.versionCounter++
	version := l.versionCounter
	l.openFiles[fp] = &openFileState{uri: uri, languageID: languageID}
	l.mu.Unlock()

	_ = l.client.NotifyDidOpenTextDocument(ctx, uri, languageID, version, text)
}

func (l *LSPIntegration) UpdateFile(ctx context.Context, filePath, text string) {
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if l.client == nil || !ok {
		l.mu.Unlock()
		return
	}
	l.versionCounter++
	version := l.versionCounter
	uri := state.uri
	l.mu.Unlock()

	_ = l.client.NotifyDidChangeTextDocument(ctx, uri, version, []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: text}},
	})
}

func (l *LSPIntegration) CloseFile(ctx context.Context, filePath string) {
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if !ok {
		l.mu.Unlock()
		return
	}
	delete(l.openFiles, fp)
	uri := state.uri
	client := l.client
	l.mu.Unlock()

	if client == nil {
		return
	}
	_ = client.NotifyDidCloseTextDocument(ctx, uri)
}

// RequestCompletions returns a flat list of completion items at (line, col).
// Returns nil if the request fails or the file is not open in the LSP server.
func (l *LSPIntegration) RequestCompletions(ctx context.Context, filePath string, line, col int) []CompletionItem {
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if l.client == nil || !ok {
		l.mu.Unlock()
		return nil
	}
	uri := state.uri
	l.mu.Unlock()

	result, err := l.client.RequestCompletion(ctx, uri, protocol.Position{
		Line:      uint32(line),
		Character: uint32(col),
	})
	if err != nil || result == nil {
		return nil
	}
	out := make([]CompletionItem, 0, len(result.Items))
	for _, it := range result.Items {
		out = append(out, CompletionItem{
			Label:         it.Label,
			Detail:        it.Detail,
			Documentation: documentationOf(it),
			Kind:          int(it.Kind),
			InsertText:    insertTextOf(it),
		})
	}
	return out
}

// CompletionItem is a flat, viewmodel-friendly representation of a single
// completion result returned by the LSP server.
type CompletionItem struct {
	Label         string
	Detail        string
	Documentation string
	Kind          int
	InsertText    string
}

func insertTextOf(it protocol.CompletionItem) string {
	// it.TextEdit is a tagged union; if present, it carries the actual
	// replacement text. Fall back to the simpler InsertText when the server
	// doesn't supply a text edit.
	if it.TextEdit != nil {
		switch t := it.TextEdit.Value.(type) {
		case protocol.TextEdit:
			return t.NewText
		case *protocol.TextEdit:
			return t.NewText
		}
	}
	return it.InsertText
}

func documentationOf(it protocol.CompletionItem) string {
	if it.Documentation == nil {
		return ""
	}
	switch v := it.Documentation.Value.(type) {
	case string:
		return v
	case protocol.MarkupContent:
		return v.Value
	}
	return ""
}

// RequestDefinition returns the definition location(s) for the symbol at
// (line, col) in filePath. Returns nil if the request fails or the client is
// not connected. Multiple locations can be returned for symbols defined in
// multiple places (interfaces, generics).
func (l *LSPIntegration) RequestDefinition(ctx context.Context, filePath string, line, col int) []LSPLocation {
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	_, ok := l.openFiles[fp]
	l.mu.Unlock()

	if client == nil || !ok {
		return nil
	}
	results, err := client.RequestDefinition(ctx, fp, line, col)
	if err != nil {
		return nil
	}
	return l.toLSPLocations(results)
}

// FindReferences returns all references to the symbol at (line, col) in
// filePath, optionally including the declaration site. includeDeclaration=true
// is the typical UX (VSCode/JetBrains include it by default).
func (l *LSPIntegration) FindReferences(ctx context.Context, filePath string, line, col int, includeDeclaration bool) []LSPLocation {
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	_, ok := l.openFiles[fp]
	l.mu.Unlock()

	if client == nil || !ok {
		return nil
	}
	results, err := client.FindReferences(ctx, fp, line, col, includeDeclaration)
	if err != nil {
		return nil
	}
	return l.toLSPLocations(results)
}

// RequestDocumentSymbols returns a hierarchical outline (functions, types,
// variables) for the given file. Children of nested symbols are preserved.
func (l *LSPIntegration) RequestDocumentSymbols(ctx context.Context, filePath string) []LSPSymbol {
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	_, ok := l.openFiles[fp]
	l.mu.Unlock()

	if client == nil || !ok {
		return nil
	}
	results, err := client.RequestDocumentSymbols(ctx, fp)
	if err != nil {
		return nil
	}
	out := make([]LSPSymbol, 0, len(results))
	for _, r := range results {
		out = append(out, l.convertDocSymbol(r, 0)...)
	}
	return out
}

// RequestHover returns the hover information (markup content + range) for the
// symbol at (line, col). Returns nil if no hover info is available.
func (l *LSPIntegration) RequestHover(ctx context.Context, filePath string, line, col int) (string, error) {
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state, ok := l.openFiles[fp]
	if l.client == nil || !ok {
		l.mu.Unlock()
		return "", fmt.Errorf("file not open in LSP")
	}
	uri := state.uri
	l.mu.Unlock()

	hover, err := l.client.RequestHover(ctx, uri, protocol.Position{
		Line:      uint32(line),
		Character: uint32(col),
	})
	if err != nil {
		return "", err
	}
	if hover == nil || hover.Contents.Value == "" {
		return "", nil
	}
	// Hover.Contents can be a string, MarkupContent, or MarkedString.
	// protocol normalizes it to a single string in our usage; strip basic
	// markdown noise for terminal display.
	return stripMarkdown(hover.Contents.Value), nil
}

// RequestRename returns the workspace edit that would rename the symbol at
// (line, col) to newName. The caller is responsible for applying the edit.
func (l *LSPIntegration) RequestRename(ctx context.Context, filePath string, line, col int, newName string) (*protocol.WorkspaceEdit, error) {
	l.mu.Lock()
	client := l.client
	fp := filepath.Clean(filePath)
	_, ok := l.openFiles[fp]
	l.mu.Unlock()

	if client == nil || !ok {
		return nil, fmt.Errorf("file not open in LSP")
	}
	return client.RequestRename(ctx, fp, line, col, newName)
}

// ServerCmd returns the command used to launch the LSP server, e.g. "gopls".
func (l *LSPIntegration) ServerCmd() string {
	return l.serverCmd
}

func (l *LSPIntegration) toLSPLocations(locs []protocol.Location) []LSPLocation {
	out := make([]LSPLocation, 0, len(locs))
	for _, p := range locs {
		uri := string(p.URI)
		out = append(out, LSPLocation{
			URI:     uri,
			Path:    l.uriToPath(p.URI),
			Line:    int(p.Range.Start.Line),
			Col:     int(p.Range.Start.Character),
			EndLine: int(p.Range.End.Line),
			EndCol:  int(p.Range.End.Character),
		})
	}
	return out
}

// convertDocSymbol flattens a DocumentSymbolResult (which can be either
// DocumentSymbol or SymbolInformation) into our LSPSymbol type. The depth
// parameter caps recursion to avoid pathological trees in the UI.
func (l *LSPIntegration) convertDocSymbol(r protocol.DocumentSymbolResult, depth int) []LSPSymbol {
	if depth > 6 {
		return nil
	}
	switch v := r.(type) {
	case *protocol.DocumentSymbol:
		sym := LSPSymbol{
			Name:     v.Name,
			Detail:   v.Detail,
			Kind:     int(v.Kind),
			KindName: SymbolKindName(int(v.Kind)),
			Line:     int(v.Range.Start.Line),
			Col:      int(v.Range.Start.Character),
			EndLine:  int(v.Range.End.Line),
			EndCol:   int(v.Range.End.Character),
		}
		if len(v.Children) > 0 {
			sym.Children = make([]LSPSymbol, 0, len(v.Children))
			for i := range v.Children {
				kids := l.convertDocSymbol(&v.Children[i], depth+1)
				sym.Children = append(sym.Children, kids...)
			}
		}
		return []LSPSymbol{sym}
	case *protocol.SymbolInformation:
		return []LSPSymbol{{
			Name:     v.Name,
			Detail:   "", // SymbolInformation has no detail field
			Kind:     int(v.Kind),
			KindName: SymbolKindName(int(v.Kind)),
			Line:     int(v.Location.Range.Start.Line),
			Col:      int(v.Location.Range.Start.Character),
			EndLine:  int(v.Location.Range.End.Line),
			EndCol:   int(v.Location.Range.End.Character),
		}}
	}
	return nil
}

// stripMarkdown removes common markdown noise that doesn't render well in a
// terminal popup: fenced code blocks' ``` fences, heading #'s, and bold/italic
// markers. Code spans are preserved as-is.
func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "```", "")
	s = strings.ReplaceAll(s, "`", "")
	// Drop leading "## ", "### ", etc. from start of lines.
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		ln = strings.TrimLeft(ln, " \t")
		for strings.HasPrefix(ln, "#") {
			ln = strings.TrimPrefix(ln, "#")
			ln = strings.TrimLeft(ln, " \t")
		}
		lines[i] = ln
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (l *LSPIntegration) Stop() {
	l.mu.Lock()
	client := l.client
	l.client = nil
	l.openFiles = make(map[string]*openFileState)
	l.mu.Unlock()

	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Shutdown(ctx)
		client.Exit()
		client.Kill()
	}
}

// NotifyWatchedFilesChanged sends workspace/didChangeWatchedFiles to the LSP
// server for files that changed on disk (outside the editor).
func (l *LSPIntegration) NotifyWatchedFilesChanged(ctx context.Context, filePath string, changeType int) {
	l.mu.Lock()
	client := l.client
	l.mu.Unlock()

	if client == nil {
		return
	}

	fp := filepath.Clean(filePath)
	uri := string(protocol.URIFromPath(fp))

	_ = client.NotifyDidChangeWatchedFiles(ctx, []protocol.FileEvent{
		{
			URI:  protocol.DocumentURI(uri),
			Type: protocol.FileChangeType(changeType),
		},
	})
}

// SyncFileFromDisk re-reads the file from disk and pushes the content to the
// LSP server via didChange. This is needed for files that were bulk-opened via
// openProjectFiles (didOpen) but are NOT open in the editor UI. Per the LSP
// spec, once a file is "open" the server's in-memory document is the source of
// truth and didChangeWatchedFiles is ignored for it — so the only way to make
// the server re-analyze after an external edit is to push the new content via
// didChange.
//
// For deleted files, the file is closed via didClose so the server clears its
// diagnostics.
//
// The caller must exclude the active editor file (which is handled by the
// editor's own Reload + UpdateFile path) to avoid clobbering unsaved edits.
func (l *LSPIntegration) SyncFileFromDisk(ctx context.Context, filePath string, changeType int) {
	fp := filepath.Clean(filePath)

	if changeType == 3 { // Deleted
		l.CloseFile(ctx, fp)
		return
	}

	// Read the new content from disk.
	data, err := os.ReadFile(fp)
	if err != nil {
		// File may have been deleted between the fsnotify event and now.
		l.CloseFile(ctx, fp)
		return
	}

	l.mu.Lock()
	_, isOpen := l.openFiles[fp]
	l.mu.Unlock()

	if !isOpen {
		// File is not open in the LSP server. Send didOpen so the server
		// starts tracking it and publishes diagnostics.
		ext := strings.ToLower(filepath.Ext(fp))
		langID := LanguageID(ext)
		if langID == "" {
			return
		}
		l.OpenFile(ctx, fp, langID, string(data))
		return
	}

	// File is already open in the LSP — push the new content via didChange.
	l.UpdateFile(ctx, fp, string(data))
}

func (l *LSPIntegration) Diagnostics() []LSPDiagnostic {
	return nil // per-file diagnostics are not stored centrally; routed via callback
}

func (l *LSPIntegration) uriToPath(uri protocol.DocumentURI) string {
	s := string(uri)
	if strings.HasPrefix(s, "file://") {
		return filepath.Clean(filepath.FromSlash(s[7:]))
	}
	return s
}

func (l *LSPIntegration) updateDiagnostics(uri protocol.DocumentURI, diags []protocol.Diagnostic) {
	filePath := l.uriToPath(uri)

	out := make([]LSPDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, LSPDiagnostic{
			Line:     int(d.Range.Start.Line),
			Col:      int(d.Range.Start.Character),
			EndLine:  int(d.Range.End.Line),
			EndCol:   int(d.Range.End.Character),
			Message:  d.Message,
			Severity: int(d.Severity),
		})
	}

	l.mu.Lock()
	cb := l.onDiagnostics
	l.mu.Unlock()

	if cb != nil {
		cb(filePath, out)
	}
}

// LanguageID returns the LSP language identifier for a file extension.
func LanguageID(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".css":
		return "css"
	case ".json":
		return "json"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx", ".c++", ".h++":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".zig":
		return "zig"
	case ".odin":
		return "odin"
	case ".lua":
		return "lua"
	default:
		return ""
	}
}

// RootURIForFile walks up from the file's directory looking for project
// markers (go.mod, package.json, etc.) and returns a "file://" URI for
// the first directory containing one.  If no marker is found it returns
// the file's own directory as a file URI.
func RootURIForFile(filePath string) string {
	dir := filepath.Dir(filePath)
	ext := strings.ToLower(filepath.Ext(filePath))
	markers := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "composer.json", "CMakeLists.txt", "compile_commands.json"}
	preferred := map[string]string{
		".go":   "go.mod",
		".rs":   "Cargo.toml",
		".py":   "pyproject.toml",
		".c":    "compile_commands.json",
		".cpp":  "compile_commands.json",
		".h":    "compile_commands.json",
		".hpp":  "compile_commands.json",
		".cc":   "compile_commands.json",
		".hh":   "compile_commands.json",
		".cxx":  "compile_commands.json",
		".hxx":  "compile_commands.json",
		".c++":  "compile_commands.json",
		".h++":  "compile_commands.json",
		".cs":   ".csproj",
		".zig":  "build.zig",
		".odin": "ols.json",
		".lua":  ".luarc.json",
	}
	if p, ok := preferred[ext]; ok {
		markers = []string{p}
	}
	d := dir
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return "file://" + d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "file://" + dir
		}
		d = parent
	}
}

// ServerConfigForExt returns the LSP server config for a file extension.
func ServerConfigForExt(ext string) (cmd string, args []string, ok bool) {
	cfg, ok := lspServers[ext]
	if !ok {
		return "", nil, false
	}
	return cfg.Cmd, cfg.Args, true
}
