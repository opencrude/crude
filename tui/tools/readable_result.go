package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		// A string field may itself carry a JSON document (an event body,
		// an encoded payload). It is structure, so it nests like any other
		// object instead of painting as raw JSON.
		embedded := make(map[string]any)
		for _, key := range keys {
			switch value := typed[key].(type) {
			case map[string]any, []any:
			case string:
				if lead, nested, trailing, truncated, ok := splitEmbeddedJSON(value); ok {
					if nested != nil {
						embedded[key] = nested
					}
					if text := embeddedJSONLead(lead, trailing, truncated); text != "" {
						section.Details = append(section.Details, ResultField{Label: readableFieldLabel(key), Value: text})
					}
					continue
				}
				section.Details = append(section.Details, ResultField{Label: readableFieldLabel(key), Value: value})
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
			value := typed[key]
			if nested, ok := embedded[key]; ok {
				value = nested
			}
			switch value.(type) {
			case map[string]any, []any:
				appendReadableValue(result, joinResultPath(title, readableFieldLabel(key)), value)
			}
		}
	case []any:
		if len(typed) == 0 {
			result.Sections = append(result.Sections, ResultSection{Title: title, Body: "No items"})
		}
		for index, item := range typed {
			appendReadableValue(result, joinResultPath(title, fmt.Sprintf("%d", index+1)), item)
		}
	case string:
		if lead, nested, trailing, truncated, ok := splitEmbeddedJSON(typed); ok {
			if text := embeddedJSONLead(lead, trailing, truncated); text != "" {
				result.Sections = append(result.Sections, ResultSection{Title: title, Body: text})
			}
			if nested != nil {
				appendReadableValue(result, title, nested)
			}
			return
		}
		result.Sections = append(result.Sections, ResultSection{Title: title, Body: typed})
	default:
		result.Sections = append(result.Sections, ResultSection{Title: title, Body: readableScalar(value)})
	}
}

// splitEmbeddedJSON reports a string value that carries a JSON object or
// array: the whole value, or the tail after a prose lead ending in ": "
// ("Background command x settled after 3s: {...}"). Prose after the document
// on later lines is kept as trailing text. A document cut off by an upstream
// length bound is reported with a nil value and truncated set, so callers keep
// the lead instead of painting the fragment. JSON fragments inside ordinary
// prose, and strings that merely contain braces, stay text.
func splitEmbeddedJSON(value string) (lead string, nested any, trailing string, truncated, ok bool) {
	decode := func(text string) (any, string, bool, bool) {
		text = strings.TrimLeft(text, " \t")
		if !strings.HasPrefix(text, "{") && !strings.HasPrefix(text, "[") {
			return nil, "", false, false
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			// Only an unexpected end is a cut-off document; any other syntax
			// error means this is prose that happens to start with a brace.
			if errors.Is(err, io.ErrUnexpectedEOF) && looksLikeBrokenJSON(text) {
				return nil, "", true, true
			}
			return nil, "", false, false
		}
		rest := text[decoder.InputOffset():]
		if strings.TrimSpace(rest) != "" && !strings.HasPrefix(strings.TrimLeft(rest, " \t\r"), "\n") {
			return nil, "", false, false
		}
		switch typed := decoded.(type) {
		case map[string]any:
			if len(typed) == 0 {
				return nil, "", false, false
			}
		case []any:
			if len(typed) == 0 {
				return nil, "", false, false
			}
		default:
			return nil, "", false, false
		}
		return decoded, strings.TrimSpace(rest), false, true
	}
	if decoded, rest, cut, found := decode(value); found {
		return "", decoded, rest, cut, true
	}
	for _, marker := range []string{": {", ": ["} {
		if at := strings.Index(value, marker); at > 0 && !strings.Contains(value[:at], "\n") {
			if decoded, rest, cut, found := decode(value[at+2:]); found {
				return strings.TrimSpace(value[:at]), decoded, rest, cut, true
			}
		}
	}
	return "", nil, "", false, false
}

// embeddedJSONLead is the readable text kept beside a nested document.
func embeddedJSONLead(lead, trailing string, truncated bool) string {
	if truncated {
		if lead == "" {
			return "Structured detail truncated."
		}
		lead += " (structured detail truncated)"
	}
	if trailing != "" {
		if lead != "" {
			lead += "\n"
		}
		lead += trailing
	}
	return lead
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
