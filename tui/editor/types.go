package editor

import "time"

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

// LSPLocation is a flat, viewmodel-friendly representation of an LSP Location.
type LSPLocation struct {
	URI      string
	Path     string // file system path (decoded from URI)
	Line     int
	Col      int
	EndLine  int
	EndCol   int
}

// LSPSymbol is a flat, viewmodel-friendly representation of a document symbol.
// Mirrors LSP SymbolKind values (1=File, 2=Module, 3=Namespace, 4=Package,
// 5=Class, 6=Method, 7=Property, 8=Field, 9=Constructor, 10=Enum, 11=Interface,
// 12=Function, 13=Variable, 14=Constant, 15=String, 16=Number, 17=Boolean,
// 18=Array, 19=Object, 20=Key, 21=Null, 22=EnumMember, 23=Struct, 24=Event,
// 25=Operator, 26=TypeParameter).
type LSPSymbol struct {
	Name     string
	Detail   string
	Kind     int    // LSP SymbolKind
	KindName string // human-friendly ("function", "type", "var", …)
	Line     int
	Col      int
	EndLine  int
	EndCol   int
	Children []LSPSymbol
}

// ProjectInsights is the whole-project snapshot rendered in the bottom-left
// status bar. It is independent of any single file — it reflects the state of
// every file known to the LSPManager.
type ProjectInsights struct {
	ServerName     string // "gopls", "rust-analyzer", "" (none)
	ServerReady    bool   // connected and idle
	ServerBusy     bool   // currently processing a request
	ServerStarting bool   // in startup phase (handshake not done)
	ServerSyncing  bool   // connected but bulk-opening project files / didChange in flight
	ServerFailed   bool   // startup or runtime failure
	ServerError    string // human-readable error message

	Errors   int
	Warnings int
	Info     int
	Hints    int

	FilesAffected int    // files with ≥1 diagnostic
	FilesTracked  int    // total files open in the LSP server
	SymbolsCount  int    // total document symbols indexed
	TopIssue      string // "undefined: foo (×3)" or "" if no diagnostics
	LastSync      int64  // unix nanos; 0 if never synced

	// LastBusyAt is the most recent time (UnixNano) the LSP was in a
	// non-idle state (starting, syncing, busy, or receiving diagnostics).
	// The status bar uses this to keep the heartbeat dot animated for a
	// minimum of AnimGrace period after the last activity, even if the
	// instantaneous state is "ready". This ensures the user sees the
	// animation even when the LSP transitions through idle states
	// between the 400ms ticks.
	LastBusyAt int64 `json:"-"`
}

// AnimGrace is the minimum time the heartbeat dot stays animated after the
// last LSP activity. Prevents the blink from turning solid before the user
// can notice it.
const AnimGrace = 2 * time.Second

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

// SymbolKindName returns a human-friendly name for an LSP SymbolKind value.
// See: https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification#symbolKind
func SymbolKindName(kind int) string {
	switch kind {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 15:
		return "string"
	case 16:
		return "number"
	case 17:
		return "boolean"
	case 18:
		return "array"
	case 19:
		return "object"
	case 20:
		return "key"
	case 21:
		return "null"
	case 22:
		return "enum-member"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type-param"
	default:
		return "symbol"
	}
}

// SymbolKindIcon returns a single-character icon suitable for an outline view.
// Keeps the outline compact and scannable.
func SymbolKindIcon(kind int) string {
	switch kind {
	case 5, 11, 23: // class, interface, struct
		return "T"
	case 6, 12, 9: // method, function, constructor
		return "ƒ"
	case 8, 7, 22: // field, property, enum-member
		return "•"
	case 13, 14: // variable, constant
		return "v"
	case 4, 2, 3: // package, module, namespace
		return "P"
	case 10: // enum
		return "E"
	default:
		return "·"
	}
}
