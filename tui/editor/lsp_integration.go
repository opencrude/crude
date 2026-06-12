package editor

import (
	"context"
	"encoding/json"
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

func (l *LSPIntegration) RequestCompletions(ctx context.Context, filePath string, line, col int) []protocol.CompletionItem {
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
	return result.Items
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

	l.mu.Lock()
	_, known := l.openFiles[filePath]
	if !known {
		// Diagnostics for a file we don't track — skip.
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()

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

	if l.onDiagnostics != nil {
		l.onDiagnostics(filePath, out)
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
