package lspclient

import model "crude/types"

type LSPLocation = model.LSPLocation
type LSPSymbol = model.LSPSymbol

const (
	DiagError   = model.DiagError
	DiagWarning = model.DiagWarning
	DiagInfo    = model.DiagInfo
	DiagHint    = model.DiagHint
)

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
