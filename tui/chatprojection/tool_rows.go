package chatprojection

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/types"
	"crude/tui/ui"
)

func toolSpinnerRole(msg types.ChatMessage, phase string) ui.SemanticColor {
	isActive := (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") &&
		msg.ToolPreviewPhase != types.ToolPreviewDone && msg.ToolPreviewPhase != types.ToolPreviewError
	if isActive {
		switch phase {
		case "grounding":
			return ui.ColorAccent
		case "planning":
			return ui.ColorAccent2
		case "executing":
			return ui.ColorSuccess
		default:
			return ui.ColorMuted
		}
	}
	if msg.ToolPreviewPhase == types.ToolPreviewDone {
		if msg.ToolStatus == types.ToolStatusCancelled {
			return ui.ColorWarning
		}
		return ui.ColorSuccess
	}
	if msg.ToolPreviewPhase == types.ToolPreviewError || msg.ToolStatus == "error" {
		return ui.ColorError
	}
	if msg.ToolStatus == "cancelled" {
		return ui.ColorWarning
	}
	return ui.ColorMuted
}

func buildToolTitleLine(msg types.ChatMessage, titlePhase, phase string, spinFrame int) PaintLine {
	title := tools.BuildToolTitle(msg, titlePhase)
	if msg.ToolStatus == "success" {
		dur := rendering.FormatDuration(msg.ToolDuration)
		target := msg.ToolArgs
		if target == "" {
			target = msg.ToolProgressLabel
		}
		if summary := tools.ResultSummary(msg.ToolName, msg.Content, target, dur); summary != "" {
			title = summary
		}
	}
	isActive := (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") &&
		msg.ToolPreviewPhase != types.ToolPreviewDone && msg.ToolPreviewPhase != types.ToolPreviewError

	dot := "● "
	if isActive && !ui.BlinkOn(spinFrame) {
		dot = "○ "
	} else if !isActive && msg.ToolPreviewPhase != types.ToolPreviewDone &&
		msg.ToolPreviewPhase != types.ToolPreviewError &&
		msg.ToolStatus != "success" && msg.ToolStatus != "error" {
		dot = "○ "
	}

	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: dot, Role: toolSpinnerRole(msg, phase)},
		{Text: title, Role: ui.ColorMuted},
	}
	if suffix := tools.PermTagSuffix(msg.PermTag); suffix != "" {
		spans = append(spans, RowSpan{Text: suffix, Role: ui.ColorMuted})
	}
	return PaintLine{Spans: spans}
}

func buildToolPlainResultLine(prefix, text string, errorStyle bool) PaintLine {
	spans := []RowSpan{{Text: prefix, Role: ui.ColorMuted}}
	role := ui.ColorMuted
	if errorStyle {
		role = ui.ColorError
	}
	spans = append(spans, RowSpan{Text: text, Role: role})
	return PaintLine{Spans: spans}
}

// buildMemoryToolRow renders a structured memory-tool result line with the
// shared tool gutter ("│   ") and a kind-specific semantic role: titles use
// accent, metadata uses muted, body uses foreground.
func buildMemoryToolRow(line tools.MemoryLine) PaintLine {
	spans := []RowSpan{{Text: "│   ", Role: ui.ColorMuted}}
	switch line.Kind {
	case tools.MemoryRowTitle:
		spans = append(spans, RowSpan{Text: line.Text, Role: ui.ColorAccent2, Attr: cell.AttrBold})
	case tools.MemoryRowMeta:
		spans = append(spans, RowSpan{Text: line.Text, Role: ui.ColorMuted})
	default:
		spans = append(spans, RowSpan{Text: line.Text, Role: ui.ColorFg})
	}
	return PaintLine{Spans: spans}
}

func buildToolTruncationLine(remaining int) PaintLine {
	msg := fmt.Sprintf("+%s more lines", rendering.FormatCount(remaining))
	return PaintLine{Spans: []RowSpan{
		{Text: "│   ", Role: ui.ColorMuted},
		{Text: msg, Role: ui.ColorMuted},
	}}
}

