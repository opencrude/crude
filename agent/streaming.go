package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"crude/jsonrepair"
	"crude/logger"

	"github.com/buger/jsonparser"
)

type ToolCallPreview struct {
	ToolID   string
	ToolName string
	Params   map[string]any
	Complete bool
}

func ExtractPreview(id, name, accumulatedJSON string, lg *logger.Logger) ToolCallPreview {
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
	unmarshalErr := json.Unmarshal([]byte(cleaned), &m)
	if unmarshalErr == nil {
		p.Params = m
		p.Complete = true
		return p
	}

	if !looksComplete(cleaned) {
		p.Params = extractWithJSONParser(cleaned, name)
		return p
	}

	if lg != nil {
		lg.Debug("STREAM", "json_unmarshal_failed", "tool", name, "tool_id", id, "error", unmarshalErr.Error())
	}

	repaired, repairErr := jsonrepair.Repair(cleaned)
	if repairErr == nil {
		if err := json.Unmarshal([]byte(repaired), &m); err == nil {
			if lg != nil {
				lg.Debug("STREAM", "json_repaired", "tool", name, "tool_id", id)
			}
			p.Params = m
			return p
		}
	} else if lg != nil {
		lg.Debug("STREAM", "json_repair_failed", "tool", name, "tool_id", id, "repair_error", repairErr.Error())
	}

	fallbackData := cleaned
	if repairErr == nil {
		fallbackData = repaired
	}
	if lg != nil {
		lg.Debug("STREAM", "json_fallback_parser", "tool", name, "tool_id", id)
	}
	p.Params = extractWithJSONParser(fallbackData, name)
	return p
}

func looksComplete(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return true
	}
	if s[0] == '{' {
		last := findLastNonWhitespace(s)
		return last == '}'
	}
	if s[0] == '[' {
		last := findLastNonWhitespace(s)
		return last == ']'
	}
	return true
}

func findLastNonWhitespace(s string) byte {
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return s[i]
		}
	}
	return 0
}

func extractWithJSONParser(data string, toolName string) map[string]any {
	result := make(map[string]any)

	keys := previewParamKeys(toolName)
	if len(keys) == 0 {
		keys = defaultParamKeys()
	}

	dataBytes := []byte(data)

	for _, key := range keys {
		val, dataType, _, err := jsonparser.Get(dataBytes, key)
		if err != nil {
			continue
		}

		switch dataType {
		case jsonparser.String:
			unescaped, _ := jsonparser.Unescape(val, nil)
			result[key] = string(unescaped)
		case jsonparser.Number:
			result[key] = string(val)
		case jsonparser.Boolean:
			result[key] = string(val) == "true"
		case jsonparser.Null:
			result[key] = nil
		case jsonparser.Array:
			var arr []any
			if json.Unmarshal(val, &arr) == nil {
				result[key] = arr
			} else {
				result[key] = string(val)
			}
		case jsonparser.Object:
			var obj map[string]any
			if json.Unmarshal(val, &obj) == nil {
				result[key] = obj
			} else {
				result[key] = string(val)
			}
		}
	}

	if len(result) == 0 {
		result = fallbackKVScan(data)
	}

	return result
}

func previewParamKeys(toolName string) []string {
	keys, ok := toolPreviewKeys[toolName]
	if !ok {
		return nil
	}
	return keys
}

func defaultParamKeys() []string {
	return []string{"path", "command", "url", "query", "source", "destination", "pattern", "handle", "seconds"}
}

var toolPreviewKeys = map[string][]string{
	"write_file":            {"path", "content"},
	"edit_file":             {"path", "old_string", "new_string", "new_text"},
	"read_file":             {"path", "offset", "limit", "encoded"},
	"execute_command":       {"command", "cwd", "timeout_seconds"},
	"delete_file":           {"path"},
	"move_file":             {"source", "destination"},
	"copy_file":             {"source", "destination"},
	"list_directory":        {"path", "recursive"},
	"glob_files":            {"pattern", "base_dir", "include_hidden"},
	"grep_files":            {"pattern", "path", "recursive", "include_hidden"},
	"web_search":            {"query"},
	"web_fetch":             {"url", "offset", "limit"},
	"arxiv_search":          {"query", "max_results"},
	"run_javascript":        {"code"},
	"reply":                 {"content"},
	"plan":                  {"objective", "steps"},
	"mark_step_doing":       {"step_id"},
	"mark_step_complete":    {"step_id", "result", "activate_steps"},
	"mark_step_failed":      {"step_id", "reason"},
	"report_blocker":        {"blocker_type", "message"},
	"resolve_blocker":       {"message"},
	"list_goal_status":      {},
	"async_execute_command": {"command", "handle", "cwd", "timeout_seconds"},
	"check_async_command":   {"handle"},
	"kill_async_command":    {"handle"},
	"read_async_output":     {"handle", "since_byte"},
	"wait":                  {"seconds"},
}

func fallbackKVScan(jsonStr string) map[string]any {
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
				result[key] = unesc(jsonStr[valStart+1 : strEnd])
				idx = strEnd + 1
			} else {
				result[key] = unesc(jsonStr[valStart+1:])
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
