package chatprojection

import (
	"fmt"
	"strings"
	"time"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types/conversation"
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
func renderRecoveryMessage(message conversation.ChatMessage, ctx renderContext) messageLines {
	status := message.Recovery
	style, title := recoveryStyle(*status, ctx)
	header := recoveryHeader(message, ctx)
	rows := messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header},
		frameSensitive: status.State == recovery.Recovering && !status.RetryAt.IsZero()}
	if ctx.disclosure != DisclosureExpanded || ctx.innerW <= 0 {
		return rows
	}
	body := strings.TrimSpace(status.Summary)
	if body == title {
		body = "" // The heading already says it.
	}
	if detail := strings.TrimSpace(status.Detail); detail != "" && detail != body {
		if body != "" {
			body += "\n"
		}
		body += detail
	}
	guidance := strings.TrimSpace(status.Action)
	if guidance == "" && status.State == recovery.Failed {
		guidance = failedTurnGuidance
	}
	style.appendBody(&rows, ctx, body, guidance)
	return rows
}

// failedTurnGuidance is the next step after a turn ended on an error. The
// incident is settled, so there is nothing to retry: the user sends again.
const failedTurnGuidance = "Fix the cause, then send a message to continue."

// recoveryStyle maps an incident state to its sign, color and heading.
func recoveryStyle(status recovery.Status, ctx renderContext) (turnErrorStyle, string) {
	style, title := turnErrorStyle{marker: "■", role: ui.ColorError}, "Recovering"
	switch status.State {
	case recovery.NeedsAttention:
		style, title = turnErrorStyle{marker: "⚠", role: ui.ColorWarning}, "Needs attention"
	case recovery.Recovered:
		style, title = turnErrorStyle{marker: "✓", role: ui.ColorSuccess}, "Run recovered"
	case recovery.Interrupted:
		style, title = turnErrorStyle{marker: "■", role: ui.ColorMuted}, "Run interrupted"
	case recovery.Cancelled:
		style, title = turnErrorStyle{marker: "■", role: ui.ColorMuted}, "Recovery stopped"
	case recovery.Continued:
		style, title = turnErrorStyle{marker: "■", role: ui.ColorMuted}, "Run interrupted · continued with your message"
	case recovery.Failed:
		style, title = failedTurnStyle, "Turn failed"
	case recovery.Recovering:
		if status.RetryAt.IsZero() {
			// The owner has resumed the attempt. Retain the incident and its
			// evidence, but never animate a stale failure above ongoing work.
			// This is not a successful task finish; the checkpoint stays open.
			style, title = turnErrorStyle{marker: "○", role: ui.ColorMuted}, "Run resumed"
		} else {
			style.marker = ui.RecoverySquare(ctx.spinFrame)
		}
	}
	return style, title
}

func recoveryHeader(message conversation.ChatMessage, ctx renderContext) PaintLine {
	status := message.Recovery
	style, title := recoveryStyle(*status, ctx)
	meta, metaRole := "", ui.ColorMuted
	switch {
	case status.State == recovery.Failed:
		// The blocker is the turn's report; keep it readable without expanding.
		meta, _, _ = strings.Cut(rendering.SanitizeForTerminal(strings.TrimSpace(status.Detail)), "\n")
		metaRole = ui.ColorFg
	case status.State == recovery.Recovering && !status.RetryAt.IsZero():
		seconds := max(0, int(status.RetryAt.Sub(time.UnixMilli(int64(ctx.spinFrame))).Seconds()+0.999))
		meta = fmt.Sprintf("retry in %ds", seconds)
	}
	var action *RowSpan
	if status.CanRetry() {
		text := " (Retry now)"
		if status.State == recovery.NeedsAttention || status.State == recovery.Interrupted {
			text = " (Continue run)"
		}
		action = &RowSpan{Text: text, Role: ui.ColorAccent, Action: &ui.Action{
			ID:      ChatRecoveryRetryActionID,
			Payload: ChatRecoveryRetryPayload{AgentID: status.AgentID, IncidentID: status.ID, Generation: status.Generation},
		}}
	}
	return style.header(ctx, title, meta, metaRole, action)
}