func isToolRichBody(msg types.ChatMessage, resultContent string) bool {
	if tools.IsPlanTool(msg.ToolName) {
		return true
	}
	if tools.IsMemorySearchTool(msg.ToolName) {
		return true
	}
	if isDiffToolName(msg.ToolName) || tools.IsDiffContent(resultContent) {
		return true
	}
	return false
}

func isDiffToolName(name string) bool {
	switch name {
	case "write_file", "edit_file":
		return true
	}
	return false
}

func renderToolMessageLines(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string) messageLines {
	titlePhase := msg.ToolPreviewPhase
	if titlePhase == "" {
		titlePhase = phase
	}

	var out messageLines
	titlePL := buildToolTitleLine(msg, titlePhase, phase, spinFrame)
	out.lines = append(out.lines, titlePL.PlainText())
	out.native = append(out.native, titlePL)

	if msg.ToolStatus == "success" && msg.Content != "" {
		resultContent := msg.Content
		fullLabel := rendering.PascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
		resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
		resultContent = strings.TrimSpace(resultContent)
		if resultContent != "" {
			if isToolRichBody(msg, resultContent) {
				out.appendRichToolBody(msg, resultContent, th, contentWidth, innerW, height)
			} else if tools.IsSummaryOnlyTool(msg.ToolName) {
				// title only
			} else {
				// Search/listing tools lead with a self-describing count line
				// ("5 matches for X") that the title summary already renders as
				// "— 5 matches". Drop it from the body so it isn't printed twice.
				body := stripRedundantResultHeader(msg.ToolName, resultContent)
				out.appendPlainToolBody(body, width, height, false)
			}
		}
	} else if msg.ToolStatus == "error" {
		if msg.Content != "" {
			out.appendPlainToolBody(msg.Content, width, height, true)
		}
	} else if msg.ToolStatus == types.ToolStatusCancelled {
		if msg.Content != "" {
			out.appendPlainToolBody(msg.Content, width, height, false)
		}
	}

	// A cancelled tool call struck through entirely: the title line and any
	// body lines read as withdrawn work rather than a completed result.
	if msg.ToolStatus == types.ToolStatusCancelled {
		strikeBody(out.native)
	}

	return out
}

// stripRedundantResultHeader removes a leading self-describing count line from
// search/listing tool bodies because the title summary already shows the count
// (e.g. "— 5 matches"). Only tools that emit such a header participate, and
// only a first line matching the "<n> (match|matches|file|files|entries|result|results)"
// shape is dropped — a first line with real content is never removed.
func stripRedundantResultHeader(toolName, content string) string {
	switch toolName {
	case "grep_files", "glob_files", "web_search", "arxiv_search", "execute_command":
		// execute_command output is arbitrary; don't guess at its first line.
		if toolName == "execute_command" {
			return content
		}
	default:
		return content
	}
	firstNL := strings.Index(content, "\n")
	if firstNL <= 0 {
		return content
	}
	first := strings.TrimSpace(content[:firstNL])
	// "Web search results for: <query>" repeats the title's `Searched "<query>"`.
	if toolName == "web_search" && strings.HasPrefix(strings.ToLower(first), "web search results for:") {
		return strings.TrimSpace(content[firstNL+1:])
	}
	if isCountHeader(first) {
		return strings.TrimSpace(content[firstNL+1:])
	}
	return content
}

