package tools

import (
	"crude/tui/rendering"
	"fmt"
	"strings"
	"unicode/utf8"
)

// isSummaryOnlyTool returns true for tools whose result is fully expressed
// in the title-line summary, and no content lines are rendered below. Search and
// listing tools (grep_files, glob_files, list_directory, web_search,
// arxiv_search) are deliberately NOT summary-only: their actual matches are
// the content the user wants to read, so they render in the body.
func isSummaryOnlyTool(name string) bool {
	switch name {
	case "read_file", "wait", "kill_async_command",
		"delete_file", "move_file", "copy_file":
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

// TerminalResultHeight caps how many result lines a tool message may occupy in
// the chat viewport. The cap is relative to the chat area height (the visible
// scroll window), not the whole terminal, so a tall chat shows substantially
// more of a command/search result before the "+N more lines" truncation.
func TerminalResultHeight(height int) int {
	// Show up to two-thirds of the visible chat area: a grep or command output
	// is content the user wants to read, not a footer. Floor of 6 keeps small
	// terminals usable.
	h := height * 2 / 3
	if h < 6 {
		h = 6
	}
	return h
}

//                    ─
// Result summary functions all use the same separator, a dash, between the
// action+target and the result gist.
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
	case "web_fetch":
		return webFetchResultSummary(content, target, dur)
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
	default:
		return ""
	}
}

// ── read_file ─

func readResultSummary(content, path, duration string) string {
	if path == "" {
		path = "file"
	}
	return (ToolTitle{Action: "Read", Operand: path, Metadata: strings.TrimSpace(readResultMetadata(content) + " " + duration)}).Text()
}

func readResultMetadata(content string) string {
	if strings.Contains(content, "— (empty file)") {
		return "— empty"
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
				return fmt.Sprintf("— L%s (of %s)", rangeStr, totalStr)
			}
		}
	}

	// Full read: count lines in the returned content
	contentLines := strings.Count(content, "\n") + 1
	return fmt.Sprintf("— %s lines", rendering.FormatCount(contentLines))
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

	// "N match(es) for ...": extract N from the first line
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

	// The header is the path, a dash, then "N entries:" (or "(recursive):").
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

	// "Web search results for: query": count numbered results (lines starting with "N. ")
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
