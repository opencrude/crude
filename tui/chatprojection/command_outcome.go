package chatprojection

import (
	"fmt"
	"strings"

	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types"
	"crude/types/command"
)

func isCommandOutcome(msg *types.ChatMessage) bool {
	return msg != nil && msg.Collaboration != nil && msg.Collaboration.Kind == "command_outcome"
}

func renderCommandOutcome(msg types.ChatMessage, ctx renderContext) messageLines {
	title := tools.ToolTitle{Action: "Background command settled"}
	status := types.ToolStatusSuccess
	info := msg.Collaboration.Command
	if info != nil {
		title.Action, title.Operand = "Completed command", info.Handle
		if info.Status == "failed" || info.ExitKnown && info.ExitCode != 0 {
			title.Action, title.Failed, status = "Command failed", true, types.ToolStatusError
		} else if info.Status == "killed" {
			title.Action, title.Failed, status = "Command timed out", true, types.ToolStatusError
		}
		if info.ExitKnown && info.ExitCode != 0 {
			title.Metadata = fmt.Sprintf("(exit %d)", info.ExitCode)
		}
		if info.Duration > 0 {
			title.Metadata = strings.TrimSpace(title.Metadata + " " + rendering.FormatDuration(info.Duration))
		}
	}
	header := paintToolTitle(title, status, false, true, ctx.spinFrame, "")
	glyph := " ▸"
	if ctx.disclosure == DisclosureExpanded {
		glyph = " ▾"
	}
	header.Spans = append(header.Spans, RowSpan{Text: glyph, Role: ui.ColorMuted})
	var out messageLines
	for _, line := range wrapToolTitle(header, ctx.innerW) {
		out.lines = append(out.lines, line.PlainText())
		out.native = append(out.native, line)
	}
	if ctx.disclosure != DisclosureExpanded {
		return out
	}
	if info == nil {
		// Evidence without a typed outcome remains readable, never parsed
		// into guessed status/command facts or painted as an oversized peer card.
		out.appendPlainToolBody("execute_command", msg.Content, ctx.innerW, ctx.height, false, true)
		return out
	}
	text := fmt.Sprintf("Command: %s\nHandle: %s\nStatus: %s\nPID: %d\nCaptured output: %s",
		info.Command, info.Handle, info.Status, info.PID, rendering.FormatBytes(int64(info.OutputBytes)))
	if info.ExitKnown {
		text += fmt.Sprintf("\nExit code: %d", info.ExitCode)
	} else {
		text += "\nExit code: unavailable"
	}
	if !info.StartedAt.IsZero() {
		text += "\nStarted: " + info.StartedAt.Format("2006-01-02 15:04:05 MST")
	}
	if !info.EndedAt.IsZero() {
		text += "\nEnded: " + info.EndedAt.Format("2006-01-02 15:04:05 MST")
	}
	if info.Error != "" {
		text += "\nReason: " + info.Error
	}
	out.appendPlainToolBody("execute_command", text, ctx.innerW, ctx.height, false, true)
	if info.Output != nil {
		out.appendPlainToolBody("execute_command", tools.CommandDetails(*info.Output), ctx.innerW, ctx.height, false, true)
		out.appendPlainToolBody("execute_command", commandOutcomeOutput(*info.Output), ctx.innerW, ctx.height, false, true)
	}
	return out
}

func commandOutcomeOutput(result command.Result) string {
	if result.Output == "" {
		return "(no output captured)"
	}
	return tools.CommandOutput(result)
}
