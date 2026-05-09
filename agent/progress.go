package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type ToolCallPreview struct {
	ToolID   string
	ToolName string
	Params   map[string]any
	Complete bool
}

func ExtractPreview(id, name, accumulatedJSON string) ToolCallPreview {
	p := ToolCallPreview{
		ToolID:   id,
		ToolName: name,
		Params:   make(map[string]any),
	}

	if accumulatedJSON == "" {
		return p
	}

	cleaned := strings.TrimSpace(accumulatedJSON)

	var m map[string]any
	if err := json.Unmarshal([]byte(cleaned), &m); err == nil {
		p.Params = m
		p.Complete = true
		return p
	}

	p.Params = extractPartialPairs(cleaned)
	return p
}

func extractPartialPairs(jsonStr string) map[string]any {
	result := make(map[string]any)

	idx := 0
	for idx < len(jsonStr) {
		quoteStart := strings.IndexByte(jsonStr[idx:], '"')
		if quoteStart < 0 {
			break
		}
		quoteStart += idx

		keyStart := quoteStart + 1
		keyEnd, ok := findStrEnd(jsonStr, keyStart)
		if !ok {
			break
		}
		key := jsonStr[keyStart:keyEnd]

		afterKey := keyEnd + 1
		if afterKey >= len(jsonStr) {
			break
		}

		colonPos := strings.IndexByte(jsonStr[afterKey:], ':')
		if colonPos < 0 {
			idx = afterKey
			continue
		}
		colonPos += afterKey

		if !isValidIdent(key) {
			idx = colonPos + 1
			continue
		}

		valStart := colonPos + 1
		for valStart < len(jsonStr) && (jsonStr[valStart] == ' ' || jsonStr[valStart] == '\t' || jsonStr[valStart] == '\n' || jsonStr[valStart] == '\r') {
			valStart++
		}

		if valStart >= len(jsonStr) {
			result[key] = ""
			break
		}

		switch jsonStr[valStart] {
		case '"':
			strEnd, ok := findStrEnd(jsonStr, valStart+1)
			if ok {
				val := jsonStr[valStart+1 : strEnd]
				result[key] = unesc(val)
				idx = strEnd + 1
			} else {
				partial := jsonStr[valStart+1:]
				result[key] = unesc(partial)
				idx = len(jsonStr)
			}

		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			numEnd := valStart + 1
			for numEnd < len(jsonStr) && isNumChar(jsonStr[numEnd]) {
				numEnd++
			}
			result[key] = jsonStr[valStart:numEnd]
			idx = numEnd

		case 't', 'f':
			if strings.HasPrefix(jsonStr[valStart:], "true") {
				result[key] = true
				idx = valStart + 4
			} else if strings.HasPrefix(jsonStr[valStart:], "false") {
				result[key] = false
				idx = valStart + 5
			} else {
				idx = valStart + 1
			}

		case 'n':
			if strings.HasPrefix(jsonStr[valStart:], "null") {
				result[key] = nil
				idx = valStart + 4
			} else {
				idx = valStart + 1
			}

		case '[':
			end := findMatch(jsonStr, valStart, '[', ']')
			if end >= 0 {
				var arr []any
				if json.Unmarshal([]byte(jsonStr[valStart:end+1]), &arr) == nil {
					result[key] = arr
				} else {
					result[key] = jsonStr[valStart : end+1]
				}
				idx = end + 1
			} else {
				result[key] = jsonStr[valStart:]
				idx = len(jsonStr)
			}

		case '{':
			end := findMatch(jsonStr, valStart, '{', '}')
			if end >= 0 {
				var obj map[string]any
				if json.Unmarshal([]byte(jsonStr[valStart:end+1]), &obj) == nil {
					result[key] = obj
				} else {
					result[key] = jsonStr[valStart : end+1]
				}
				idx = end + 1
			} else {
				result[key] = jsonStr[valStart:]
				idx = len(jsonStr)
			}

		default:
			idx = valStart + 1
		}
	}

	return result
}

func findStrEnd(s string, start int) (int, bool) {
	i := start
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if s[i] == '"' {
			return i, true
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			size = 1
		}
		i += size
	}
	return len(s), false
}

