package jsonrepair

import (
	"encoding/json"
	"regexp"
	"strings"
)

func Repair(s string) string {
	s = strings.TrimSpace(s)

	repaired := findLargestValidJSON(s)
	if repaired != "" {
		var test any
		if json.Unmarshal([]byte(repaired), &test) == nil {
			return repaired
		}
	}

	repaired = fixUnescapedChars(s)
	if repaired != "" {
		var test any
		if json.Unmarshal([]byte(repaired), &test) == nil {
			return repaired
		}
	}

	repaired = EscapeRawNewlinesInStrings(s)
	if repaired != "" {
		var test any
		if json.Unmarshal([]byte(repaired), &test) == nil {
			return repaired
		}
	}

	return ""
}

func findLargestValidJSON(s string) string {
	best := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			open := s[i]
			close := byte('}')
			if s[i] == '[' {
				close = ']'
			}
			depth := 0
			inString := false
			escaped := false
			start := i
			for j := i; j < len(s); j++ {
				c := s[j]
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' && inString {
					escaped = true
					continue
				}
				if c == '"' {
					inString = !inString
					continue
				}
				if inString {
					continue
				}
				if c == open {
					depth++
				} else if c == close {
					depth--
					if depth == 0 {
						candidate := s[start : j+1]
						if len(candidate) > len(best) {
							best = candidate
						}
						break
					}
				}
			}
		}
	}
	return best
}

func fixUnescapedChars(s string) string {
	validEscape := regexp.MustCompile(`\\["\\/bfnrtu]`)

	s = validEscape.ReplaceAllStringFunc(s, func(match string) string {
		return "\x00ESC" + match[1:]
	})

	s = strings.ReplaceAll(s, `\`, `\\`)

	s = strings.ReplaceAll(s, "\x00ESC", `\`)

	return s
}

// escapeRawNewlinesInStrings finds raw newlines and tabs inside JSON string values
// and escapes them as \n and \t. This handles the common LLM mistake of including
// multi-line code in a JSON string value without proper escaping.
func EscapeRawNewlinesInStrings(s string) string {
	var b strings.Builder
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && inString {
			b.WriteByte(c)
			escaped = true
			continue
		}

		if c == '"' {
			inString = !inString
			b.WriteByte(c)
			continue
		}

		if inString {
			switch c {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteByte(c)
			}
			continue
		}

		b.WriteByte(c)
	}

	return b.String()
}

func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
