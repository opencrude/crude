package ui

import (
	"crude/types/insight"
)

// DiagnosticGlyph and DiagnosticColor give editor markers and readers the
// same severity language as the Problems panel.
func DiagnosticGlyph(severity int) string {
	switch severity {
	case insight.DiagError:
		return "✕"
	case insight.DiagWarning:
		return "⚠"
	case insight.DiagInfo:
		return "i"
	default:
		return "·"
	}
}

func DiagnosticColor(severity int) SemanticColor {
	switch severity {
	case insight.DiagError:
		return ColorError
	case insight.DiagWarning:
		return ColorWarning
	case insight.DiagInfo:
		return ColorAccent2
	default:
		return ColorMuted
	}
}
