package ui

import (
	"strings"

	"crude/tui/rendering"
)

const TitleBreadcrumbSeparator = " · "

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
	return strings.Join(out, TitleBreadcrumbSeparator)
}

// BreadcrumbIndex resolves a painted label in terminal columns. Separators,
// empty labels, and columns outside the clipped breadcrumb are not targets.
func BreadcrumbIndex(parts []string, separator string, x, width int) int {
	if x < 0 || x >= width {
		return -1
	}
	column, count := 0, 0
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if count > 0 {
			column += rendering.VisibleWidth(separator)
		}
		end := column + rendering.VisibleWidth(part)
		if x >= column && x < min(end, width) {
			return index
		}
		column, count = end, count+1
	}
	return -1
}
