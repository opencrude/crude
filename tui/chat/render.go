package chat

import (
	"fmt"
	"strings"
	"time"

	ttypes "crude/tui/types"
	"crude/tui/widget"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func RenderAllMessages(messages []ChatMessage, theme *widget.Theme, width int, spinFrame int, mdRenderer *glamour.TermRenderer, modelName string, streaming bool, height int, username string) []string {
	var all []string

	padLR := theme.Spacing.PadLR
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

		timeLine1, timeLine2 := FormatTimeLabel(msg.Time)
		timeW := widget.Max(len(timeLine1), len(timeLine2))

		var msgLines []string
		cursorLine := -1

		switch msg.Role {
		case RoleUser:
			msgLines = RenderUserMessage(msg, theme, contentWidth, innerW, timeLine1, timeLine2, timeW, mdRenderer, username)
		case RoleAssistant:
			msgLines = RenderAssistantMessage(msg, theme, contentWidth, innerW, timeLine1, timeLine2, timeW, mdRenderer, modelName, streaming, isLastMsg)
			if streaming && isLastMsg && len(msgLines) > 0 {
				cursorLine = len(msgLines) - 1
			}
		case RoleThinking:
			msgLines = RenderThinkingMessage(msg, theme, contentWidth, innerW, spinFrame)
			if msg.ThinkingActive && isLastMsg && len(msgLines) > 0 {
				cursorLine = len(msgLines) - 1
			}
		case RoleTool:
			msgLines = RenderToolMessage(msg, theme, contentWidth, innerW, width, spinFrame, height)
		case RoleError:
			msgLines = RenderErrorMessage(msg, theme, contentWidth, innerW, timeLine1, timeLine2, timeW)
		}

		prevLen := len(all)
		all = append(all, msgLines...)
		if cursorLine >= 0 && cursorLine < len(msgLines) {
			cursor := blockCursor(spinFrame, theme.Colors.Accent)
			all[prevLen+cursorLine] = all[prevLen+cursorLine] + cursor
		}
	}

	return all
}

