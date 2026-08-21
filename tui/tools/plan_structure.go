package tools

import (
	"strings"

	"crude/tui/rendering"
)

// PlanRowKind classifies a plan-box line with no colour info.
type PlanRowKind int

const (
	PlanRowDefault PlanRowKind = iota
	PlanRowDone                // ✓
	PlanRowActive              // →
	PlanRowFailed              // ✗
	PlanRowWarning             // !
	PlanRowTodo                // ☐
	PlanRowHeader              // Plan: / Goal:
	PlanRowSuspended
)

// PlanLine is one plan-box row: the (truncated) text plus its kind.
type PlanLine struct {
	Text string
	Kind PlanRowKind
}

// ParsePlanBoxStruct extracts the colour-free structure from plan-box text.
// Each input line is classified by its leading marker (✓ → ✗ ! ☐ Plan: Goal:
// Suspended:) and truncated to contentWidth using the same grapheme-width path
// as the ANSI renderer. Empty input returns a nil slice. The semantic mapping
// to RowSpan roles lives in tui/chatprojection.
func ParsePlanBoxStruct(text string, contentWidth int) []PlanLine {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}
	if contentWidth < 20 {
		contentWidth = 20
	}
	out := make([]PlanLine, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			out = append(out, PlanLine{Text: "", Kind: PlanRowDefault})
			continue
		}
		truncated := truncateLineToWidth(line, contentWidth)
		kind := classifyPlanLine(trimmed)
		out = append(out, PlanLine{Text: truncated, Kind: kind})
	}
	return out
}

func classifyPlanLine(trimmed string) PlanRowKind {
	switch {
	case strings.HasPrefix(trimmed, "✓"):
		return PlanRowDone
	case strings.HasPrefix(trimmed, "→"):
		return PlanRowActive
	case strings.HasPrefix(trimmed, "✗"):
		return PlanRowFailed
	case strings.HasPrefix(trimmed, "!"):
		return PlanRowWarning
	case strings.HasPrefix(trimmed, "☐"):
		return PlanRowTodo
	case strings.HasPrefix(trimmed, "Plan:"), strings.HasPrefix(trimmed, "Goal:"):
		return PlanRowHeader
	case strings.HasPrefix(trimmed, "Suspended:"):
		return PlanRowSuspended
	default:
		return PlanRowDefault
	}
}

// PlanBoxContentWidth returns the content width for a plan box given the
// outer max width (box has Padding(0,1) so content area is maxWidth-2,
// clamped to a minimum of 20), matching the ANSI renderer.
func PlanBoxContentWidth(maxWidth int) int {
	w := maxWidth - 2
	if w < 20 {
		w = 20
	}
	return w
}

// PlanLineVisibleWidth is a thin wrapper so chatprojection does not need to
// import rendering just to measure plan text; it uses the shared grapheme
// width path.
func PlanLineVisibleWidth(s string) int { return rendering.VisibleWidth(s) }
