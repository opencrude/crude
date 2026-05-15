package tools

import (
	"crude/tui/rendering"
	"crude/tui/theme"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ── Structured diff types ──────────────────────────────────────────

type diffLineType int

const (
	lineContext diffLineType = iota
	lineAdd
	lineDelete
)

type diffLine struct {
	Type      diffLineType
	NewLineNum int    // 0 for deletions (no position in new file)
	Content   string // the line body (without +/- prefix)
}

type diffHunk struct {
	StartLine int        // new-file start line from @@ header
	Lines     []diffLine
}

type diffFile struct {
	Path       string
	AddCount   int
	DelCount   int
	Hunks      []diffHunk
}

// ── Unified diff parser ────────────────────────────────────────────

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseUnifiedDiff extracts structured data from a raw unified diff string.
// It strips markdown fences (```diff / ```), file headers (+++ / ---),
// @@ hunk markers, and git-specific metadata headers (diff --git, index,
// mode changes, renames, etc.), returning only the semantically useful data:
// file path, change counts, and per-hunk line classifications with new-file
// line numbers.
func parseUnifiedDiff(raw string) []diffFile {
	text := raw

	// Strip markdown diff fences
	if idx := strings.Index(text, "```diff\n"); idx >= 0 {
		text = text[idx+len("```diff\n"):]
		if end := strings.Index(text, "```"); end >= 0 {
			text = text[:end]
		}
	}
	text = strings.TrimRight(text, "\n")

	var files []diffFile
	var current *diffFile
	var currentHunk *diffHunk

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")

		// File headers — extract path and start new file block
		if strings.HasPrefix(line, "--- ") {
			if current != nil && currentHunk != nil {
				current.Hunks = append(current.Hunks, *currentHunk)
				currentHunk = nil
			}
			path := strings.TrimPrefix(line, "--- ")
			path = strings.TrimPrefix(path, "a/")
			if current == nil {
				current = &diffFile{Path: path}
			}
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			path := strings.TrimPrefix(line, "+++ ")
			path = strings.TrimPrefix(path, "b/")
			if current != nil {
				current.Path = path
			} else {
				current = &diffFile{Path: path}
			}
			continue
		}

		// Hunk header — extract new-file start line
		if strings.HasPrefix(line, "@@") {
			if current != nil && currentHunk != nil {
				current.Hunks = append(current.Hunks, *currentHunk)
			}
			m := hunkRe.FindStringSubmatch(line)
			startLine := 1
			if len(m) >= 2 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					startLine = n
				}
			}
			currentHunk = &diffHunk{StartLine: startLine}
			continue
		}

		// Skip git-specific metadata headers — these appear before the
		// standard unified diff headers (---/+++) and are not content.
		if strings.HasPrefix(line, "diff --git ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "new file mode ") ||
			strings.HasPrefix(line, "deleted file mode ") ||
			strings.HasPrefix(line, "old mode ") ||
			strings.HasPrefix(line, "new mode ") ||
			strings.HasPrefix(line, "similarity index ") ||
			strings.HasPrefix(line, "rename from ") ||
			strings.HasPrefix(line, "rename to ") ||
			strings.HasPrefix(line, "copy from ") ||
			strings.HasPrefix(line, "copy to ") {
			continue
		}

		if current == nil {
			current = &diffFile{}
		}
		if currentHunk == nil {
			currentHunk = &diffHunk{StartLine: 1}
		}

		// Content lines
		switch {
		case strings.HasPrefix(line, "+"):
			current.AddCount++
			currentHunk.Lines = append(currentHunk.Lines, diffLine{
				Type:    lineAdd,
				Content: line[1:],
			})
		case strings.HasPrefix(line, "-"):
			current.DelCount++
			currentHunk.Lines = append(currentHunk.Lines, diffLine{
				Type:    lineDelete,
				Content: line[1:],
			})
		default:
			// Context line (may have leading space or be empty)
			content := line
			if strings.HasPrefix(content, " ") && len(content) > 0 {
				content = content[1:]
			}
			currentHunk.Lines = append(currentHunk.Lines, diffLine{
				Type: lineContext,
				Content: content,
			})
		}
	}

	if current != nil {
		if currentHunk != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
		}
		files = append(files, *current)
	}

	// Assign new-file line numbers
	for fi := range files {
		for hi := range files[fi].Hunks {
			lineNum := files[fi].Hunks[hi].StartLine
			for li := range files[fi].Hunks[hi].Lines {
				dl := &files[fi].Hunks[hi].Lines[li]
				switch dl.Type {
				case lineContext, lineAdd:
					dl.NewLineNum = lineNum
					lineNum++
				case lineDelete:
					dl.NewLineNum = 0
				}
			}
		}
	}

	return files
}

