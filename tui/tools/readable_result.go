package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// BuildReadableResult projects evidence without treating an arbitrary tool's
// JSON-looking prose as a command or a successful operation. The source strings
// remain complete; the caller decides what can fit in a compact preview.
func BuildReadableResult(content string) StructuredResult {
	result := StructuredResult{Summary: "Result"}
	content = strings.TrimSpace(content)
	if content == "" {
		result.Summary = "No result available"
		return result
	}
	for content != "" {
		content = strings.TrimSpace(content)
		if content == "" {
			break
		}
		// A service may flatten text parts and structured JSON on separate lines.
		// Decode one complete leading JSON value, then consume its remainder.
		if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") || strings.HasPrefix(content, "\"") {
			decoder := json.NewDecoder(strings.NewReader(content))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				if looksLikeBrokenJSON(content) {
					result.Sections = append(result.Sections, ResultSection{Body: "Structured result unavailable (invalid JSON)."})
				} else {
					result.Sections = append(result.Sections, ResultSection{Body: content})
				}
				break
			}
			consumed := decoder.InputOffset()
			if consumed < int64(len(content)) && content[consumed] != '\n' && content[consumed] != '\r' {
				// An embedded JSON fragment in a prose sentence is not a
				// structured service part.
				result.Sections = append(result.Sections, ResultSection{Body: content})
				break
			}
			appendReadableValue(&result, "", value)
			content = content[consumed:]
			continue
		}
		// Find the next whole-line JSON part; preserve all preceding prose.
		next := -1
		for _, marker := range []string{"\n{", "\n[", "\n\""} {
			if at := strings.Index(content, marker); at >= 0 && (next < 0 || at < next) {
				next = at
			}
		}
		if next < 0 {
			result.Sections = append(result.Sections, ResultSection{Body: content})
			break
		}
		result.Sections = append(result.Sections, ResultSection{Body: strings.TrimSpace(content[:next])})
		content = content[next+1:]
	}
	return result
}

func looksLikeBrokenJSON(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") {
		return len(value) == 1 || strings.ContainsAny(value[1:], "{[\"0123456789-tnf]")
	}
	if strings.HasPrefix(value, "{") {
		return len(value) == 1 || strings.HasPrefix(strings.TrimSpace(value[1:]), "\"") || !strings.Contains(value, "}")
	}
	return false
}

func appendReadableValue(result *StructuredResult, title string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		section := ResultSection{Title: title}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			switch typed[key].(type) {
			case map[string]any, []any:
			default:
				section.Details = append(section.Details, ResultField{Label: readableFieldLabel(key), Value: readableScalar(typed[key])})
			}
		}
		if len(section.Details) > 0 || len(keys) == 0 {
			if len(keys) == 0 {
				section.Body = "No fields"
			}
			result.Sections = append(result.Sections, section)
		}
		for _, key := range keys {
			switch typed[key].(type) {
			case map[string]any, []any:
				appendReadableValue(result, joinResultPath(title, readableFieldLabel(key)), typed[key])
			}
		}
	case []any:
		if len(typed) == 0 {
			result.Sections = append(result.Sections, ResultSection{Title: title, Body: "No items"})
		}
		for index, item := range typed {
			appendReadableValue(result, joinResultPath(title, fmt.Sprintf("%d", index+1)), item)
		}
	default:
		result.Sections = append(result.Sections, ResultSection{Title: title, Body: readableScalar(value)})
	}
}

func readableFieldLabel(key string) string {
	var label strings.Builder
	runes := []rune(key)
	for i, r := range runes {
		if r == '_' || r == '-' {
			label.WriteByte(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			label.WriteByte(' ')
		}
		label.WriteRune(r)
	}
	return strings.Join(strings.Fields(label.String()), " ")
}

func joinResultPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + " · " + child
}

func readableScalar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return "null"
	default:
		return fmt.Sprint(v)
	}
}
