package editor

import model "crude/types"

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

// chromaLineEntry holds the syntax colour map for one line.
type chromaLineEntry struct {
	raw      string
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
// status bar. It is independent of any single file — it reflects the state of
// every file known to the LSPManager.
type ProjectInsights = model.ProjectInsights

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
