package tools

import (
	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// isSummaryOnlyTool returns true for tools whose result is fully expressed
// in the title-line summary — no content lines are rendered below.
func isSummaryOnlyTool(name string) bool {
	switch name {
	case "read_file", "grep_files", "list_directory", "glob_files",
		"web_search", "arxiv_search", "wait", "kill_async_command",
		"delete_file", "move_file", "copy_file",
		"run_sub_agent", "poll_sub_agent", "cancel_sub_agent",
		"list_sub_agents", "write_report":
		return true
	}
	return false
}

func RenderToolMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string) []string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	isActive := (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") && msg.ToolPreviewPhase != types.ToolPreviewDone && msg.ToolPreviewPhase != types.ToolPreviewError

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
	} else if msg.ToolPreviewPhase == types.ToolPreviewDone {
		if msg.ToolStatus == types.ToolStatusCancelled {
			spinColor = th.Colors.Warning
		} else {
			spinColor = th.Colors.Spinner.Done
		}
	} else if msg.ToolPreviewPhase == types.ToolPreviewError || msg.ToolStatus == "error" {
		spinColor = th.Colors.Spinner.Error
	} else if msg.ToolStatus == "cancelled" {
		spinColor = th.Colors.Warning
	}
	spinStyle := lipgloss.NewStyle().Foreground(spinColor)
	nameStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	titlePhase := msg.ToolPreviewPhase
	if titlePhase == "" {
		titlePhase = phase
	}
	title := BuildToolTitle(msg, titlePhase)

	// Override title with content-derived summary on success
	if msg.ToolStatus == "success" {
		dur := rendering.FormatDuration(msg.ToolDuration)
		target := msg.ToolArgs
		if target == "" {
			target = msg.ToolProgressLabel
		}
		if summary := resultSummary(msg.ToolName, msg.Content, target, dur); summary != "" {
			title = summary
		}
	}

	// Build the permission tag suffix for the title line
	permSuffix := formatPermTag(msg.PermTag, th)

	var msgLines []string

	if isActive {
		var dot string
		if components.BlinkOn(spinFrame) {
			dot = "● "
		} else {
			dot = "○ "
		}
		line := dim.Render("│ ") + spinStyle.Render(dot) + nameStyle.Render(title) + permSuffix
		msgLines = append(msgLines, line)
	} else if msg.ToolPreviewPhase == types.ToolPreviewDone {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title) + permSuffix
		msgLines = append(msgLines, line)
	} else if msg.ToolPreviewPhase == types.ToolPreviewError {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title) + permSuffix
		msgLines = append(msgLines, line)
	} else if msg.ToolStatus == "success" && msg.ToolName == "plan" {
	} else if msg.ToolStatus == "success" {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title) + permSuffix
		msgLines = append(msgLines, line)
	} else if msg.ToolStatus == "error" {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title) + permSuffix
		msgLines = append(msgLines, line)
	} else {
		line := dim.Render("│ ") + spinStyle.Render("○ ") + nameStyle.Render(title) + permSuffix
		msgLines = append(msgLines, line)
	}

	if msg.ToolStatus == "success" && msg.Content != "" {
		resultContent := msg.Content
		fullLabel := rendering.PascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
		resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
		resultContent = strings.TrimSpace(resultContent)
		if resultContent != "" {
			if IsPlanTool(msg.ToolName) {
				planBox := RenderPlanBox(resultContent, contentWidth-4, th)
				for _, pl := range strings.Split(planBox, "\n") {
					msgLines = append(msgLines, dim.Render("│ ")+pl)
				}
			} else if isSummaryOnlyTool(msg.ToolName) {
				// Summary already embedded in the title line; no content lines needed
			} else if isDiffTool(msg.ToolName) || IsDiffContent(resultContent) {
				// Use the no-header variant: the title line already shows
				// the file path and change counts (plus tokens & duration).
				maxRes := TerminalResultHeight(height)
				diffWidth := innerW - 4
				if diffWidth < 20 {
					diffWidth = 20
				}
				renderedDiff := RenderDiffNoHeader(resultContent, maxRes, diffWidth, th)
				if renderedDiff != "" {
					for _, dl := range strings.Split(renderedDiff, "\n") {
						msgLines = append(msgLines, dim.Render("│ ")+dl)
					}
				} else {
					// Fallback for non-diff content
					firstLine := strings.SplitN(resultContent, "\n", 2)[0]
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(firstLine))
				}
			} else {
			toolLineWidth := width - th.Spacing.PadLR*2 - 6
			if toolLineWidth < 20 {
				toolLineWidth = 20
			}
			var wrappedLines []string
			for _, rl := range strings.Split(resultContent, "\n") {
				// Trim all whitespace including tabs, not just spaces
				trimmed := strings.TrimSpace(rl)
				if trimmed == "" {
					// Preserve empty lines to maintain output structure
					// (paragraph breaks between sections, etc.)
					wrappedLines = append(wrappedLines, dim.Render("│   "))
					continue
				}
				// Sanitize the content to prevent TUI breakage
				sanitized := rendering.SanitizeForTerminal(trimmed)
				for _, seg := range rendering.HardWrap(sanitized, toolLineWidth) {
					if seg != "" {
						wrappedLines = append(wrappedLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(seg))
					}
				}
			}
			maxRes := TerminalResultHeight(height)
			for i, wl := range wrappedLines {
				if i >= maxRes-1 {
					remaining := len(wrappedLines) - i
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("+%s more lines", rendering.FormatCount(remaining))))
					break
				}
				msgLines = append(msgLines, wl)
			}
			}
		}
	} else if msg.ToolStatus == "error" || msg.ToolStatus == "cancelled" {
		if msg.Content != "" {
			maxRes := TerminalResultHeight(height)
			toolLineWidth := width - th.Spacing.PadLR*2 - 6
			if toolLineWidth < 20 {
				toolLineWidth = 20
			}
			var wrappedLines []string
			for _, rl := range strings.Split(msg.Content, "\n") {
				trimmed := strings.TrimSpace(rl)
				if trimmed == "" {
					wrappedLines = append(wrappedLines, dim.Render("│   "))
					continue
				}
				sanitized := rendering.SanitizeForTerminal(trimmed)
				for _, seg := range rendering.HardWrap(sanitized, toolLineWidth) {
					if seg != "" {
						wrappedLines = append(wrappedLines, dim.Render("│   ")+th.Styles.ErrorText.Render(seg))
					}
				}
			}
			for i, wl := range wrappedLines {
				if i >= maxRes-1 {
					remaining := len(wrappedLines) - i
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("+%s more lines", rendering.FormatCount(remaining))))
					break
				}
				msgLines = append(msgLines, wl)
			}
		}
	}

	return msgLines
}