func blockCursor(frame int, color lipgloss.Color) string {
	if frame%2 == 0 {
		return lipgloss.NewStyle().Foreground(color).Render("█")
	}
	return " "
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

func RenderUserMessage(msg ChatMessage, theme *widget.Theme, contentWidth, innerW int, timeLine1, timeLine2 string, timeW int, mdRenderer *glamour.TermRenderer, username string) []string {
	var userLines []string

	rendered, isMarkdown := renderMarkdown(msg.Content, mdRenderer)
	if !isMarkdown {
		userLines = widget.WrapText(msg.Content, contentWidth)
	} else {
		userLines = strings.Split(rendered, "\n")
	}

	dim := lipgloss.NewStyle().Foreground(theme.Colors.Muted)
	labelStyle := theme.Styles.UserLabel
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

func RenderAssistantMessage(msg ChatMessage, theme *widget.Theme, contentWidth, innerW int, timeLine1, timeLine2 string, timeW int, mdRenderer *glamour.TermRenderer, modelName string, streaming bool, isLastMsg bool) []string {
	var asstLines []string

	rendered, isMarkdown := renderMarkdown(msg.Content, mdRenderer)
	if !isMarkdown {
		asstLines = widget.WrapText(msg.Content, contentWidth)
	} else {
		asstLines = strings.Split(rendered, "\n")
	}

	dim := lipgloss.NewStyle().Foreground(theme.Colors.Muted)
	labelStyle := theme.Styles.AsstLabel
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
			asstLines[i] = widget.TrimRightAnsiSpaces(line)
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

func RenderThinkingMessage(msg ChatMessage, theme *widget.Theme, contentWidth, innerW int, spinFrame int) []string {
	lines := widget.WrapText(msg.Content, contentWidth)
	borderPrefix := lipgloss.NewStyle().Foreground(theme.Colors.Border).Render("│ ")
	thinkingStyle := theme.Styles.ThinkingLine

	var result []string
	for i, line := range lines {
		if i == 0 {
			if msg.ThinkingActive {
				alpha := (spinFrame % 4) + 1
				dotColors := []string{"#f0883e", "#d0681e", "#b85010", "#d0681e"}
				dotColor := dotColors[alpha%len(dotColors)]
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(lipgloss.Color(dotColor)).Render("● ")+thinkingStyle.Render(line))
			} else {
				result = append(result, borderPrefix+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render("● ")+thinkingStyle.Render(line))
			}
		} else {
			result = append(result, borderPrefix+"  "+thinkingStyle.Render(line))
		}
	}
	return result
}

func RenderToolMessage(msg ChatMessage, theme *widget.Theme, contentWidth, innerW, width, spinFrame, height int) []string {
	dim := lipgloss.NewStyle().Foreground(theme.Colors.Muted)
	isActive := msg.ToolStatus == "streaming" || msg.ToolStatus == "calling"

	spinColor := theme.Colors.Spinner.Default
	if msg.ToolStatus == "success" {
		spinColor = theme.Colors.Spinner.Done
	} else if msg.ToolStatus == "error" {
		spinColor = theme.Colors.Spinner.Error
	}
	spinStyle := lipgloss.NewStyle().Foreground(spinColor)
	nameStyle := lipgloss.NewStyle().Foreground(theme.Colors.Muted)
	title := buildToolTitle(msg)

	var msgLines []string

	if isActive {
		var dot string
		if spinFrame%2 == 0 {
			dot = "● "
		} else {
			dot = "○ "
		}
		line := dim.Render("│ ") + spinStyle.Render(dot) + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	} else if msg.ToolStatus == "success" && msg.ToolName == "plan" {
	} else if msg.ToolStatus == "success" {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	} else if msg.ToolStatus == "error" {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	} else {
		line := dim.Render("│ ") + spinStyle.Render("○ ") + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	}

	if msg.ToolStatus == "success" && msg.Content != "" {
		resultContent := msg.Content
		fullLabel := pascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
		resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
		resultContent = strings.TrimSpace(resultContent)
		if resultContent != "" {
			if IsPlanTool(msg.ToolName) {
				planBox := renderPlanBox(resultContent, contentWidth-4, theme)
				for _, pl := range strings.Split(planBox, "\n") {
					msgLines = append(msgLines, dim.Render("│ ")+pl)
				}
			} else if msg.ToolName == "read_file" {
				path := msg.ToolArgs
				if path == "" {
					path = msg.ToolProgressLabel
				}
				if path == "" {
					path = "file"
				}
				if strings.Contains(resultContent, "— (empty file)") {
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render("(empty file)"))
				} else if strings.HasPrefix(resultContent, "Error:") {
					firstLine := strings.SplitN(resultContent, "\n", 2)[0]
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(firstLine))
				} else {
					contentLines := len(strings.Split(resultContent, "\n"))
					if strings.HasPrefix(resultContent, "Lines") {
						contentLines--
					}
					if contentLines < 1 {
						contentLines = 1
					}
					suffix := "s"
					if contentLines == 1 {
						suffix = ""
					}
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(fmt.Sprintf("Read %d line%s from %s", contentLines, suffix, path)))
					if strings.HasPrefix(resultContent, "Lines") {
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render("… partial content returned to agent"))
					} else {
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render("… full content returned to agent"))
					}
				}
			} else if msg.ToolName == "write_file" || msg.ToolName == "edit_file" || IsDiffContent(resultContent) {
				resultLines := renderCompactDiff(msg.ToolName, resultContent, theme)
				maxRes := terminalResultHeight(height)
				for i, rl := range resultLines {
					if i >= maxRes-1 {
						remaining := len(resultLines) - i
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(fmt.Sprintf("+%d more lines", remaining)))
						break
					}
					msgLines = append(msgLines, dim.Render("│ ")+rl)
				}
			} else {
				toolLineWidth := width - theme.Spacing.PadLR*2 - 6
				if toolLineWidth < 20 {
					toolLineWidth = 20
				}
				var wrappedLines []string
				for _, rl := range strings.Split(resultContent, "\n") {
					trimmed := strings.TrimLeft(rl, " ")
					for _, seg := range widget.HardWrap(trimmed, toolLineWidth) {
						wrappedLines = append(wrappedLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(seg))
					}
				}
				maxRes := terminalResultHeight(height)
				for i, wl := range wrappedLines {
					if i >= maxRes-1 {
						remaining := len(wrappedLines) - i
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(fmt.Sprintf("+%d more lines", remaining)))
						break
					}
					msgLines = append(msgLines, wl)
				}
			}
		}
	}

	return msgLines
}

func RenderErrorMessage(msg ChatMessage, theme *widget.Theme, contentWidth, innerW int, timeLine1, timeLine2 string, timeW int) []string {
	lines := widget.WrapText(msg.Content, contentWidth)
	dim := lipgloss.NewStyle().Foreground(theme.Colors.Muted)

	for i, line := range lines {
		styled := theme.Styles.ErrorText.Render("  " + line)
		if i == 0 {
			label := theme.Styles.ErrorText.Render("error")
			styled = label + " " + theme.Styles.ErrorText.Render(line)
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

func renderPlanBox(text string, maxWidth int, theme *widget.Theme) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}

	var styledLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			styledLines = append(styledLines, "")
			continue
		}

		var styled string
		switch {
		case strings.HasPrefix(trimmed, "✓"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Success).Render(line)
		case strings.HasPrefix(trimmed, "→"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Accent2).Render(line)
		case strings.HasPrefix(trimmed, "✗"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Error).Render(line)
		case strings.HasPrefix(trimmed, "!"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Warning).Render(line)
		case strings.HasPrefix(trimmed, "☐"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(line)
		case strings.HasPrefix(trimmed, "Plan:") || strings.HasPrefix(trimmed, "Goal:"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Accent).Bold(true).Render(line)
		case strings.HasPrefix(trimmed, "Suspended:"):
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Warning).Render(line)
		default:
			styled = lipgloss.NewStyle().Foreground(theme.Colors.Muted).Render(line)
		}
		styledLines = append(styledLines, styled)
	}

	content := strings.Join(styledLines, "\n")

	box := lipgloss.NewStyle().
		Padding(0, 1).
		Width(maxWidth)

	return box.Render(content)
}

