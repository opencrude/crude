package tools

import (
	"crude/tui/rendering"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// isSummaryOnlyTool returns true for tools whose result is fully expressed
// in the title-line summary — no content lines are rendered below.
func isSummaryOnlyTool(name string) bool {
	switch name {
	case "read_file", "grep_files", "list_directory", "glob_files",
		"web_search", "arxiv_search", "wait", "kill_async_command",
		"delete_file", "move_file", "copy_file",
		"run_sub_agent", "poll_sub_agent", "cancel_sub_agent",
		"list_sub_agents", "write_report",
		"remember", "update_memory", "forget_memory":
		return true
	}
	return false
}

// IsSummaryOnlyTool reports whether tool output is fully captured in the title line.
func IsSummaryOnlyTool(name string) bool {
	return isSummaryOnlyTool(name)
}

// ResultSummary returns a compact title-line summary for successful tool output.
func ResultSummary(tool, content, target, dur string) string {
	return resultSummary(tool, content, target, dur)
}

// truncateLineToWidth truncates a line to fit within maxDisplayWidth using
// rune-width-aware measurement (handles CJK wide chars etc.).
func truncateLineToWidth(line string, maxDisplayWidth int) string {
	if maxDisplayWidth <= 0 {
		return line
	}
	if rendering.VisibleWidth(line) <= maxDisplayWidth {
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

//                    ─
// Result summary functions — all use " — " as the consistent
// separator between the action+target and the result gist.
// ─

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
	case "remember", "update_memory", "forget_memory", "recall_memory", "search_history":
		return MemoryResultSummary(tool, content)
	default:
		return ""
	}
}

// ── read_file ─

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

// ── grep_files ─

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

// ── list_directory

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

// ── glob_files ─────────────────────────────────────

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
	_ = json.Unmarshal([]byte(content), &result)
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
