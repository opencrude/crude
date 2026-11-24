package chatprojection

import (
	"fmt"
	"strings"

	toolargs "crude/agent/tools"
	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types/command"
	"crude/types/conversation"
)

func isCommandOutcome(msg *conversation.ChatMessage) bool {
	return msg != nil && msg.Collaboration != nil && msg.Collaboration.Kind == "command_outcome"
}

func renderCommandOutcome(msg conversation.ChatMessage, ctx renderContext) messageLines {
	title := tools.ToolTitle{Action: "Background command settled"}
	status := conversation.ToolStatusSuccess
	info := msg.Collaboration.Command
	if info != nil {
		title.Action, title.Operand = "Completed command", info.Handle
		if info.Uncertain {
			title.Action, title.Warning, status = "Command outcome unknown", true, conversation.ToolStatusError
		} else if info.TimedOut {
			title.Action, title.Failed, status = "Command timed out", true, conversation.ToolStatusError
		} else if info.Status == "failed" || info.ExitKnown && info.ExitCode != 0 {
			title.Action, title.Failed, status = "Command failed", true, conversation.ToolStatusError
		} else if info.Status == "killed" {
			title.Action, status = "Command stopped", conversation.ToolStatusCancelled
		}
		if info.TaskID != "" {
			title.Operand = info.Command
			switch {
			case info.Uncertain:
				title.Action = "Outcome unknown"
			case info.TimedOut:
				title.Action = "Timed out"
			case info.Status == "failed":
				title.Action = "Failed"
			case info.Status == "killed":
				title.Action = "Stopped"
			default:
				title.Action = "Completed"
			}
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
	suffix := []RowSpan{{Text: glyph, Role: ui.ColorMuted}}
	var out messageLines
	lines := wrapCompactToolTitle(header, ctx.innerW, suffix)
	if ctx.disclosure == DisclosureExpanded {
		header.Spans = append(header.Spans, suffix...)
		lines = wrapToolTitle(header, ctx.innerW)
	}
	for _, line := range lines {
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
	if msg.ToolName != "" {
		if initial, ok := toolargs.ParseCommandResult(msg.ToolName, msg.Content); ok {
			out.appendPlainToolBody("execute_command", "Initial result:\n"+tools.CommandMetadata(initial), ctx.innerW, ctx.height, false, true)
			if output := tools.CommandOutput(initial); output != "" {
				out.appendPlainToolBody("execute_command", output, ctx.innerW, ctx.height, false, true)
			}
		}
	}
	text := fmt.Sprintf("Command: %s\nHandle: %s\nStatus: %s\nCaptured output: %s",
		info.Command, info.Handle, info.Status, rendering.FormatBytes(int64(info.OutputBytes)))
	if info.PID > 0 {
		text += fmt.Sprintf("\nPID: %d", info.PID)
	}
	if info.ExitKnown {
		text += fmt.Sprintf("\nExit code: %d", info.ExitCode)
	} else if info.TaskID == "" {
		text += "\nExit code: unavailable"
	}
	if !info.StartedAt.IsZero() {
		text += "\nStarted: " + info.StartedAt.Local().Format("2006-01-02 15:04:05 MST")
	}
	if !info.EndedAt.IsZero() {
		text += "\nEnded: " + info.EndedAt.Local().Format("2006-01-02 15:04:05 MST")
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