func renderCompactDiff(toolName, content string, theme *widget.Theme) []string {
	dim := lipgloss.NewStyle().Foreground(theme.Colors.Muted)
	addStyle := lipgloss.NewStyle().Foreground(theme.Colors.Success)
	delStyle := lipgloss.NewStyle().Foreground(theme.Colors.Error)

	diffContent := extractDiffBlock(content)
	if diffContent == "" {
		firstLine := strings.SplitN(content, "\n", 2)[0]
		return []string{dim.Render("  " + firstLine)}
	}

	var addedCount, removedCount int
	var diffLines []string

	for _, line := range strings.Split(diffContent, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---"):
			continue
		case strings.HasPrefix(trimmed, "@@"):
			continue
		case strings.HasPrefix(trimmed, "+"):
			addedCount++
			diffLines = append(diffLines, addStyle.Render("  + "+trimmed[1:]))
		case strings.HasPrefix(trimmed, "-"):
			removedCount++
			diffLines = append(diffLines, delStyle.Render("  ─ "+trimmed[1:]))
		}
	}

	if len(diffLines) == 0 {
		firstLine := strings.SplitN(content, "\n", 2)[0]
		return []string{dim.Render("  " + firstLine)}
	}

	var result []string
	result = append(result, dim.Render(fmt.Sprintf("  -%d/+%d lines", removedCount, addedCount)))
	result = append(result, diffLines...)
	return result
}

func extractDiffBlock(content string) string {
	start := strings.Index(content, "```diff")
	if start < 0 {
		return ""
	}
	start += len("```diff\n")
	end := strings.Index(content[start:], "```")
	if end < 0 {
		return ""
	}
	return content[start : start+end]
}

func IsDiffContent(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "```diff") {
		return true
	}
	hasMinus := false
	hasPlus := false
	hasHunk := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "--- ") {
			hasMinus = true
		}
		if strings.HasPrefix(line, "+++ ") {
			hasPlus = true
		}
		if strings.HasPrefix(line, "@@") {
			hasHunk = true
		}
	}
	return (hasMinus && hasPlus) || hasHunk
}

func IsPlanTool(name string) bool {
	switch name {
	case "plan", "mark_step_complete", "mark_step_failed", "report_blocker", "resolve_blocker", "list_goal_status":
		return true
	default:
		return false
	}
}

func terminalResultHeight(height int) int {
	h := height / 3
	if h < 4 {
		h = 4
	}
	return h
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

func buildToolTitle(msg ChatMessage) string {
	target := msg.ToolProgressLabel
	if target == "" {
		target = msg.ToolArgs
	}

	switch msg.ToolPreviewPhase {
	case ttypes.ToolPreviewPreparing:
		return "Preparing " + pascalCaseTool(msg.ToolName)
	case ttypes.ToolPreviewStreaming:
		return buildStreamingLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens)
	case ttypes.ToolPreviewDone:
		dur := formatToolDuration(msg.ToolDuration)
		if msg.ToolStatus == "error" {
			label := pascalCaseTool(msg.ToolName)
			if target != "" {
				label += " " + target
			}
			label += " failed"
			if dur != "" {
				label += " " + dur
			}
			return label
		}
		return buildDoneLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens, dur)
	default:
		if msg.ToolStatus == "success" || msg.ToolStatus == "error" {
			dur := formatToolDuration(msg.ToolDuration)
			return buildDoneLabel(msg.ToolName, target, 0, 0, 0, dur)
		}
		if target != "" {
			return pascalCaseTool(msg.ToolName) + " " + target
		}
		return pascalCaseTool(msg.ToolName)
	}
}

