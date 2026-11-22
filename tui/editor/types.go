package editor

import (
	"crude/lsp/lspclient"
	"crude/types/insight"
)

type Position struct {
	Line int
	Col  int
}

// lineGeomCache holds tab-expanded, grapheme-collapsed geometry for one line.
// Deliberately excludes chroma colour data so cursor motion never tokenises.
type lineGeomCache struct {
	raw        string
	clusters   []string
	runes      []rune
	widths     []int
	totalW     int
	whitespace *whitespaceGeometry
	bidi       editorLineBidi
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
	// DiagError is the error severity of a diagnostic.
	DiagError = insight.DiagError
	// DiagWarning is the warning severity of a diagnostic.
	DiagWarning = insight.DiagWarning
	// DiagInfo is the information severity of a diagnostic.
	DiagInfo = insight.DiagInfo
	// DiagHint is the hint severity of a diagnostic.
	DiagHint = insight.DiagHint
)

const (
	// LSPIdle means the language server is not running yet.
	LSPIdle = insight.LSPIdle
	// LSPStarting means the language server is starting.
	LSPStarting = insight.LSPStarting
	// LSPConnected means the language server is ready.
	LSPConnected = insight.LSPConnected
	// LSPFailed means the language server did not start.
	LSPFailed = insight.LSPFailed
)

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

// SymbolKindName names protocol symbol kinds for editor presentation.
func SymbolKindName(kind int) string { return lspclient.SymbolKindName(kind) }