// turnErrorStyle is the one presentation of a turn incident: a sign and a bold
// heading in the severity color, the readable reason, and, when expanded, the
// complete neutral diagnostic and the next step behind an edge in that color.
// A failed turn also sits on the error band, so it stands apart from tool rows
// and assistant text. The diagnostic itself is never tinted.
type turnErrorStyle struct {
	marker string
	role   ui.SemanticColor
	band   bool
}

var failedTurnStyle = turnErrorStyle{marker: "✕", role: ui.ColorError, band: true}

func (s turnErrorStyle) header(ctx renderContext, title, meta string, metaRole ui.SemanticColor, action *RowSpan) PaintLine {
	chevron := ctx.tokens.Glyphs().TreeCollapsed
	if ctx.disclosure == DisclosureExpanded {
		chevron = ctx.tokens.Glyphs().TreeExpanded
	}
	prefix := "│ " + s.marker + " "
	suffix := " " + chevron
	if action != nil {
		suffix = action.Text + suffix
	}
	space := ctx.innerW - rendering.VisibleWidth(prefix) - rendering.VisibleWidth(suffix)
	if space < 1 {
		// Keep the action complete on constrained grids; never paint a
		// partial label that appears clickable but has no usable hit target.
		prefix = ""
		space = ctx.innerW - rendering.VisibleWidth(suffix)
	}
	if space < 0 {
		return PaintLine{Spans: []RowSpan{{Text: rendering.TruncateVisual(title+" "+chevron, max(0, ctx.innerW), ""), Role: ui.ColorMuted}}}
	}
	line := PaintLine{}
	if prefix != "" {
		line.Spans = []RowSpan{{Text: "│ ", Role: ui.ColorMuted}, {Text: s.marker + " ", Role: s.role, Attr: cell.AttrBold}}
	}
	heading := rendering.TruncateVisual(title, space, "…")
	line.Spans = append(line.Spans, RowSpan{Text: heading, Role: s.role, Attr: cell.AttrBold})
	if rest := space - rendering.VisibleWidth(heading) - 3; meta != "" && rest > 0 {
		line.Spans = append(line.Spans, RowSpan{Text: " · ", Role: ui.ColorMuted}, RowSpan{Text: rendering.TruncateVisual(meta, rest, "…"), Role: metaRole})
	}
	if action != nil {
		line.Spans = append(line.Spans, *action)
	}
	line.Spans = append(line.Spans, RowSpan{Text: " " + chevron, Role: ui.ColorMuted})
	if s.band && prefix != "" {
		line = bandPaintLine(line, ui.ColorIncompleteBg, ctx.innerW)
	}
	return line
}

// appendBody wraps the diagnostic and the guidance at the real width, behind
// the severity edge. Very narrow grids keep only the text, so no evidence is cut.
func (s turnErrorStyle) appendBody(rows *messageLines, ctx renderContext, diagnostic, guidance string) {
	edge := ctx.tokens.Glyphs().FocusEdge
	lead := []RowSpan{{Text: "│ ", Role: ui.ColorMuted}, {Text: edge + " ", Role: s.role}}
	leadW := 3 + rendering.VisibleWidth(edge)
	if ctx.innerW < leadW+8 {
		lead, leadW = nil, 0
	}
	width := max(1, ctx.innerW-leadW)
	add := func(text string, role ui.SemanticColor) {
		for _, paragraph := range strings.Split(rendering.SanitizeForTerminal(text), "\n") {
			for _, part := range rendering.WordWrap(paragraph, width) {
				native := PaintLine{Spans: append(append([]RowSpan(nil), lead...), RowSpan{Text: part, Role: role})}
				if s.band && lead != nil {
					native = bandPaintLine(native, ui.ColorIncompleteBg, ctx.innerW)
				}
				rows.lines = append(rows.lines, native.PlainText())
				rows.native = append(rows.native, native)
			}
		}
	}
	if diagnostic != "" {
		add(diagnostic, ui.ColorFg)
	}
	if guidance != "" {
		add(guidance, ui.ColorMuted)
	}
}
