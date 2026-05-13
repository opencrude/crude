package widgets

import (
	"strings"
	"time"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
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

		timeLine1, timeLine2 := FormatTimeLabel(msg.Time)
		timeW := rendering.Max(len(timeLine1), len(timeLine2))

		var msgLines []string
		cursorLine := -1

		switch msg.Role {
		case types.RoleUser:
			msgLines = RenderUserMessage(*msg, th, contentWidth, innerW, timeLine1, timeLine2, timeW, mdRenderer, username)
	case types.RoleAssistant:
		if streaming && isLastMsg {
			updateStreamingCache(msg, mdRenderer, contentWidth)
		}
		msgLines = RenderAssistantMessage(*msg, th, contentWidth, innerW, timeLine1, timeLine2, timeW, mdRenderer, modelName, streaming, isLastMsg)
		if streaming && isLastMsg && len(msgLines) > 0 {
			cursorLine = len(msgLines) - 1
		}
		case types.RoleThinking:
			msgLines = RenderThinkingMessage(*msg, th, contentWidth, innerW, spinFrame)
			if msg.ThinkingActive && isLastMsg && len(msgLines) > 0 {
				cursorLine = len(msgLines) - 1
			}
		case types.RoleTool:
			msgLines = RenderToolMessage(*msg, th, contentWidth, innerW, width, spinFrame, height, phase)
		case types.RoleError:
			msgLines = RenderErrorMessage(*msg, th, contentWidth, innerW, timeLine1, timeLine2, timeW)
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

func RenderUserMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, timeLine1, timeLine2 string, timeW int, mdRenderer *glamour.TermRenderer, username string) []string {
	var userLines []string

	rendered, isMarkdown := renderMarkdown(msg.Content, mdRenderer)
	if !isMarkdown {
		userLines = rendering.WrapText(msg.Content, contentWidth)
	} else {
		userLines = strings.Split(rendered, "\n")
	}

	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	labelStyle := th.Styles.UserLabel
	if len(userLines) > 0 {
		label := labelStyle.Render("❯ " + username + " ")
		userLines[0] = label + userLines[0]
	}

	for i, line := range userLines {
		var timePart string
		if i == 0 {
			timePart = dim.Render(timeLine1)
		} else if i == 1 {
			timePart = dim.Render(timeLine2)
		}
		if timePart != "" {
			lineW := lipgloss.Width(line)
			sp := innerW - lineW - timeW
			if sp < 1 {
				sp = 1
			}
			userLines[i] = line + strings.Repeat(" ", sp) + timePart
		}
	}

	if len(userLines) == 1 {
		sp := innerW - timeW
		if sp < 0 {
			sp = 0
		}
		userLines = append(userLines, strings.Repeat(" ", sp)+dim.Render(timeLine2))
	}

	return userLines
}

func RenderAssistantMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, timeLine1, timeLine2 string, timeW int, mdRenderer *glamour.TermRenderer, modelName string, streaming bool, isLastMsg bool) []string {
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
		lastLines := rendering.WrapText(lastLineText, contentWidth)
		asstLines = append(asstLines, lastLines...)
	} else {
		rendered, isMarkdown := renderMarkdown(msg.Content, mdRenderer)
		if !isMarkdown {
			asstLines = rendering.WrapText(msg.Content, contentWidth)
		} else {
			asstLines = strings.Split(rendered, "\n")
		}
	}

	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	labelStyle := th.Styles.AsstLabel
	if len(asstLines) > 0 {
		label := labelStyle.Render("❯ Crude " + modelName + " ")
		asstLines[0] = label + asstLines[0]
	}

	for i, line := range asstLines {
		isCursorLine := streaming && isLastMsg && i == len(asstLines)-1
		var timePart string
		if i == 0 && !isCursorLine {
			timePart = dim.Render(timeLine1)
		} else if i == 1 && !isCursorLine {
			timePart = dim.Render(timeLine2)
		}
		if timePart != "" {
			lineW := lipgloss.Width(line)
			sp := innerW - lineW - timeW
			if sp < 1 {
				sp = 1
			}
			asstLines[i] = line + strings.Repeat(" ", sp) + timePart
		} else if isCursorLine {
			asstLines[i] = rendering.TrimRightAnsiSpaces(line)
		}
	}

	if len(asstLines) == 1 && !(streaming && isLastMsg) {
		sp := innerW - timeW
		if sp < 0 {
			sp = 0
		}
		asstLines = append(asstLines, strings.Repeat(" ", sp)+dim.Render(timeLine2))
	}

	return asstLines
}

func RenderThinkingMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, spinFrame int) []string {
	lines := rendering.WrapText(msg.Content, contentWidth)
	borderPrefix := lipgloss.NewStyle().Foreground(th.Colors.Border).Render("│ ")
	thinkingStyle := th.Styles.ThinkingLine

	var result []string
	for i, line := range lines {
		if i == 0 {
		if msg.ThinkingActive {
			alpha := components.ThinkingDotIndex(spinFrame)
				dotColors := []string{"#f0883e", "#d0681e", "#b85010", "#d0681e"}
				dotColor := dotColors[alpha%len(dotColors)]
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(lipgloss.Color(dotColor)).Render("● ")+thinkingStyle.Render(line))
			} else {
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("● ")+thinkingStyle.Render(line))
			}
		} else {
			result = append(result, borderPrefix+"  "+thinkingStyle.Render(line))
		}
	}
	return result
}

func RenderErrorMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW int, timeLine1, timeLine2 string, timeW int) []string {
	lines := rendering.WrapText(msg.Content, contentWidth)
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)

	for i, line := range lines {
		styled := th.Styles.ErrorText.Render("  " + line)
		if i == 0 {
			label := th.Styles.ErrorText.Render("error")
			styled = label + " " + th.Styles.ErrorText.Render(line)
			sp := innerW - lipgloss.Width(styled) - timeW
			if sp < 1 {
				sp = 1
			}
			lines[i] = styled + strings.Repeat(" ", sp) + dim.Render(timeLine1)
		} else if i == 1 {
			sp := innerW - lipgloss.Width(styled) - timeW
			if sp < 1 {
				sp = 1
			}
			lines[i] = styled + strings.Repeat(" ", sp) + dim.Render(timeLine2)
		} else {
			lines[i] = styled
		}
	}
	if len(lines) == 1 {
		sp := innerW - timeW
		if sp < 0 {
			sp = 0
		}
		lines = append(lines, strings.Repeat(" ", sp)+dim.Render(timeLine2))
	}
	return lines
}

func FormatTimeLabel(t time.Time) (string, string) {
	line1 := t.Format("15:04")
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if t.After(todayStart) {
		return line1, "today"
	}
	yesterdayStart := todayStart.Add(-24 * time.Hour)
	if t.After(yesterdayStart) {
		return line1, "yesterday"
	}
	return line1, t.Format("01/02")
}
