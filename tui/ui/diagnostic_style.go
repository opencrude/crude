package ui

import "crude/types"

// DiagnosticGlyph and DiagnosticColor give editor markers and readers the
// same severity language as the Problems panel.
func DiagnosticGlyph(severity int) string {
	switch severity {
	case types.DiagError:
		return "✕"
	case types.DiagWarning:
		return "⚠"
	case types.DiagInfo:
		return "i"
	default:
		return "·"
	}
}

func DiagnosticColor(severity int) SemanticColor {
	switch severity {
	case types.DiagError:
		return ColorError
	case types.DiagWarning:
		return ColorWarning
	case types.DiagInfo:
		return ColorAccent2
	default:
		return ColorMuted
	}
}