func buildStreamingLabel(tool, target string, added, removed, tokens int) string {
	switch tool {
	case "write_file":
		if target != "" {
			return fmt.Sprintf("Writing %s +%d ↓%d", target, added, tokens)
		}
		return fmt.Sprintf("Writing ↓%d", tokens)
	case "edit_file":
		if target != "" {
			return fmt.Sprintf("Editing %s -%d +%d ↓%d", target, removed, added, tokens)
		}
		return fmt.Sprintf("Editing -%d +%d ↓%d", removed, added, tokens)
	case "read_file":
		return fmt.Sprintf("Reading %s", target)
	case "execute_command":
		if target != "" {
			return fmt.Sprintf("Running %s", truncateLine(target, 50))
		}
		return "Running command"
	case "delete_file":
		return fmt.Sprintf("Deleting %s", target)
	case "move_file":
		return fmt.Sprintf("Moving %s", target)
	case "copy_file":
		return fmt.Sprintf("Copying %s", target)
	case "web_search":
		if target != "" {
			return fmt.Sprintf("Searching \"%s\"", truncateLine(target, 40))
		}
		return "Searching web"
	case "web_fetch":
		if target != "" {
			return fmt.Sprintf("Fetching %s", truncateLine(target, 50))
		}
		return "Fetching URL"
	case "grep_files":
		if target != "" {
			return fmt.Sprintf("Grepping %s", truncateLine(target, 50))
		}
		return "Grepping files"
	case "glob_files":
		return fmt.Sprintf("Globbing %s", target)
	case "list_directory":
		return fmt.Sprintf("Listing %s", target)
	case "run_javascript":
		return "Running JavaScript"
	case "arxiv_search":
		if target != "" {
			return fmt.Sprintf("Searching ArXiv \"%s\"", truncateLine(target, 40))
		}
		return "Searching ArXiv"
	case "consult_oracle":
		return "Consulting oracle"
	case "plan", "list_goal_status":
		return fmt.Sprintf("Running %s", pascalCaseTool(tool))
	case "mark_step_complete":
		if target != "" {
			return fmt.Sprintf("Completing %s", target)
		}
		return "Completing step"
	case "mark_step_failed":
		if target != "" {
			return fmt.Sprintf("Failing %s", target)
		}
		return "Failing step"
	case "report_blocker":
		return "Reporting blocker"
	case "resolve_blocker":
		return "Resolving blocker"
	default:
		if target != "" {
			return fmt.Sprintf("Running %s %s", pascalCaseTool(tool), target)
		}
		return fmt.Sprintf("Running %s", pascalCaseTool(tool))
	}
}

func buildDoneLabel(tool, target string, added, removed, tokens int, duration string) string {
	switch tool {
	case "write_file":
		label := "Wrote"
		if target != "" {
			label += " " + target
		}
		label += fmt.Sprintf(" +%d", added)
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%d", tokens)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "edit_file":
		label := "Edited"
		if target != "" {
			label += " " + target
		}
		label += fmt.Sprintf(" -%d +%d", removed, added)
		if tokens > 0 {
			label += fmt.Sprintf(" ↓%d", tokens)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "read_file":
		return fmt.Sprintf("Read %s", target)
	case "execute_command":
		return fmt.Sprintf("Ran %s %s", truncateLine(target, 50), duration)
	case "delete_file":
		label := "Deleted " + target
		if duration != "" {
			label += " " + duration
		}
		return label
	case "move_file":
		label := "Moved " + target
		if duration != "" {
			label += " " + duration
		}
		return label
	case "copy_file":
		label := "Copied " + target
		if duration != "" {
			label += " " + duration
		}
		return label
	case "web_search":
		label := "Searched"
		if target != "" {
			label += fmt.Sprintf(" \"%s\"", truncateLine(target, 40))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "web_fetch":
		label := "Fetched"
		if target != "" {
			label += " " + truncateLine(target, 50)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "grep_files":
		label := "Grepped"
		if target != "" {
			label += " " + truncateLine(target, 50)
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "glob_files":
		label := "Globbed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "list_directory":
		label := "Listed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "run_javascript":
		label := "Ran JavaScript"
		if duration != "" {
			label += " " + duration
		}
		return label
	case "arxiv_search":
		label := "ArXiv"
		if target != "" {
			label += fmt.Sprintf(" \"%s\"", truncateLine(target, 40))
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "consult_oracle":
		label := "Consulted oracle"
		if duration != "" {
			label += " " + duration
		}
		return label
	case "plan", "list_goal_status":
		return pascalCaseTool(tool)
	case "mark_step_complete":
		label := "Completed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "mark_step_failed":
		label := "Failed"
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "report_blocker":
		label := "Blocked"
		if target != "" {
			label += ": " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "resolve_blocker":
		label := "Resolved blocker"
		if duration != "" {
			label += " " + duration
		}
		return label
	default:
		label := pascalCaseTool(tool)
		if target != "" {
			label += " " + target
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	}
}

func pascalCaseTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func formatToolDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	secs := d.Seconds()
	if secs < 1.0 {
		return fmt.Sprintf("%.1fs", secs)
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", int(secs))
	}
	mins := int(secs) / 60
	remainingSecs := int(secs) % 60
	return fmt.Sprintf("%dmin %dseconds", mins, remainingSecs)
}

func truncateLine(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}
