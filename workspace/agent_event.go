package workspace

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// AgentEvent is the workspace-owned domain event produced by the backend
// orchestrator. The TUI converts ttypes.AgentEventMsg to this type at the
// boundary so workspace code never imports tui/types.
type AgentEvent struct {
	Type    string
	Data    any
	AgentID string
}

// SanitizeForTerminal removes control characters and invalid UTF-8 from
// agent text before it enters conversation data. This is a domain concern
// (data hygiene), not a rendering concern — the workspace stores clean text
// and the TUI projects it.
func SanitizeForTerminal(s string) string {
	if s == "" {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := decodeRune(s[i:])
		if r == 0xFFFD && size == 1 {
			out = append(out, '?')
			i++
			continue
		}
		switch {
		case r == '\n' || r == '\t':
			out = append(out, s[i:i+size]...)
		case isControl(r):
			out = append(out, ' ')
		default:
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	// Strip carriage returns.
	result := make([]byte, 0, len(out))
	for _, b := range out {
		if b != '\r' {
			result = append(result, b)
		}
	}
	return string(result)
}

// TruncateLine truncates a string to max visible cells, appending an
// ellipsis. This is domain data normalization (keeping stored labels short),
// not terminal rendering.
func TruncateLine(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// ToolLabel builds a compact "tool args" status label. This is domain
// data formatting, not terminal rendering.
func ToolLabel(tool, argsSummary string) string {
	label := PascalCaseTool(tool)
	switch tool {
	case "write_file":
		label = "Writing"
	case "edit_file":
		label = "Editing"
	case "read_file":
		label = "Reading"
	case "execute_command", "async_execute_command", "run_javascript":
		label = "Running"
	case "grep_files", "glob_files", "web_search", "arxiv_search":
		label = "Searching"
	case "web_fetch":
		label = "Fetching"
	case "list_directory":
		label = "Listing"
	case "update_plan":
		label = "Updating plan"
	case "get_plan":
		label = "Reading plan"
	}
	if argsSummary != "" {
		return label + " " + argsSummary
	}
	return label
}

// SummarizeToolArgs produces a compact summary of tool arguments for
// display in conversation data. This is a domain helper that replaces the
// previous tui/tools dependency.
func SummarizeToolArgs(tool, argsStr string) string {
	if tool == "update_plan" || tool == "get_plan" {
		return ExtractArg(argsStr, "plan_id")
	}
	if tool == "grep_files" {
		pattern := ExtractArg(argsStr, "pattern")
		path := ExtractArg(argsStr, "path")
		label := ""
		if pattern != "" {
			label = "\"" + TruncateLine(pattern, 30) + "\""
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

	if path := ExtractArg(argsStr, "path"); path != "" {
		return path
	}
	if pattern := ExtractArg(argsStr, "pattern"); pattern != "" {
		return pattern
	}
	if command := ExtractArg(argsStr, "command"); command != "" {
		return TruncateLine(command, 50)
	}
	if content := ExtractArg(argsStr, "content"); content != "" {
		return TruncateLine(content, 50) + " (content)"
	}
	if query := ExtractArg(argsStr, "query"); query != "" {
		return TruncateLine(query, 50)
	}
	if url := ExtractArg(argsStr, "url"); url != "" {
		return TruncateLine(url, 50)
	}
	if source := ExtractArg(argsStr, "source"); source != "" {
		dest := ExtractArg(argsStr, "destination")
		if dest != "" {
			return source + " → " + dest
		}
		return source
	}
	if code := ExtractArg(argsStr, "code"); code != "" {
		return TruncateLine(code, 50)
	}
	if tool == "async_execute_command" {
		handle := ExtractArg(argsStr, "handle")
		if command := ExtractArg(argsStr, "command"); command != "" {
			if handle != "" {
				return TruncateLine(command, 40) + " [" + handle + "]"
			}
			return TruncateLine(command, 50)
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
	return TruncateLine(argsStr, 60)
}

// ExtractArg extracts a key-value pair from a compact args string.
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
		idx := indexOf(str, prefix)
		if idx == -1 {
			continue
		}
		start := idx + len(prefix)
		if start >= len(str) {
			return ""
		}
		end := indexAny(str[start:], " ,}]")
		if end == -1 {
			return trimSpace(str[start:])
		}
		return trimSpace(str[start : start+end])
	}
	return ""
}

// PascalCaseTool converts a snake_case tool name to readable title words.
func PascalCaseTool(tool string) string {
	upper := true
	out := make([]byte, 0, len(tool))
	for i := 0; i < len(tool); i++ {
		ch := tool[i]
		if ch == '_' {
			out = append(out, ' ')
			upper = true
			continue
		}
		if upper && ch >= 'a' && ch <= 'z' {
			ch -= 32
		}
		upper = false
		out = append(out, ch)
	}
	return string(out)
}

func decodeRune(s string) (rune, int) {
	return utf8.DecodeRuneInString(s)
}

func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}

func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func indexAny(s, chars string) int {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return i
			}
		}
	}
	return -1
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
