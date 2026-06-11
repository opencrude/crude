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
	cmd  string
	args []string
}

var lspServers = map[string]lspServerConfig{
	".go":   {cmd: "gopls"},
	".rs":   {cmd: "rust-analyzer"},
	".py":   {cmd: "pyright-langserver", args: []string{"--stdio"}},
	".ts":   {cmd: "typescript-language-server", args: []string{"--stdio"}},
	".js":   {cmd: "typescript-language-server", args: []string{"--stdio"}},
	".tsx":  {cmd: "typescript-language-server", args: []string{"--stdio"}},
	".jsx":  {cmd: "typescript-language-server", args: []string{"--stdio"}},
	".css":  {cmd: "vscode-css-language-server", args: []string{"--stdio"}},
	".json": {cmd: "vscode-json-language-server", args: []string{"--stdio"}},
}
