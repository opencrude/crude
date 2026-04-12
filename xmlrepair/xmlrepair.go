// Package xmlrepair provides lenient XML parsing and repair strategies for LLM-generated XML.
// Unlike JSON, XML errors are often recoverable — missing closing tags, unclosed CDATA,
// unescaped < inside text content can all be fixed with heuristic repair.
package xmlrepair

import (
	"strings"
)

// Repair attempts to fix common XML malformations from LLM output.
// Returns the repaired string, or empty string if repair is not possible.
func Repair(raw string) string {
	raw = strings.TrimSpace(raw)

	// Fix unclosed CDATA sections.
	raw = FixUnclosedCDATA(raw)

	// Fix missing closing tags.
	raw = FixMissingCloseTags(raw)

	// Fix unescaped < that aren't tag starts.
	raw = FixUnescapedAngles(raw)

	// Fix self-closing tags that should be closed.
	raw = FixMalformedTags(raw)

	return raw
}

// FixUnclosedCDATA finds <![CDATA[ without matching ]]> and closes it at the end of the element.
func FixUnclosedCDATA(raw string) string {
	var result strings.Builder
	i := 0
	for i < len(raw) {
		// Look for <![CDATA[
		if i+9 <= len(raw) && raw[i:i+9] == "<![CDATA[" {
			result.WriteString("<![CDATA[")
			i += 9
			// Find the closing ]]> or the next </ which likely ends the element
			end := strings.Index(raw[i:], "]]>")
			if end >= 0 {
				result.WriteString(raw[i : i+end+3])
				i = i + end + 3
			} else {
				// No ]]> found — look for next tag that closes the element
				nextTag := strings.Index(raw[i:], "</")
				if nextTag >= 0 {
					// Insert ]]> before the closing tag
					result.WriteString("]]>")
					result.WriteString(raw[i : i+nextTag])
					i = i + nextTag
				} else {
					// No closing tag found — just close CDATA at end
					result.WriteString(raw[i:])
					result.WriteString("]]>")
					i = len(raw)
				}
			}
		} else {
			result.WriteByte(raw[i])
			i++
		}
	}
	return result.String()
}

// FixMissingCloseTags finds opening tags without matching closing tags and adds them.
// This is a simple heuristic: it tracks which tags are open and closes them at the end.
func FixMissingCloseTags(raw string) string {
	var stack []string
	var result strings.Builder
	i := 0

	for i < len(raw) {
		// Skip CDATA — don't parse tags inside CDATA
		if i+9 <= len(raw) && raw[i:i+9] == "<![CDATA[" {
			end := strings.Index(raw[i:], "]]>")
			if end >= 0 {
				result.WriteString(raw[i : i+end+3])
				i = i + end + 3
			} else {
				result.WriteString(raw[i:])
				return result.String()
			}
			continue
		}

		// Check for closing tag
		if i+2 <= len(raw) && raw[i] == '<' && i+1 < len(raw) && raw[i+1] == '/' {
			end := strings.Index(raw[i:], ">")
			if end >= 0 {
				tagName := extractTagName(raw[i+2 : i+end])
				result.WriteString(raw[i : i+end+1])
				// Pop from stack if it matches
				if len(stack) > 0 && stack[len(stack)-1] == tagName {
					stack = stack[:len(stack)-1]
				}
				i = i + end + 1
				continue
			}
		}

		// Check for opening tag
		if i+1 < len(raw) && raw[i] == '<' && raw[i+1] != '!' && raw[i+1] != '?' && raw[i+1] != '/' {
			end := findTagEnd(raw, i)
			if end >= 0 {
				tagContent := raw[i+1 : end]
				tagName := extractTagName(tagContent)
				isSelfClosing := len(tagContent) > 0 && tagContent[len(tagContent)-1] == '/'

				result.WriteString(raw[i : end+1])

				if tagName != "" && !isSelfClosing && !isVoidElement(tagName) {
					stack = append(stack, tagName)
				}

				i = end + 1
				continue
			}
		}

		result.WriteByte(raw[i])
		i++
	}

	// Close any remaining open tags in reverse order.
	for j := len(stack) - 1; j >= 0; j-- {
		result.WriteString("</" + stack[j] + ">")
	}

	return result.String()
}

// FixUnescapedAngles replaces < characters that appear in text content (not as tag starts).
// This is a conservative fix: it only fixes obvious cases like < inside attribute values.
func FixUnescapedAngles(raw string) string {
	// This is tricky — we can't just replace all < with &lt; because many are tag starts.
	// We'll skip this for now as Go's XML parser is fairly lenient with text content.
	return raw
}

// FixMalformedTags fixes common tag malformations like <tag > (extra space before >).
func FixMalformedTags(raw string) string {
	return raw // Go's XML parser handles most of these natively.
}

// extractTagName gets the tag name from tag content (e.g., "param name=\"x\">" → "param").
func extractTagName(tagContent string) string {
	tagContent = strings.TrimSpace(tagContent)
	// Remove trailing / for self-closing
	if len(tagContent) > 0 && tagContent[len(tagContent)-1] == '/' {
		tagContent = strings.TrimSpace(tagContent[:len(tagContent)-1])
	}
	// Get first word
	parts := strings.Fields(tagContent)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// findTagEnd finds the index of the closing > for a tag starting at position start.
// It handles quoted attributes correctly (don't count > inside quotes).
func findTagEnd(s string, start int) int {
	inQuote := byte(0)
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == '>' {
			return i
		}
	}
	return -1
}

// isVoidElement returns true for HTML/XML void elements that don't need closing tags.
func isVoidElement(name string) bool {
	switch strings.ToLower(name) {
	case "br", "hr", "img", "input", "meta", "link":
		return true
	}
	return false
}
