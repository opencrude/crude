package chatprojection

import (
	"strings"

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

// Recovery diagnostics use the same native notice hierarchy as other system
// boundaries: colored title, ordinary diagnostic ink, quiet actionable guidance.
// Countdown belongs in the fixed strip, never in reflowing transcript rows.
func renderRecoveryMessage(message types.ChatMessage, ctx renderContext) messageLines {
	status := message.Recovery
	title, role := "Recovering · work kept", ui.ColorWarning
	switch status.State {
	case recovery.NeedsAttention:
		title, role = "Needs attention · work kept", ui.ColorError
	case recovery.Recovered:
		title, role = "Run recovered", ui.ColorSuccess
	case recovery.Interrupted:
		title, role = "Run interrupted · work kept", ui.ColorWarning
	case recovery.Cancelled:
		title, role = "Recovery stopped · work kept", ui.ColorMuted
	}
	body := strings.TrimSpace(status.Summary)
	if detail := strings.TrimSpace(status.Detail); detail != "" && detail != body {
		if body != "" {
			body += "\n"
		}
		body += detail
	}
	message.Content = rendering.SanitizeForTerminal(body)
	rows := renderNoticeMessage(message, ctx, title, role)
	if ctx.innerW <= 0 || status.Action == "" {
		return rows
	}
	width := ctx.innerW
	if ctx.contentWidth > 0 {
		width = min(width, ctx.contentWidth)
	}
	for _, line := range rendering.WordWrap(rendering.SanitizeForTerminal(status.Action), width) {
		native := PaintLine{Spans: []RowSpan{{Text: line, Role: ui.ColorMuted}}}
		rows.lines = append(rows.lines, native.PlainText())
		rows.native = append(rows.native, native)
	}
	return rows
}
