package tools

import (
	"crude/tui/rendering"
	"fmt"
	"strings"
)

func SummarizeToolArgs(tool, argsStr string) string {
	path := ExtractArg(argsStr, "path")
	if path != "" {
		return path
	}
	if pattern := ExtractArg(argsStr, "pattern"); pattern != "" {
		return pattern
	}
	if command := ExtractArg(argsStr, "command"); command != "" {
		return rendering.TruncateLine(command, 50)
	}
	if content := ExtractArg(argsStr, "content"); content != "" {
		return rendering.TruncateLine(content, 50) + " (content)"
	}
	if query := ExtractArg(argsStr, "query"); query != "" {
		return rendering.TruncateLine(query, 50)
	}
	if url := ExtractArg(argsStr, "url"); url != "" {
		return rendering.TruncateLine(url, 50)
	}
	if source := ExtractArg(argsStr, "source"); source != "" {
		dest := ExtractArg(argsStr, "destination")
		if dest != "" {
			return fmt.Sprintf("%s → %s", source, dest)
		}
		return source
	}
	if tool == "plan" || tool == "list_goal_status" {
		return rendering.PascalCaseTool(tool)
	}
	if tool == "mark_step_complete" || tool == "mark_step_failed" {
		if stepID := ExtractArg(argsStr, "step_id"); stepID != "" {
			return stepID
		}
		return rendering.PascalCaseTool(tool)
	}
	if tool == "report_blocker" {
		if msg := ExtractArg(argsStr, "message"); msg != "" {
			return rendering.TruncateLine(msg, 50)
		}
		return rendering.PascalCaseTool(tool)
	}
	if tool == "resolve_blocker" {
		return rendering.PascalCaseTool(tool)
	}
	if code := ExtractArg(argsStr, "code"); code != "" {
		return rendering.TruncateLine(code, 50)
	}
	return rendering.TruncateLine(argsStr, 60)
}

func ExtractArg(str, key string) string {
	prefixes := []string{key + ":", key + ": "}
	for _, prefix := range prefixes {
		idx := strings.Index(str, prefix)
		if idx == -1 {
			continue
		}
		start := idx + len(prefix)
		if start >= len(str) {
			return ""
		}
		end := strings.IndexAny(str[start:], " ,}]")
		if end == -1 {
			return strings.TrimSpace(str[start:])
		}
		return strings.TrimSpace(str[start : start+end])
	}
	return ""
}
