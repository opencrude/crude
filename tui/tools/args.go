package tools

import (
	"crude/tui/rendering"
	"encoding/json"
	"fmt"
	"strings"
)

func SummarizeToolArgs(tool, argsStr string) string {
	if tool == "update_plan" || tool == "get_plan" {
		return ExtractArg(argsStr, "plan_id")
	}
	// grep_files: show what (pattern) and where (path)
	if tool == "grep_files" {
		pattern := ExtractArg(argsStr, "pattern")
		path := ExtractArg(argsStr, "path")
		label := ""
		if pattern != "" {
			label = fmt.Sprintf("\"%s\"", rendering.TruncateLine(pattern, 30))
		}
		if path != "" && path != "." {
			if label != "" {
				label += " in " + path
			} else {
				label = path
			}
		}
		if label != "" {
			return label
		}
	}

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
	if code := ExtractArg(argsStr, "code"); code != "" {
		return rendering.TruncateLine(code, 50)
	}
	if tool == "async_execute_command" {
		handle := ExtractArg(argsStr, "handle")
		if command := ExtractArg(argsStr, "command"); command != "" {
			if handle != "" {
				return rendering.TruncateLine(command, 40) + " [" + handle + "]"
			}
			return rendering.TruncateLine(command, 50)
		}
		if handle != "" {
			return handle
		}
	}
	if tool == "check_async_command" || tool == "kill_async_command" || tool == "read_async_output" {
		if handle := ExtractArg(argsStr, "handle"); handle != "" {
			return handle
		}
	}
	if tool == "wait" {
		if seconds := ExtractArg(argsStr, "seconds"); seconds != "" {
			return seconds + "s"
		}
	}
	return rendering.TruncateLine(argsStr, 60)
}

func ExtractArg(str, key string) string {
	var object map[string]any
	if json.Unmarshal([]byte(str), &object) == nil {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			case float64, bool:
				return fmt.Sprint(typed)
			}
		}
	}
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
