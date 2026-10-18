package editor

import (
	"crude/lspclient"
	model "crude/types"
)

type Position struct {
	Line int
	Col  int
}

// lineGeomCache holds tab-expanded, grapheme-collapsed geometry for one line.
// Deliberately excludes chroma colour data so cursor motion never tokenises.
type lineGeomCache struct {
	raw      string
	clusters []string
	runes    []rune
	widths   []int
	totalW   int
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
	DiagError   = model.DiagError
	DiagWarning = model.DiagWarning
	DiagInfo    = model.DiagInfo
	DiagHint    = model.DiagHint
)

// LSPStatus tracks the lifecycle state of the LSP connection for UI feedback.
type LSPStatus = model.LSPStatus

const (
	LSPIdle      = model.LSPIdle
	LSPStarting  = model.LSPStarting
	LSPConnected = model.LSPConnected
	LSPFailed    = model.LSPFailed
)

// LSPLocation is a flat, viewmodel-friendly representation of an LSP Location.
type LSPLocation = model.LSPLocation

// LSPSymbol is a flat, viewmodel-friendly representation of a document symbol.
// Mirrors LSP SymbolKind values (1=File, 2=Module, 3=Namespace, 4=Package,
// 5=Class, 6=Method, 7=Property, 8=Field, 9=Constructor, 10=Enum, 11=Interface,
// 12=Function, 13=Variable, 14=Constant, 15=String, 16=Number, 17=Boolean,
// 18=Array, 19=Object, 20=Key, 21=Null, 22=EnumMember, 23=Struct, 24=Event,
// 25=Operator, 26=TypeParameter).
type LSPSymbol = model.LSPSymbol

// ProjectInsights is the whole-project snapshot rendered in the bottom-left
// status bar. It is independent of any single file; it reflects the state of
// every file known to the LSPManager.
type ProjectInsights = model.ProjectInsights

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

type LSPDiagnostic = model.LSPDiagnostic
type CompletionItem = model.CompletionItem
