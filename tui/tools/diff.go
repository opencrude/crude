package tools

import (
	"crude/tui/rendering"
	"crude/tui/theme"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func RenderDiffContent(text string, maxWidth int, th *theme.Theme) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "```diff" || trimmed == "```" {
			continue
		}
		lines = append(lines, line)
	}

	fileStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.File).
		Background(lipgloss.Color("#1e2235"))
	hunkStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Hunk).
		Background(lipgloss.Color("#0d1117"))
	ctxStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Context).
		Background(lipgloss.Color("#0d1117"))
	addStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Add).
		Background(lipgloss.Color("#1c2a20"))
	delStyle := lipgloss.NewStyle().
		Foreground(th.Colors.Diff.Delete).
		Background(lipgloss.Color("#2a1d1e"))

	var sb strings.Builder
	for _, line := range lines {
		if runewidth.StringWidth(line) <= maxWidth {
			renderDiffLine(&sb, line, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle)
			sb.WriteByte('\n')
			continue
		}

		var prefix string
		content := line
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			prefix = line[:4]
			content = line[4:]
		case len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' '):
			prefix = line[:1]
			content = line[1:]
		}

		pw := runewidth.StringWidth(prefix)
		wrapW := maxWidth - pw
		if wrapW < 4 {
			wrapW = 4
		}
		segs := rendering.HardWrap(strings.TrimLeft(content, " "), wrapW)
		for i, seg := range segs {
			if i == 0 {
				renderDiffLine(&sb, prefix+seg, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle)
			} else {
				renderDiffLine(&sb, strings.Repeat(" ", pw)+seg, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle)
			}
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderDiffLine(sb *strings.Builder, line string, fileStyle, hunkStyle, ctxStyle, addStyle, delStyle lipgloss.Style) {
	switch {
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		sb.WriteString(fileStyle.Render(line))
	case strings.HasPrefix(line, "@@"):
		sb.WriteString(hunkStyle.Render(line))
	case strings.HasPrefix(line, "+"):
		sb.WriteString(addStyle.Render(line))
	case strings.HasPrefix(line, "-"):
		sb.WriteString(delStyle.Render(line))
	default:
		sb.WriteString(ctxStyle.Render(line))
	}
}

func RenderCompactDiff(toolName, content string, th *theme.Theme) []string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	addStyle := lipgloss.NewStyle().Foreground(th.Colors.Success)
	delStyle := lipgloss.NewStyle().Foreground(th.Colors.Error)

	diffContent := ExtractDiffBlock(content)
	if diffContent == "" {
		firstLine := strings.SplitN(content, "\n", 2)[0]
		return []string{dim.Render("  " + firstLine)}
	}

	var addedCount, removedCount int
	var diffLines []string

	for _, line := range strings.Split(diffContent, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---"):
			continue
		case strings.HasPrefix(trimmed, "@@"):
			continue
		case strings.HasPrefix(trimmed, "+"):
			addedCount++
			diffLines = append(diffLines, addStyle.Render("  + "+trimmed[1:]))
		case strings.HasPrefix(trimmed, "-"):
			removedCount++
			diffLines = append(diffLines, delStyle.Render("  ─ "+trimmed[1:]))
		}
	}

	if len(diffLines) == 0 {
		firstLine := strings.SplitN(content, "\n", 2)[0]
		return []string{dim.Render("  " + firstLine)}
	}

	var result []string
	result = append(result, dim.Render(fmt.Sprintf("  -%d/+%d lines", removedCount, addedCount)))
	result = append(result, diffLines...)
	return result
}

func ExtractDiffBlock(content string) string {
	start := strings.Index(content, "```diff")
	if start < 0 {
		return ""
	}
	start += len("```diff\n")
	end := strings.Index(content[start:], "```")
	if end < 0 {
		return ""
	}
	return content[start : start+end]
}

func IsDiffContent(content string) bool {
	text := strings.TrimSpace(content)
	if strings.Contains(text, "```diff") {
		return true
	}
	hasMinus := false
	hasPlus := false
	hasHunk := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "--- ") {
			hasMinus = true
		}
		if strings.HasPrefix(line, "+++ ") {
			hasPlus = true
		}
		if strings.HasPrefix(line, "@@") {
			hasHunk = true
		}
	}
	return (hasMinus && hasPlus) || hasHunk
}