func unesc(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"':
				b.WriteByte('"')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case '/':
				b.WriteByte('/')
				i++
			case 'n':
				b.WriteByte('\n')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case 'r':
				b.WriteByte('\r')
				i++
			default:
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func findMatch(s string, start int, open, close byte) int {
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		if inStr {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if s[i] == '"' {
				inStr = false
			}
			continue
		}
		if s[i] == '"' {
			inStr = true
			continue
		}
		if s[i] == open {
			depth++
		} else if s[i] == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isValidIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func isNumChar(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E'
}

func CountLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func FormatPreview(p ToolCallPreview) string {
	name := pascalTool(p.ToolName)
	path := getPS(p.Params, "path")
	content := getPS(p.Params, "content")
	command := getPS(p.Params, "command")
	query := getPS(p.Params, "query")
	url := getPS(p.Params, "url")
	source := getPS(p.Params, "source")
	dest := getPS(p.Params, "destination")
	oldStr := getPS(p.Params, "old_string")
	newStr := getPS(p.Params, "new_string")

	switch p.ToolName {
	case "write_file":
		if path != "" && content != "" {
			return fmt.Sprintf("%s %s (%d lines)", name, path, CountLines(content))
		}
		if path != "" {
			return fmt.Sprintf("%s %s \u2026", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "edit_file":
		if path != "" && oldStr != "" && newStr != "" {
			return fmt.Sprintf("%s %s (\u2212%d/+%d lines)", name, path, CountLines(oldStr), CountLines(newStr))
		}
		if path != "" {
			return fmt.Sprintf("%s %s \u2026", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "delete_lines":
		if path != "" {
			return fmt.Sprintf("%s %s (\u2212\u2217 lines)", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "replace_lines":
		if path != "" {
			newText := getPS(p.Params, "new_text")
			if newText != "" {
				return fmt.Sprintf("%s %s (+%d lines)", name, path, CountLines(newText))
			}
			return fmt.Sprintf("%s %s \u2026", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "read_file":
		if path != "" {
			return fmt.Sprintf("%s %s", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "execute_command":
		if command != "" {
			return fmt.Sprintf("%s %s", name, trunc(command, 50))
		}
		return fmt.Sprintf("%s \u2026", name)

	case "move_file", "copy_file":
		if source != "" && dest != "" {
			return fmt.Sprintf("%s %s \u2192 %s", name, source, dest)
		}
		if source != "" {
			return fmt.Sprintf("%s %s \u2192 \u2026", name, source)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "delete_file":
		if path != "" {
			return fmt.Sprintf("%s %s", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "list_directory":
		if path != "" {
			return fmt.Sprintf("%s %s", name, path)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "glob_files":
		if pat := getPS(p.Params, "pattern"); pat != "" {
			return fmt.Sprintf("%s %s", name, pat)
		}
		return fmt.Sprintf("%s \u2026", name)

	case "grep_files":
		if query != "" {
			return fmt.Sprintf("%s %s", name, trunc(query, 50))
		}
		if pat := getPS(p.Params, "pattern"); pat != "" {
			return fmt.Sprintf("%s %s", name, trunc(pat, 50))
		}
		return fmt.Sprintf("%s \u2026", name)

	case "web_search":
		if query != "" {
			return fmt.Sprintf("%s %s", name, trunc(query, 50))
		}
		return fmt.Sprintf("%s \u2026", name)

	case "web_fetch":
		if url != "" {
			return fmt.Sprintf("%s %s", name, trunc(url, 50))
		}
		return fmt.Sprintf("%s \u2026", name)

	default:
		if path != "" {
			return fmt.Sprintf("%s %s", name, path)
		}
		if command != "" {
			return fmt.Sprintf("%s %s", name, trunc(command, 50))
		}
		return name
	}
}

func getPS(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\u2026"
}

func pascalTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

type ToolProgress struct {
	Phase         string
	Message       string
	Path          string
	LinesRead     int
	LinesWritten  int
	LinesAdded    int
	LinesRemoved  int
}

func NewToolProgress(phase, message, path string) ToolProgress {
	return ToolProgress{
		Phase:   phase,
		Message: message,
		Path:    path,
	}
}

func ParseDiffStats(result string) (additions, removals int) {
	diffStart := strings.Index(result, "```diff")
	if diffStart < 0 {
		return 0, 0
	}
	diffStart += len("```diff\n")
	diffEnd := strings.Index(result[diffStart:], "```")
	if diffEnd < 0 {
		diffEnd = len(result) - diffStart
	}
	diff := result[diffStart : diffStart+diffEnd]
	for _, line := range strings.Split(diff, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		if strings.HasPrefix(trimmed, "@@") {
			continue
		}
		if strings.HasPrefix(trimmed, "+") {
			additions++
		} else if strings.HasPrefix(trimmed, "-") {
			removals++
		}
	}
	return additions, removals
}
