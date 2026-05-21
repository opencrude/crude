package widgets

import (
	"strings"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/types"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

func RenderAllMessages(messages []*types.ChatMessage, th *theme.Theme, width int, spinFrame int, mdRenderer *glamour.TermRenderer, modelName string, streaming bool, height int, username string, phase string, activeTyping bool) []string {
	var all []string

	padLR := th.Spacing.PadLR
	contentWidth := width - padLR*2 - 16
	if contentWidth < 10 {
		contentWidth = 10
	}
	innerW := width - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	for msgIdx := range messages {
		msg := messages[msgIdx]
		isLastMsg := msgIdx == len(messages)-1

		all = append(all, "")

		// Use cached lines if available
		if msg.CachedLines != nil {
			all = append(all, msg.CachedLines...)
			if msg.CursorLine >= 0 && msg.CursorLine < len(msg.CachedLines) &&
				isLastMsg && (streaming || msg.ThinkingActive) {
				cursor := blockCursor(spinFrame, th.Colors.Accent, activeTyping)
				all[len(all)-len(msg.CachedLines)+msg.CursorLine] += cursor
			}
			continue
		}

		timeLabel := rendering.FormatTimeLabel(msg.Time)

		var msgLines []string
		cursorLine := -1

		switch msg.Role {
		case types.RoleUser:
			msgLines = RenderUserMessage(*msg, th, contentWidth, innerW, timeLabel, mdRenderer, username, false)
		case types.RoleAssistant:
			if streaming && isLastMsg {
				updateStreamingCache(msg, mdRenderer, contentWidth)
			}
			msgLines = RenderAssistantMessage(*msg, th, contentWidth, innerW, timeLabel, mdRenderer, modelName, streaming, isLastMsg, false)
			if streaming && isLastMsg && len(msgLines) > 0 {
				cursorLine = len(msgLines) - 1
			}
		case types.RoleThinking:
			msgLines = RenderThinkingMessage(*msg, th, contentWidth, innerW, spinFrame, modelName, timeLabel, false)
			if msg.ThinkingActive && isLastMsg && len(msgLines) > 0 {
				cursorLine = len(msgLines) - 1
			}
		case types.RoleTool:
			msgLines = tools.RenderToolMessage(*msg, th, contentWidth, innerW, width, spinFrame, height, phase)
		case types.RoleError:
			msgLines = RenderErrorMessage(*msg, th, contentWidth, innerW, timeLabel)
		}

		prevLen := len(all)
		all = append(all, msgLines...)

		// Cache the rendered lines (without cursor)
		msg.CachedLines = make([]string, len(msgLines))
		copy(msg.CachedLines, msgLines)
		msg.CursorLine = cursorLine

		if cursorLine >= 0 && cursorLine < len(msgLines) {
			cursor := blockCursor(spinFrame, th.Colors.Accent, activeTyping)
			all[prevLen+cursorLine] = all[prevLen+cursorLine] + cursor
		}
	}

	return all
}

func blockCursor(frame int, color lipgloss.Color, activeTyping bool) string {
	if activeTyping {
		return components.BlockCursorSolid(color)
	}
	return components.BlockCursor(frame, color)
}

func renderMarkdown(content string, mdRenderer *glamour.TermRenderer) (string, bool) {
	if mdRenderer == nil {
		return "", false
	}
	r, err := mdRenderer.Render(content)
	if err != nil {
		return "", false
	}
	return strings.TrimRight(r, "\n"), true
}

func updateStreamingCache(msg *types.ChatMessage, mdRenderer *glamour.TermRenderer, contentWidth int) {
	lastNL := strings.LastIndex(msg.Content, "\n")
	completedLen := 0
	if lastNL >= 0 {
		completedLen = lastNL + 1
	}
	if msg.StreamCompletedLen == completedLen {
		return
	}
	if completedLen == 0 {
		msg.StreamCompletedLines = nil
		msg.StreamCompletedLen = 0
		return
	}
	completed := msg.Content[:completedLen]
	rendered, isMarkdown := renderMarkdown(completed, mdRenderer)
	var lines []string
	if isMarkdown {
		lines = strings.Split(rendered, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	} else {
		lines = rendering.WrapText(strings.TrimRight(completed, "\n"), contentWidth)
	}
	msg.StreamCompletedLines = lines
	msg.StreamCompletedLen = completedLen
}

func RenderUserMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, timeLabel string, mdRenderer *glamour.TermRenderer, username string, suppressHeader bool) []string {
	var userLines []string

	rendered, isMarkdown := renderMarkdown(msg.Content, mdRenderer)
	if !isMarkdown {
		userLines = rendering.WrapText(msg.Content, contentWidth)
	} else {
		userLines = strings.Split(rendered, "\n")
	}

	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	if !suppressHeader {
		labelStyle := th.Styles.UserLabel
		userLines = append([]string{labelStyle.Render("❯ " + username + " ")}, userLines...)
	}

	if !suppressHeader {
		timeW := len(timeLabel)
		timePart := dim.Render(timeLabel)
		lineW := lipgloss.Width(userLines[0])
		sp := innerW - lineW - timeW
		if sp < 1 {
			sp = 1
		}
		userLines[0] = userLines[0] + strings.Repeat(" ", sp) + timePart
	}

	return userLines
}

func RenderAssistantMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, timeLabel string, mdRenderer *glamour.TermRenderer, modelName string, streaming bool, isLastMsg bool, suppressHeader bool) []string {
	var asstLines []string

	if streaming && isLastMsg {
		lastNL := strings.LastIndex(msg.Content, "\n")
		var lastLineText string
		if lastNL >= 0 {
			lastLineText = msg.Content[lastNL+1:]
		} else {
			lastLineText = msg.Content
		}
		if len(msg.StreamCompletedLines) > 0 {
			asstLines = make([]string, len(msg.StreamCompletedLines))
			copy(asstLines, msg.StreamCompletedLines)
		}
		lastLineText = strings.TrimRight(lastLineText, "\n")
		if lastLineText != "" {
			lastLines := rendering.WrapText(lastLineText, contentWidth)
			asstLines = append(asstLines, lastLines...)
		}
	} else {
		rendered, isMarkdown := renderMarkdown(msg.Content, mdRenderer)
		if !isMarkdown {
			asstLines = rendering.WrapText(msg.Content, contentWidth)
		} else {
			asstLines = strings.Split(rendered, "\n")
		}
	}

	if len(asstLines) == 0 {
		asstLines = []string{""}
	}

	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	if !suppressHeader {
		var headerLabel string
		if msg.Incomplete {
			incompleteLabelStyle := lipgloss.NewStyle().
				Foreground(th.Colors.Error).
				Italic(true).
				Bold(true).
				Background(lipgloss.Color("#3a1a1a"))
			headerLabel = incompleteLabelStyle.Render("❯ ⚠ Crude " + modelName + " ")
		} else {
			headerLabel = th.Styles.AsstLabel.Render("❯ Crude " + modelName + " ")
		}
		asstLines = append([]string{headerLabel}, asstLines...)
	}

	if !suppressHeader {
		timeW := len(timeLabel)
		timePart := dim.Render(timeLabel)
		lineW := lipgloss.Width(asstLines[0])
		sp := innerW - lineW - timeW
		if sp < 1 {
			sp = 1
		}
		asstLines[0] = asstLines[0] + strings.Repeat(" ", sp) + timePart
	}

	if msg.Incomplete && msg.IncompleteReason != "" {
		reasonStyle := lipgloss.NewStyle().Foreground(th.Colors.Error).Italic(true)
		reasonText := reasonStyle.Render(" (" + msg.IncompleteReason + ")")
		lastIdx := len(asstLines) - 1
		if lastIdx >= 0 {
			asstLines[lastIdx] = asstLines[lastIdx] + reasonText
		}
	}

	return asstLines
}

func RenderThinkingMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, spinFrame int, modelName string, timeLabel string, suppressHeader bool) []string {
	var result []string
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)

	if !suppressHeader {
		var headerLabel string
		if msg.Incomplete {
			incompleteLabelStyle := lipgloss.NewStyle().
				Foreground(th.Colors.Error).
				Italic(true).
				Bold(true).
				Background(lipgloss.Color("#3a1a1a"))
			headerLabel = incompleteLabelStyle.Render("❯ ⚠ Crude " + modelName + " ")
		} else {
			headerLabel = th.Styles.AsstLabel.Render("❯ Crude " + modelName + " ")
		}
		result = append(result, headerLabel, "")

		timeW := len(timeLabel)
		lineW := lipgloss.Width(headerLabel)
		sp := innerW - lineW - timeW
		if sp < 1 {
			sp = 1
		}
		result[0] = headerLabel + strings.Repeat(" ", sp) + dim.Render(timeLabel)
	}

	lines := rendering.WrapText(msg.Content, contentWidth)
	borderPrefix := lipgloss.NewStyle().Foreground(th.Colors.Border).Render("│ ")
	thinkingStyle := th.Styles.ThinkingLine

	for i, line := range lines {
		if i == 0 {
			if msg.ThinkingActive {
				alpha := components.ThinkingDotIndex(spinFrame)
				dotColors := []string{"#f0883e", "#d0681e", "#b85010", "#d0681e"}
				dotColor := dotColors[alpha%len(dotColors)]
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(lipgloss.Color(dotColor)).Render("● ")+thinkingStyle.Render(line))
			} else if msg.ThinkingInterrupted {
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(th.Colors.Warning).Render("● ")+thinkingStyle.Render(line))
			} else {
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("● ")+thinkingStyle.Render(line))
			}
		} else {
			result = append(result, borderPrefix+"  "+thinkingStyle.Render(line))
		}
	}

	if msg.Incomplete && msg.IncompleteReason != "" && len(result) > 1 {
		reasonStyle := lipgloss.NewStyle().Foreground(th.Colors.Error).Italic(true)
		reasonText := reasonStyle.Render(" (" + msg.IncompleteReason + ")")
		result[len(result)-1] = result[len(result)-1] + reasonText
	}

	return result
}

func RenderErrorMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, timeLabel string) []string {
	lines := rendering.WrapText(msg.Content, contentWidth)
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)

	timeW := len(timeLabel)
	timePart := dim.Render(timeLabel)
	for i, line := range lines {
		styled := th.Styles.ErrorText.Render("  " + line)
		if i == 0 {
			label := th.Styles.ErrorText.Render("error")
			styled = label + " " + th.Styles.ErrorText.Render(line)
			sp := innerW - lipgloss.Width(styled) - timeW
			if sp < 1 {
				sp = 1
			}
			lines[i] = styled + strings.Repeat(" ", sp) + timePart
		} else {
			lines[i] = styled
		}
	}
	return lines
}