// isDiffTool returns true for tool names that produce diff-like output.
func isDiffTool(name string) bool {
	switch name {
	case "write_file", "edit_file":
		return true
	}
	return false
}

func RenderPlanBox(text string, maxWidth int, th *theme.Theme) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}

	// The box has Padding(0,1) so content area is maxWidth-2 characters.
	// Truncate long lines (especially step descriptions) to fit.
	contentWidth := maxWidth - 2
	if contentWidth < 20 {
		contentWidth = 20
	}

	var styledLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			styledLines = append(styledLines, "")
			continue
		}

		// Truncate the line to fit within contentWidth if needed.
		// Step lines have format: "  [marker] [id]: [description]"
		// We truncate the whole line rather than trying to split it.
		line = truncateLineToWidth(line, contentWidth)

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

// truncateLineToWidth truncates a line to fit within maxDisplayWidth using
// rune-width-aware measurement (handles CJK wide chars etc.).
func truncateLineToWidth(line string, maxDisplayWidth int) string {
	if maxDisplayWidth <= 0 {
		return line
	}
	if rendering.StringWidth(line) <= maxDisplayWidth {
		return line
	}
	return rendering.TruncateVisual(line, maxDisplayWidth, "…")
}

func TerminalResultHeight(height int) int {
	h := height / 3
	if h < 4 {
		h = 4
	}
	return h
}

