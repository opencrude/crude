package chatprojection

import (
	"fmt"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
)

func recoveryEqual(left, right *recovery.Status) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// A recovery is a compact ordinary transcript item, with diagnostic evidence
// behind the same disclosure contract as other chat projections.
func renderRecoveryMessage(message types.ChatMessage, ctx renderContext) messageLines {
	status := message.Recovery
	header := recoveryHeader(message, ctx)
	rows := messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header},
		frameSensitive: status.State == recovery.Recovering}
	if ctx.disclosure != DisclosureExpanded || ctx.innerW <= 0 {
		return rows
	}
	width := max(1, ctx.innerW)
	body := strings.TrimSpace(status.Summary)
	if detail := strings.TrimSpace(status.Detail); detail != "" && detail != body {
		if body != "" {
			body += "\n"
		}
		body += detail
	}
	for _, paragraph := range strings.Split(rendering.SanitizeForTerminal(body), "\n") {
		for _, text := range rendering.WordWrap(paragraph, width) {
			native := PaintLine{Spans: []RowSpan{{Text: text, Role: ui.ColorFg}}}
			rows.lines = append(rows.lines, native.PlainText())
			rows.native = append(rows.native, native)
		}
	}
	for _, text := range rendering.WordWrap(rendering.SanitizeForTerminal(status.Action), max(1, ctx.innerW)) {
		native := PaintLine{Spans: []RowSpan{{Text: text, Role: ui.ColorMuted}}}
		rows.lines = append(rows.lines, native.PlainText())
		rows.native = append(rows.native, native)
	}
	return rows
}

func recoveryHeader(message types.ChatMessage, ctx renderContext) PaintLine {
	status := message.Recovery
	label, role, marker := "Recovering", ui.ColorError, "■"
	switch status.State {
	case recovery.NeedsAttention:
		label = "Needs attention"
	case recovery.Recovered:
		label, role, marker = "Run recovered", ui.ColorSuccess, "✓"
	case recovery.Interrupted:
		label, role = "Run interrupted", ui.ColorMuted
	case recovery.Cancelled:
		label, role = "Recovery stopped", ui.ColorMuted
	}
	if status.State == recovery.Recovering {
		marker = ui.RecoverySquare(ctx.spinFrame)
		if !status.RetryAt.IsZero() {
			seconds := max(0, int(status.RetryAt.Sub(time.UnixMilli(int64(ctx.spinFrame))).Seconds()+0.999))
			label += fmt.Sprintf(" · retry in %ds", seconds)
		} else {
			label += " · retrying"
		}
	}
	chevron := ctx.tokens.Glyphs().TreeCollapsed
	if ctx.disclosure == DisclosureExpanded {
		chevron = ctx.tokens.Glyphs().TreeExpanded
	}
	prefix := "│ " + marker + " "
	actionable := status.State == recovery.Recovering && !status.RetryAt.IsZero() ||
		status.State == recovery.NeedsAttention || status.State == recovery.Interrupted
	actionText := " (Retry now)"
	suffix := " " + chevron
	if actionable {
		suffix = actionText + suffix
	}
	space := ctx.innerW - rendering.VisibleWidth(prefix) - rendering.VisibleWidth(suffix)
	if space < 1 {
		// Keep the action complete on constrained grids; never paint a
		// partial label that appears clickable but has no usable hit target.
		prefix = ""
		space = ctx.innerW - rendering.VisibleWidth(suffix)
	}
	if space < 0 {
		return PaintLine{Spans: []RowSpan{{Text: rendering.TruncateVisual(label+" "+chevron, max(0, ctx.innerW), ""), Role: ui.ColorMuted}}}
	}
	line := PaintLine{}
	if prefix != "" {
		line.Spans = []RowSpan{{Text: "│ ", Role: ui.ColorMuted}, {Text: marker + " ", Role: role}}
	}
	line.Spans = append(line.Spans, RowSpan{Text: rendering.TruncateVisual(label, space, "…"), Role: ui.ColorMuted})
	if actionable {
		line.Spans = append(line.Spans, RowSpan{Text: actionText, Role: ui.ColorAccent, Action: &ui.Action{
			ID:      ChatRecoveryRetryActionID,
			Payload: ChatRecoveryRetryPayload{AgentID: status.AgentID, IncidentID: status.ID, Generation: status.Generation},
		}})
	}
	line.Spans = append(line.Spans, RowSpan{Text: " " + chevron, Role: ui.ColorMuted})
	return line
}
