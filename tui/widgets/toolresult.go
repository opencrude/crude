package widgets

import (
	"fmt"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func RenderToolMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string) []string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	isActive := msg.ToolStatus == "streaming" || msg.ToolStatus == "calling"

	spinColor := th.Colors.Spinner.Default
	if isActive {
		switch phase {
		case "grounding":
			spinColor = th.Colors.Spinner.Grounding
		case "planning":
			spinColor = th.Colors.Spinner.Planning
		case "executing":
			spinColor = th.Colors.Spinner.Executing
		}
	} else if msg.ToolStatus == "success" {
		spinColor = th.Colors.Spinner.Done
	} else if msg.ToolStatus == "error" {
		spinColor = th.Colors.Spinner.Error
	}
	spinStyle := lipgloss.NewStyle().Foreground(spinColor)
	nameStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)
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
				planBox := renderPlanBox(resultContent, contentWidth-4, th)
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
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("(empty file)"))
				} else if strings.HasPrefix(resultContent, "Error:") {
					firstLine := strings.SplitN(resultContent, "\n", 2)[0]
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(firstLine))
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
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("Read %d line%s from %s", contentLines, suffix, path)))
					if strings.HasPrefix(resultContent, "Lines") {
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("… partial content returned to agent"))
					} else {
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("… full content returned to agent"))
					}
				}
		} else if msg.ToolName == "write_file" || msg.ToolName == "edit_file" || isDiffContent(resultContent) {
				diffBlock := extractDiffBlock(resultContent)
				if diffBlock != "" {
					diffLineCount := len(strings.Split(diffBlock, "\n"))
					maxRes := terminalResultHeight(height)
					if diffLineCount <= maxRes {
						renderedDiff := renderDiffContent(diffBlock, innerW-4, th)
						for _, dl := range strings.Split(renderedDiff, "\n") {
							msgLines = append(msgLines, dim.Render("│ ")+dl)
						}
					} else {
						resultLines := renderCompactDiff(msg.ToolName, resultContent, th)
						for i, rl := range resultLines {
							if i >= maxRes-1 {
								remaining := len(resultLines) - i
								msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("+%d more lines", remaining)))
								break
							}
							msgLines = append(msgLines, dim.Render("│ ")+rl)
						}
					}
				} else {
					resultLines := renderCompactDiff(msg.ToolName, resultContent, th)
					maxRes := terminalResultHeight(height)
					for i, rl := range resultLines {
						if i >= maxRes-1 {
							remaining := len(resultLines) - i
							msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("+%d more lines", remaining)))
							break
						}
						msgLines = append(msgLines, dim.Render("│ ")+rl)
					}
				}
			} else {
				toolLineWidth := width - th.Spacing.PadLR*2 - 6
				if toolLineWidth < 20 {
					toolLineWidth = 20
				}
				var wrappedLines []string
				for _, rl := range strings.Split(resultContent, "\n") {
					trimmed := strings.TrimLeft(rl, " ")
					for _, seg := range rendering.HardWrap(trimmed, toolLineWidth) {
						wrappedLines = append(wrappedLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(seg))
					}
				}
				maxRes := terminalResultHeight(height)
				for i, wl := range wrappedLines {
					if i >= maxRes-1 {
						remaining := len(wrappedLines) - i
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("+%d more lines", remaining)))
						break
					}
					msgLines = append(msgLines, wl)
				}
			}
		}
	}

	return msgLines
}

func renderPlanBox(text string, maxWidth int, th *theme.Theme) string {
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
			styled = lipgloss.NewStyle().Foreground(th.Colors.Success).Render(line)
		case strings.HasPrefix(trimmed, "→"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Accent2).Render(line)
		case strings.HasPrefix(trimmed, "✗"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Error).Render(line)
		case strings.HasPrefix(trimmed, "!"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Warning).Render(line)
		case strings.HasPrefix(trimmed, "☐"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(line)
		case strings.HasPrefix(trimmed, "Plan:") || strings.HasPrefix(trimmed, "Goal:"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true).Render(line)
		case strings.HasPrefix(trimmed, "Suspended:"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Warning).Render(line)
		default:
			styled = lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(line)
		}
		styledLines = append(styledLines, styled)
	}

	content := strings.Join(styledLines, "\n")

	box := lipgloss.NewStyle().
		Padding(0, 1).
		Width(maxWidth)

	return box.Render(content)
}

func renderCompactDiff(toolName, content string, th *theme.Theme) []string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	addStyle := lipgloss.NewStyle().Foreground(th.Colors.Success)
	delStyle := lipgloss.NewStyle().Foreground(th.Colors.Error)

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

func buildToolTitle(msg types.ChatMessage) string {
	target := msg.ToolProgressLabel
	if target == "" {
		target = msg.ToolArgs
	}

	switch msg.ToolPreviewPhase {
	case types.ToolPreviewPreparing:
		return "Preparing " + pascalCaseTool(msg.ToolName)
	case types.ToolPreviewStreaming:
		return buildStreamingLabel(msg.ToolName, target, msg.ToolLivedAdded, msg.ToolLivedRemoved, msg.ToolEstTokens)
	case types.ToolPreviewDone:
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

func isDiffContent(content string) bool {
	text := strings.TrimSpace(content)
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

func renderDiffContent(text string, maxWidth int, th *theme.Theme) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "```diff" || trimmed == "```" {
			continue
		}
		lines = append(lines, line)
	}

	fileStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.File).
		Background(lipgloss.Color("#1e2235"))
	hunkStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Hunk).
		Background(lipgloss.Color("#0d1117"))
	ctxStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Context).
		Background(lipgloss.Color("#0d1117"))
	addStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Add).
		Background(lipgloss.Color("#1c2a20"))
	delStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Delete).
		Background(lipgloss.Color("#2a1d1e"))

	var sb strings.Builder
	for _, line := range lines {
		if runewidth.StringWidth(line) <= maxWidth {
			renderDiffLine(&sb, line, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle)
			sb.WriteByte('\n')
			continue
		}

		var prefix string
		content := line
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			prefix = line[:4]
			content = line[4:]
		case len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' '):
			prefix = line[:1]
			content = line[1:]
		}

		pw := runewidth.StringWidth(prefix)
		wrapW := maxWidth - pw
		if wrapW < 4 {
			wrapW = 4
		}
		segs := rendering.HardWrap(strings.TrimLeft(content, " "), wrapW)
		for i, seg := range segs {
			if i == 0 {
				renderDiffLine(&sb, prefix+seg, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle)
			} else {
				renderDiffLine(&sb, strings.Repeat(" ", pw)+seg, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle)
			}
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderDiffLine(sb *strings.Builder, line string, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle lipgloss.Style) {
	switch {
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		sb.WriteString(fileStyle.Render(line))
	case strings.HasPrefix(line, "@@"):
		sb.WriteString(hunkStyle.Render(line))
	case strings.HasPrefix(line, "+"):
		sb.WriteString(addStyle.Render(line))
	case strings.HasPrefix(line, "-"):
		sb.WriteString(delStyle.Render(line))
	default:
		sb.WriteString(ctxStyle.Render(line))
	}
}