// ─────────────────────────────────────────────────────────────
// Result summary functions — all use " — " as the consistent
// separator between the action+target and the result gist.
// ─────────────────────────────────────────────────────────────

// resultSummary dispatches to the appropriate tool-specific
// summary function. Returns "" when no summary is available
// (letting the default title handle display).
func resultSummary(tool, content, target, dur string) string {
	if strings.HasPrefix(strings.TrimSpace(content), "Error:") {
		summary := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "Error:"))
		if len(summary) > 60 {
			summary = summary[:59] + "…"
		}
		return "Error: " + summary
	}

	switch tool {
	case "read_file":
		return readResultSummary(content, target, dur)
	case "grep_files":
		return grepResultSummary(content, target, dur)
	case "list_directory":
		return listDirResultSummary(content, target, dur)
	case "glob_files":
		return globResultSummary(content, target, dur)
	case "web_search":
		return webSearchResultSummary(content, target, dur)
	case "arxiv_search":
		return arxivResultSummary(content, target, dur)
	case "execute_command":
		return execResultSummary(content, target, dur)
	case "web_fetch":
		return webFetchResultSummary(content, target, dur)
	case "run_javascript":
		return jsResultSummary(content, target, dur)
	case "async_execute_command":
		return asyncExecResultSummary(content, target, dur)
	case "check_async_command":
		return checkAsyncResultSummary(content, target, dur)
	case "read_async_output":
		return readAsyncResultSummary(content, target, dur)
	case "kill_async_command":
		return killAsyncResultSummary(content, target, dur)
	case "wait":
		return waitResultSummary(content, target, dur)
	case "delete_file":
		return deleteResultSummary(content, target, dur)
	case "move_file":
		return moveResultSummary(content, target, dur)
	case "copy_file":
		return copyResultSummary(content, target, dur)
	case "run_sub_agent":
		return subAgentRunSummary(content, target, dur)
	case "poll_sub_agent":
		return subAgentPollSummary(content, target, dur)
	case "cancel_sub_agent":
		return subAgentCancelSummary(content, target, dur)
	case "list_sub_agents":
		return subAgentListSummary(content, target, dur)
	case "write_report":
		return subAgentWriteReportSummary(content, target, dur)
	default:
		return ""
	}
}

// ── read_file ──────────────────────────────────────────────