// ── Rendering constants ────────────────────────────────────────────

const (
	compactContextLines = 2 // context lines around change groups in compact mode
)

// ── RenderDiff — the single unified diff renderer ──────────────────

// RenderDiff produces a professional, line-numbered diff presentation.
// maxLines controls how many output lines are available (from terminal height);
// 0 means unlimited. maxWidth controls horizontal space (from terminal width).
// The function adapts context depth based on available space but always uses
// the same visual language: clean header, numbered gutter, change indicators.
func RenderDiff(raw string, maxLines int, maxWidth int, th *theme.Theme) string {
	return renderDiff(raw, maxLines, maxWidth, th, false)
}

// RenderDiffNoHeader renders a diff without the file header line (▌ path −X +Y).
// Used when the title line already shows file path and change counts.
func RenderDiffNoHeader(raw string, maxLines int, maxWidth int, th *theme.Theme) string {
	return renderDiff(raw, maxLines, maxWidth, th, true)
}

func renderDiff(raw string, maxLines int, maxWidth int, th *theme.Theme, skipHeader bool) string {
	files := parseUnifiedDiff(raw)
	if len(files) == 0 {
		return ""
	}

	// Styles
	mutedStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	addStyle := lipgloss.NewStyle().Foreground(th.Colors.Diff.Add)
	delStyle := lipgloss.NewStyle().Foreground(th.Colors.Diff.Delete)
	ctxStyle := lipgloss.NewStyle().Foreground(th.Colors.Diff.Context)
	accentStyle := lipgloss.NewStyle().Foreground(th.Colors.Accent)
	pathStyle := lipgloss.NewStyle().Foreground(th.Colors.Diff.File)
	sepStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)

	var sb strings.Builder

	for fi, file := range files {
		if fi > 0 {
			sb.WriteString("\n")
		}

		headerLines := 0
		if !skipHeader {
			// Header: ▌ filepath   −X +Y
			sb.WriteString(accentStyle.Render("▌ "))
			sb.WriteString(pathStyle.Render(file.Path))

			stats := ""
			if file.DelCount > 0 {
				stats += delStyle.Render(fmt.Sprintf(" −%d", file.DelCount))
			}
			if file.AddCount > 0 {
				if stats != "" {
					stats += mutedStyle.Render(" ")
				}
				stats += addStyle.Render(fmt.Sprintf("+%d", file.AddCount))
			}
			if stats != "" {
				sb.WriteString(mutedStyle.Render("   "))
				sb.WriteString(stats)
			}
			sb.WriteString("\n")
			headerLines = 1
		}

		// Determine if we need compact mode (trim context)
		allRenderLines := buildRenderLines(file.Hunks)
		needCompact := maxLines > 0 && len(allRenderLines)+headerLines > maxLines

		var renderLines []renderLine
		if needCompact {
			renderLines = trimContext(allRenderLines, compactContextLines)
		} else {
			renderLines = allRenderLines
		}

		// If still too many lines, truncate from bottom
		availLines := maxLines
		if availLines > 0 {
			availLines -= headerLines
		}
		truncated := false
		truncatedRemaining := 0
		if availLines > 0 && len(renderLines) > availLines {
			truncatedRemaining = len(renderLines) - availLines
			renderLines = renderLines[:availLines]
			truncated = true
		}

		// Compute max line number width for gutter alignment
		maxNumWidth := 3
		for _, rl := range renderLines {
			if rl.lineNum > 0 {
				w := runewidth.StringWidth(strconv.Itoa(rl.lineNum))
				if w > maxNumWidth {
					maxNumWidth = w
				}
			}
		}

		// Render lines
		for _, rl := range renderLines {
			if rl.ellipsis {
				sb.WriteString(mutedStyle.Render(fmt.Sprintf("%s   ···", strings.Repeat(" ", maxNumWidth))))
				sb.WriteString("\n")
				continue
			}

			// Gutter: line number + separator
			numStr := ""
			if rl.lineNum > 0 {
				numStr = strconv.Itoa(rl.lineNum)
			}
			paddedNum := fmt.Sprintf("%*s", maxNumWidth, numStr)

			// Indicator
			indicator := " "
			indStyle := ctxStyle
			contentStyle := ctxStyle
			switch rl.lineType {
			case lineAdd:
				indicator = "+"
				indStyle = addStyle
				contentStyle = addStyle
			case lineDelete:
				indicator = "─"
				indStyle = delStyle
				contentStyle = delStyle
			case lineContext:
				indicator = " "
				indStyle = mutedStyle
				contentStyle = ctxStyle
			}

			// Content with word-wrapping
			content := rl.content
			gutterWidth := maxNumWidth + 3 + 2 // numStr + " │ " + "IND "
			contentWidth := maxWidth - gutterWidth
			if contentWidth < 10 {
				contentWidth = 10
			}

			if runewidth.StringWidth(content) <= contentWidth {
				sb.WriteString(mutedStyle.Render(paddedNum))
				sb.WriteString(sepStyle.Render(" │ "))
				sb.WriteString(indStyle.Render(indicator + " "))
				sb.WriteString(contentStyle.Render(content))
				sb.WriteString("\n")
			} else {
				// Word-wrap long lines
				segs := rendering.HardWrap(content, contentWidth)
				for si, seg := range segs {
					if si == 0 {
						sb.WriteString(mutedStyle.Render(paddedNum))
						sb.WriteString(sepStyle.Render(" │ "))
						sb.WriteString(indStyle.Render(indicator + " "))
					} else {
						sb.WriteString(mutedStyle.Render(strings.Repeat(" ", maxNumWidth)))
						sb.WriteString(sepStyle.Render(" │ "))
						sb.WriteString(strings.Repeat(" ", 2)) // align under content
					}
					sb.WriteString(contentStyle.Render(seg))
					sb.WriteString("\n")
				}
			}
		}

		if truncated {
			sb.WriteString(mutedStyle.Render(fmt.Sprintf("%s   +%d more changes not shown",
				strings.Repeat(" ", maxNumWidth), truncatedRemaining)))
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// ── Render line helpers ────────────────────────────────────────────

type renderLine struct {
	lineNum   int
	lineType  diffLineType
	content   string
	ellipsis  bool
}

func buildRenderLines(hunks []diffHunk) []renderLine {
	var lines []renderLine
	for _, hunk := range hunks {
		for _, dl := range hunk.Lines {
			lines = append(lines, renderLine{
				lineNum:  dl.NewLineNum,
				lineType: dl.Type,
				content:  dl.Content,
			})
		}
	}
	return lines
}

// trimContext reduces context lines around change groups to `keep` lines
// before and after each change, inserting ellipsis markers between separated
// sections.
func trimContext(lines []renderLine, keep int) []renderLine {
	if len(lines) == 0 {
		return lines
	}

	// Mark which lines are changes or adjacent context
	keepFlags := make([]bool, len(lines))

	// Find change positions
	changePositions := []int{}
	for i, rl := range lines {
		if rl.lineType == lineAdd || rl.lineType == lineDelete {
			changePositions = append(changePositions, i)
		}
	}

	if len(changePositions) == 0 {
		// No changes — return first and last context line
		if len(lines) <= keep*2 {
			return lines
		}
		var result []renderLine
		result = append(result, lines[:keep]...)
		result = append(result, renderLine{ellipsis: true})
		result = append(result, lines[len(lines)-keep:]...)
		return result
	}

	// For each change position, keep `keep` context lines before and after
	for _, cp := range changePositions {
		for j := cp - keep; j <= cp+keep; j++ {
			if j >= 0 && j < len(lines) {
				keepFlags[j] = true
			}
		}
	}

	// Merge overlapping kept regions and build result with ellipsis
	var result []renderLine
	lastKept := -1

	for i, rl := range lines {
		if keepFlags[i] {
			if i-lastKept > 1 && lastKept >= 0 {
				result = append(result, renderLine{ellipsis: true})
			}
			result = append(result, rl)
			lastKept = i
		}
	}

	return result
}

// IsDiffContent returns true if the content contains unified diff markers.
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