func isCountHeader(line string) bool {
	// "<n> <unit> ..." where unit is one of the known search/listing nouns.
	i := strings.Index(line, " ")
	if i <= 0 {
		return false
	}
	num := line[:i]
	for _, ch := range num {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	rest := strings.ToLower(line[i+1:])
	for _, unit := range []string{"match", "matches", "file", "files", "entries", "result", "results", "papers"} {
		if strings.HasPrefix(rest, unit+" ") || rest == unit {
			return true
		}
	}
	return false
}

func (ml *messageLines) appendPlainToolBody(resultContent string, width, height int, errorStyle bool) {
	toolLineWidth := width - 6
	if toolLineWidth < 20 {
		toolLineWidth = 20
	}
	maxRes := tools.TerminalResultHeight(height)
	// Grep/find-style output repeats the same absolute path prefix on every
	// line ("/home/u/proj/tui/file.go:12: ..."). Stripping that shared prefix
	// fits far more matches per line; a short header shows what was stripped.
	var nonBlank []string
	rawLines := strings.Split(resultContent, "\n")
	for _, rl := range rawLines {
		if strings.TrimSpace(rl) != "" {
			nonBlank = append(nonBlank, strings.TrimSpace(rl))
		}
	}
	prefix := commonDirPrefix(nonBlank)
	if prefix != "" {
		header := buildPlainToolResultLine(prefixHeader(prefix))
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}
	var wrapped []string
	for _, rl := range rawLines {
		trimmed := strings.TrimSpace(rl)
		if trimmed == "" {
			wrapped = append(wrapped, "")
			continue
		}
		if prefix != "" {
			trimmed = strings.TrimPrefix(trimmed, prefix)
		}
		sanitized := rendering.SanitizeForTerminal(trimmed)
		for _, seg := range rendering.HardWrap(sanitized, toolLineWidth) {
			if seg != "" {
				wrapped = append(wrapped, seg)
			}
		}
	}
	for i, seg := range wrapped {
		if i >= maxRes-1 {
			remaining := len(wrapped) - i
			trunc := buildToolTruncationLine(remaining)
			ml.lines = append(ml.lines, trunc.PlainText())
			ml.native = append(ml.native, trunc)
			break
		}
		pl := buildToolPlainResultLine("│   ", seg, errorStyle)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
}

// commonDirPrefix returns the longest directory prefix shared by every line,
// cut at a path separator and ending in "/". Returns "" unless every line
// begins with an absolute path under a common directory (so scattered output
// is never mangled).
func commonDirPrefix(lines []string) string {
	if len(lines) < 2 {
		return ""
	}
	prefix := lines[0]
	for _, l := range lines[1:] {
		for !strings.HasPrefix(l, prefix) {
			if len(prefix) == 0 {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	// Cut back to the last path separator and require a root-ish absolute path
	// ("/a/b/") so "3 result"/"line 1" fragments are never treated as paths.
	slash := strings.LastIndex(prefix, "/")
	if slash < 1 {
		return ""
	}
	prefix = prefix[:slash+1]
	// Require at least two path segments ("/x/") to avoid stripping "/".
	if strings.Count(prefix, "/") < 2 {
		return ""
	}
	return prefix
}

func prefixHeader(prefix string) string {
	return "in " + prefix
}

func buildPlainToolResultLine(text string) PaintLine {
	return PaintLine{Spans: []RowSpan{
		{Text: "│   ", Role: ui.ColorMuted},
		{Text: text, Role: ui.ColorMuted, Attr: cell.AttrItalic},
	}}
}

func (ml *messageLines) appendRichToolBody(msg types.ChatMessage, resultContent string, th *theme.Theme, contentWidth, innerW, height int) {
	_ = th
	if tools.IsPlanTool(msg.ToolName) {
		planRows := BuildPlanRows(resultContent, contentWidth)
		for _, pl := range planRows {
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		return
	}
	if tools.IsMemorySearchTool(msg.ToolName) {
		memLines := tools.BuildMemoryResultRows(msg.ToolName, resultContent, contentWidth)
		if len(memLines) > 0 {
			for _, mline := range memLines {
				pl := buildMemoryToolRow(mline)
				ml.lines = append(ml.lines, pl.PlainText())
				ml.native = append(ml.native, pl)
			}
			return
		}
		// Non-JSON content (e.g. "No past turns matched...") renders as plain
		// body, not a diff.
		ml.appendPlainToolBody(resultContent, contentWidth, height, false)
		return
	}
	maxRes := tools.TerminalResultHeight(height)
	diffWidth := innerW - 4
	if diffWidth < 20 {
		diffWidth = 20
	}
	diffRows := BuildDiffRows(resultContent, maxRes, diffWidth)
	if len(diffRows) > 0 {
		for _, pl := range diffRows {
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
	} else {
		firstLine := strings.SplitN(resultContent, "\n", 2)[0]
		pl := buildToolPlainResultLine("│   ", firstLine, false)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
}