func readResultSummary(content, path, duration string) string {
	if path == "" {
		path = "file"
	}

	if strings.Contains(content, "— (empty file)") {
		label := "Read " + path + " — empty"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	// Partial read: first line is "Lines X–Y of path (N total):"
	if strings.HasPrefix(content, "Lines ") {
		firstNewline := strings.Index(content, "\n")
		firstLine := content
		if firstNewline > 0 {
			firstLine = content[:firstNewline]
		}
		firstLine = strings.TrimSuffix(firstLine, ":")
		afterLines := strings.TrimPrefix(firstLine, "Lines ")
		ofIdx := strings.Index(afterLines, " of ")
		if ofIdx > 0 {
			rangeStr := afterLines[:ofIdx]
			rest := afterLines[ofIdx+4:]
			openParen := strings.LastIndex(rest, "(")
			closeParen := strings.LastIndex(rest, ")")
			if openParen > 0 && closeParen > openParen {
				totalStr := rest[openParen+1 : closeParen]
				totalStr = strings.TrimSuffix(totalStr, " total")
				label := fmt.Sprintf("Read %s — L%s (of %s)", path, rangeStr, totalStr)
				if duration != "" {
					label += " " + duration
				}
				return label
			}
		}
	}

	// Full read: count lines in the returned content
	contentLines := len(strings.Split(content, "\n"))
	label := fmt.Sprintf("Read %s — %s lines", path, rendering.FormatCount(contentLines))
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── grep_files ──────────────────────────────────────────────

func grepResultSummary(content, target, duration string) string {
	// "No matches for ..."
	if strings.HasPrefix(content, "No matches") {
		label := "Grepped"
		if target != "" {
			label += " " + rendering.TruncateLine(target, 50)
		}
		label += " — 0 matches"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	// "N match(es) for ..." — extract N from first line
	firstNewline := strings.Index(content, "\n")
	firstLine := content
	if firstNewline > 0 {
		firstLine = content[:firstNewline]
	}
	spaceIdx := strings.Index(firstLine, " ")
	if spaceIdx > 0 {
		count := firstLine[:spaceIdx]
		label := "Grepped"
		if target != "" {
			label += " " + rendering.TruncateLine(target, 50)
		}
		label += " — " + count + " matches"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	return "" // fallback to default
}

// ── list_directory ──────────────────────────────────────────

func listDirResultSummary(content, target, duration string) string {
	if strings.Contains(content, "is empty") {
		label := "Listed " + target + " — empty"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	// "path — N entries:" or "path — N entries (recursive):"
	if idx := strings.Index(content, " — "); idx > 0 {
		afterDash := content[idx+len(" — "):]
		colonIdx := strings.Index(afterDash, ":")
		if colonIdx > 0 {
			info := strings.TrimSpace(afterDash[:colonIdx])
			label := "Listed " + target + " — " + info
			if duration != "" {
				label += " " + duration
			}
			return label
		}
	}

	// Count non-empty lines as a fallback
	lines := strings.Count(content, "\n") + 1
	label := fmt.Sprintf("Listed %s — %s entries", target, rendering.FormatCount(lines))
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── glob_files ──────────────────────────────────────────────

func globResultSummary(content, target, duration string) string {
	if strings.HasPrefix(content, "No files matching") {
		label := "Globbed " + rendering.TruncateLine(target, 50) + " — 0 matches"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	// "N file(s) matching ..."
	spaceIdx := strings.Index(content, " ")
	if spaceIdx > 0 {
		count := content[:spaceIdx]
		label := "Globbed " + rendering.TruncateLine(target, 50) + " — " + count + " files"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	return ""
}

// ── web_search ──────────────────────────────────────────────

func webSearchResultSummary(content, target, duration string) string {
	if strings.HasPrefix(content, "No results found") {
		label := "Searched"
		if target != "" {
			label += fmt.Sprintf(" \"%s\"", rendering.TruncateLine(target, 40))
		}
		label += " — 0 results"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	if strings.HasPrefix(content, "Search error") {
		return "" // let default handle error
	}

	// "Web search results for: query" — count numbered results (lines starting with "N. ")
	count := strings.Count(content, "\n") + 1
	// Count numbered entries like "1. ", "2. ", etc.
	resultCount := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 3 && trimmed[0] >= '0' && trimmed[0] <= '9' && trimmed[1] == '.' && trimmed[2] == ' ' {
			resultCount++
		}
	}
	label := "Searched"
	if target != "" {
		label += fmt.Sprintf(" \"%s\"", rendering.TruncateLine(target, 40))
	}
	if resultCount > 0 {
		label += fmt.Sprintf(" — %s results", rendering.FormatCount(resultCount))
	} else {
		label += fmt.Sprintf(" — %s lines", rendering.FormatCount(count))
	}
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── arxiv_search ────────────────────────────────────────────

func arxivResultSummary(content, target, duration string) string {
	if strings.HasPrefix(content, "ArXiv search error") {
		return "" // let default handle error
	}

	if strings.Contains(content, "No papers found") || strings.Contains(content, "no results") {
		label := "ArXiv"
		if target != "" {
			label += fmt.Sprintf(" \"%s\"", rendering.TruncateLine(target, 40))
		}
		label += " — 0 papers"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	// Count paper entries (lines starting with "Title: " or numbered entries)
	paperCount := strings.Count(content, "Title: ")
	label := "ArXiv"
	if target != "" {
		label += fmt.Sprintf(" \"%s\"", rendering.TruncateLine(target, 40))
	}
	if paperCount > 0 {
		label += fmt.Sprintf(" — %d papers", paperCount)
	} else {
		lines := strings.Count(content, "\n") + 1
		label += fmt.Sprintf(" — %s lines", rendering.FormatCount(lines))
	}
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── execute_command ─────────────────────────────────────────

func execResultSummary(content, target, duration string) string {
	if strings.HasPrefix(content, "Command failed") {
		return "" // let default handle error
	}

	lineCount := strings.Count(content, "\n") + 1
	label := "Ran " + rendering.TruncateLine(target, 50)
	if content == "(command completed with no output)" {
		label += " — no output"
	} else {
		label += fmt.Sprintf(" — %s lines", rendering.FormatCount(lineCount))
	}
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── web_fetch ───────────────────────────────────────────────

func webFetchResultSummary(content, target, duration string) string {
	if strings.HasPrefix(content, "Fetch error") || strings.HasPrefix(content, "fetch:") {
		return "" // let default handle error
	}

	if strings.HasPrefix(content, "No content could be extracted") {
		label := "Fetched " + rendering.TruncateLine(target, 50) + " — no content"
		if duration != "" {
			label += " " + duration
		}
		return label
	}

	charCount := utf8.RuneCountInString(content)
	label := "Fetched " + rendering.TruncateLine(target, 50)
	label += fmt.Sprintf(" — %s chars", rendering.FormatCount(charCount))
	if duration != "" {
		label += " " + duration
	}
	return label
}


// ── run_javascript ──────────────────────────────────────────

func jsResultSummary(content, target, duration string) string {
	if strings.HasPrefix(content, "JavaScript error") {
		return "" // let default handle error
	}

	firstLine := strings.SplitN(content, "\n", 2)[0]
	label := "Ran JS"
	if firstLine != "" {
		label += " — " + rendering.TruncateLine(firstLine, 40)
	}
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── async_execute_command ──────────────────────────────────

func asyncExecResultSummary(content, target, duration string) string {
	label := "Started " + rendering.TruncateLine(target, 50)
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── check_async_command ─────────────────────────────────────

func checkAsyncResultSummary(content, target, duration string) string {
	// Content looks like: "Handle: X\nPID: Y\nStatus: running/completed/failed/killed\n..."
	status := "unknown"
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Status:") {
			status = strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
			break
		}
	}
	label := "Checked " + target + " — " + status
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── read_async_output ───────────────────────────────────────

func readAsyncResultSummary(content, target, duration string) string {
	label := "Read output " + target
	lineCount := strings.Count(content, "\n") + 1
	if content != "" {
		label += fmt.Sprintf(" — %s lines", rendering.FormatCount(lineCount))
	}
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── kill_async_command ──────────────────────────────────────

func killAsyncResultSummary(content, target, duration string) string {
	label := "Killed " + target
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── wait ────────────────────────────────────────────────────

func waitResultSummary(content, target, duration string) string {
	label := "Waited " + target
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── delete_file ─────────────────────────────────────────────

func deleteResultSummary(content, target, duration string) string {
	label := "Deleted " + target
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── move_file ───────────────────────────────────────────────

func moveResultSummary(content, target, duration string) string {
	label := "Moved " + target
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── copy_file ───────────────────────────────────────────────

func copyResultSummary(content, target, duration string) string {
	label := "Copied " + target
	if duration != "" {
		label += " " + duration
	}
	return label
}

// ── sub-agent tools ──────────────────────────────────────────

func parseJSONMap(content string) map[string]string {
	result := make(map[string]string)
	json.Unmarshal([]byte(content), &result)
	return result
}

func subAgentRunSummary(content, target, duration string) string {
	m := parseJSONMap(content)
	name := m["name"]
	cat := m["category"]
	status := m["status"]
	handle := m["handle_id"]

	if status == "running" {
		parts := []string{"Spawned"}
		if cat != "" {
			parts = append(parts, cat)
		}
		if name != "" {
			parts = append(parts, "\""+name+"\"")
		}
		label := strings.Join(parts, " ")
		if handle != "" && len(handle) > 8 {
			label += " " + handle[:8]
		}
		return label + " (async)"
	}

	parts := []string{"Sub-agent"}
	if name != "" {
		parts = append(parts, "\""+name+"\"")
	}
	if cat != "" {
		parts = append(parts, "("+cat+")")
	}
	label := strings.Join(parts, " ")
	if duration != "" {
		label += " " + duration
	}
	return label
}

func subAgentPollSummary(content, target, duration string) string {
	m := parseJSONMap(content)
	status := m["status"]
	name := m["name"]
	cat := m["category"]
	elapsed := m["elapsed_seconds"]
	steps := m["steps_used"]
	maxSteps := m["max_steps"]
	resultPath := m["result_path"]

	switch status {
	case "running":
		parts := []string{"Running"}
		if name != "" {
			parts = append(parts, "\""+name+"\"")
		}
		if cat != "" {
			parts = append(parts, "("+cat+")")
		}
		label := strings.Join(parts, " ")
		if elapsed != "" {
			label += " " + elapsed + "s"
		}
		if steps != "" {
			label += " " + steps
			if maxSteps != "" {
				label += "/" + maxSteps
			}
			label += " steps"
		}
		return label
	case "completed":
		parts := []string{"Completed"}
		if name != "" {
			parts = append(parts, "\""+name+"\"")
		}
		label := strings.Join(parts, " ")
		if resultPath != "" {
			label += " — " + resultPath
		}
		if duration != "" {
			label += " " + duration
		}
		return label
	case "not_found":
		return "Agent not found"
	default:
		if strings.HasPrefix(status, "failed") {
			parts := []string{"Failed"}
			if name != "" {
				parts = append(parts, "\""+name+"\"")
			}
			label := strings.Join(parts, " ") + " (" + status + ")"
			if duration != "" {
				label += " " + duration
			}
			return label
		}
		if status == "cancelled" {
			label := "Cancelled"
			if name != "" {
				label += " " + name
			}
			return label
		}
	}
	return "Poll " + name + " " + status
}

func subAgentCancelSummary(content, target, duration string) string {
	m := parseJSONMap(content)
	status := m["status"]
	name := m["name"]

	if status == "not_found" {
		return "Agent not found"
	}
	if name != "" {
		return "Cancelled \"" + name + "\""
	}
	return "Cancelled agent"
}

func subAgentListSummary(content, target, duration string) string {
	var entries []map[string]any
	if err := json.Unmarshal([]byte(content), &entries); err != nil {
		return "Listed sub-agents"
	}
	active := 0
	completed := 0
	for _, e := range entries {
		switch s, _ := e["status"].(string); s {
		case "running":
			active++
		default:
			completed++
		}
	}
	if active == 0 && completed == 0 {
		return "No sub-agents"
	}
	parts := []string{}
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	}
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("%d done", completed))
	}
	return strings.Join(parts, ", ")
}

func subAgentWriteReportSummary(content, target, duration string) string {
	m := parseJSONMap(content)
	path := m["report_path"]
	if path == "" {
		path = target
	}
	if path != "" {
		label := "Wrote " + path
		if duration != "" {
			label += " " + duration
		}
		return label
	}
	if m["status"] == "report_written" {
		label := "Wrote report"
		if duration != "" {
			label += " " + duration
		}
		return label
	}
	return "Write report"
}

// ── helpers ─────────────────────────────────────────────────

func formatPermTag(tag string, th *theme.Theme) string {
	if tag == "" {
		return ""
	}
	muted := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	switch tag {
	case "allowed":
		return muted.Render("  ✓")
	case "always":
		return muted.Render("  ✓ always")
	case "auto":
		return muted.Render("  ✓ auto")
	case "denied":
		return muted.Render("  ✗")
	default:
		return muted.Render("  " + tag)
	}
}
