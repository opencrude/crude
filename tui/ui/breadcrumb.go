package ui

import "strings"

// TitleBreadcrumb joins hierarchy labels for dock and form titles.
// Always uses a middle-dot, never a chevron or prompt glyph. Empty parts
// are dropped so any depth of page stack can share one helper.
func TitleBreadcrumb(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, " · ")
}
