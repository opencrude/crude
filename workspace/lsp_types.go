package workspace

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	model "crude/types"
)

type LSPStatus = model.LSPStatus

const (
	LSPIdle      = model.LSPIdle
	LSPStarting  = model.LSPStarting
	LSPConnected = model.LSPConnected
	LSPFailed    = model.LSPFailed
	DiagError    = model.DiagError
	DiagWarning  = model.DiagWarning
	DiagInfo     = model.DiagInfo
	DiagHint     = model.DiagHint
)

type LSPDiagnostic = model.LSPDiagnostic
type LSPLocation = model.LSPLocation
type LSPSymbol = model.LSPSymbol
type CompletionItem = model.CompletionItem
type ProjectInsights = model.ProjectInsights

type LSPServerConfig struct {
	Cmd  string
	Args []string
}

// LSPClient is the workspace-facing connection contract. The concrete
// powernap/editor client is supplied by the TUI adapter at composition time.
type LSPClient interface {
	OnDiagnostics(func(filePath string, diags []LSPDiagnostic))
	Start(context.Context) error
	Stop()
	// Done is closed when the underlying server connection dies (server
	// exit, pipe error, or a wedged server that hit the write-queue cap).
	// The manager reaps the client on Done and restarts it on demand.
	Done() <-chan struct{}
	OpenFile(context.Context, string, string, string)
	UpdateFile(context.Context, string, string)
	CloseFile(context.Context, string)
	NotifySave(context.Context, string, string, int, bool)
	NotifyWatchedFilesChanged(context.Context, string, int)
	SyncFileFromDisk(context.Context, string, int)
	RequestDefinition(context.Context, string, int, int) []LSPLocation
	FindReferences(context.Context, string, int, int, bool) []LSPLocation
	RequestDocumentSymbols(context.Context, string) []LSPSymbol
	RequestHover(context.Context, string, int, int) (string, error)
	RequestRename(context.Context, string, int, int, string) (interface{}, error)
	RequestCompletions(context.Context, string, int, int) []CompletionItem
}

type LSPRuntime interface {
	NewClient(serverCmd string, serverArgs []string, rootURI string) LSPClient
	ServerConfigForExt(ext string) (cmd string, args []string, ok bool)
	RootURIForFile(filePath string) string
	LanguageID(ext string) string
	AllServerConfigs() map[string]LSPServerConfig
}

var defaultLSPConfigs = map[string]LSPServerConfig{
	".go":   {Cmd: "gopls"},
	".rs":   {Cmd: "rust-analyzer"},
	".py":   {Cmd: "pyright-langserver", Args: []string{"--stdio"}},
	".ts":   {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".js":   {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".tsx":  {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".jsx":  {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".css":  {Cmd: "vscode-css-language-server", Args: []string{"--stdio"}},
	".json": {Cmd: "vscode-json-language-server", Args: []string{"--stdio"}},
	".c":    {Cmd: "clangd"}, ".h": {Cmd: "clangd"}, ".cpp": {Cmd: "clangd"},
	".hpp": {Cmd: "clangd"}, ".cc": {Cmd: "clangd"}, ".hh": {Cmd: "clangd"},
	".cxx": {Cmd: "clangd"}, ".hxx": {Cmd: "clangd"}, ".c++": {Cmd: "clangd"},
	".h++": {Cmd: "clangd"}, ".cs": {Cmd: "omnisharp", Args: []string{"--languageserver"}},
	".zig": {Cmd: "zls"}, ".odin": {Cmd: "ols"}, ".lua": {Cmd: "lua-language-server"},
}

func defaultServerConfigForExt(ext string) (string, []string, bool) {
	cfg, ok := defaultLSPConfigs[strings.ToLower(ext)]
	if !ok {
		return "", nil, false
	}
	return cfg.Cmd, append([]string(nil), cfg.Args...), true
}

func defaultRootURI(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	return fileURI(filepath.Dir(abs))
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func filePathFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return filepath.Clean(strings.TrimPrefix(uri, "file://"))
	}
	path := u.Path
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		path = "//" + u.Host + path
	}
	if trimmed := strings.TrimPrefix(path, "/"); filepath.VolumeName(trimmed) != "" {
		path = trimmed
	}
	return filepath.Clean(filepath.FromSlash(path))
}

func defaultLanguageID(ext string) string {
	return strings.TrimPrefix(strings.ToLower(ext), ".")
}
