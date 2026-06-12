package editor

type Position struct {
	Line int
	Col  int
}

type lineRenderCache struct {
	text     string
	scrollX  int
	runes    []rune
	widths   []int
	totalW   int
	colorMap []string
}

type EditInfo struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

type diagSpan struct {
	startCol int
	endCol   int
	severity int
}

const (
	DiagError   = 1
	DiagWarning = 2
	DiagInfo    = 3
	DiagHint    = 4
)

// LSPStatus tracks the lifecycle state of the LSP connection for UI feedback.
type LSPStatus int

const (
	LSPIdle        LSPStatus = iota // no LSP server configured for this file type
	LSPStarting                     // attempting to start the LSP server
	LSPConnected                    // LSP server is running and initialized
	LSPFailed                       // LSP server failed to start or crashed
)

type lspServerConfig struct {
	Cmd  string
	Args []string
}

var lspServers = map[string]lspServerConfig{
	// Go
	".go":   {Cmd: "gopls"},
	// Rust
	".rs":   {Cmd: "rust-analyzer"},
	// Python
	".py":   {Cmd: "pyright-langserver", Args: []string{"--stdio"}},
	// TypeScript / JavaScript
	".ts":   {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".js":   {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".tsx":  {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	".jsx":  {Cmd: "typescript-language-server", Args: []string{"--stdio"}},
	// CSS / JSON
	".css":  {Cmd: "vscode-css-language-server", Args: []string{"--stdio"}},
	".json": {Cmd: "vscode-json-language-server", Args: []string{"--stdio"}},
	// C / C++
	".c":   {Cmd: "clangd"},
	".h":   {Cmd: "clangd"},
	".cpp": {Cmd: "clangd"},
	".hpp": {Cmd: "clangd"},
	".cc":  {Cmd: "clangd"},
	".hh":  {Cmd: "clangd"},
	".cxx": {Cmd: "clangd"},
	".hxx": {Cmd: "clangd"},
	".c++": {Cmd: "clangd"},
	".h++": {Cmd: "clangd"},
	// C#
	".cs":  {Cmd: "omnisharp", Args: []string{"--languageserver"}},
	// Zig
	".zig": {Cmd: "zls"},
	// Odin
	".odin": {Cmd: "ols"},
	// Lua
	".lua": {Cmd: "lua-language-server"},
}

// AllServerConfigs returns a copy of the server config map for enumeration.
func AllServerConfigs() map[string]lspServerConfig {
	out := make(map[string]lspServerConfig, len(lspServers))
	for k, v := range lspServers {
		out[k] = v
	}
	return out
}

// MustServerCmd returns the server command for a file extension.
// It panics if the extension is not configured (callers must validate first).
func MustServerCmd(ext string) string {
	return lspServers[ext].Cmd
}
