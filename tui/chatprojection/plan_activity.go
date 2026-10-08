package chatprojection

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types/activity"
)

// PlanActivityRows is the reading projection of the plan owner's immutable
// snapshot. Like transcript cards it shares status markers and wrapping, but
// it has no transcript gutter and never parses tool results or accesses storage.
func PlanActivityRows(p *activity.Plan, width int) []PaintLine {
	if p == nil || width < 1 {
		return nil
	}
	var rows []PaintLine
	add := func(text string, role ui.SemanticColor, attr cell.Attrs) {
		for _, line := range wrapPlanText(text, width) {
			rows = append(rows, PaintLine{Spans: []RowSpan{{Text: line, Role: role, Attr: attr}}})
		}
	}
	add(p.Objective, ui.ColorFg, cell.AttrBold)
	status := fmt.Sprintf("%d/%d completed", p.Completed, p.Total)
	if p.Status == "paused" {
		status = "Paused · " + status
	}
	add(status, ui.ColorMuted, 0)
	rows = append(rows, PaintLine{})
	for _, step := range p.Steps {
		marker, role := planStepMarker(step.Status)
		prefix := fmt.Sprintf("%s %d. ", marker, step.Number)
		// Even an extremely constrained reader reserves text space instead of
		// painting only an over-wide marker/number on every wrapped row.
		prefix = rendering.TruncateVisual(prefix, max(0, width-1), "")
		indent := strings.Repeat(" ", rendering.VisibleWidth(prefix))
		attr, textRole := cell.Attrs(0), ui.ColorFg
		switch step.Status {
		case "in_progress":
			attr = cell.AttrBold
		case "completed", "skipped", "cancelled":
			textRole = ui.ColorMuted
		}
		text := step.Text
		if step.Reason != "" {
			text += " · " + step.Reason
		}
		for i, line := range wrapPlanText(text, max(1, width-rendering.VisibleWidth(prefix))) {
			lead := prefix
			if i > 0 {
				lead = indent
			}
			rows = append(rows, PaintLine{Spans: []RowSpan{{Text: lead, Role: role}, {Text: line, Role: textRole, Attr: attr}}})
		}
	}
	return rows
}
