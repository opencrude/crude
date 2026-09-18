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

func toolRowFrameSensitive(msg types.ChatMessage) bool {
	return (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") && msg.ToolPreviewPhase != types.ToolPreviewDone && msg.ToolPreviewPhase != types.ToolPreviewError
}

func toolSpinnerRole(msg types.ChatMessage, titlePhase string) ui.SemanticColor {
	isActive := toolRowFrameSensitive(msg)
	if isActive {
		switch titlePhase {
		case types.ToolPreviewPreparing:
			return ui.ColorWarning
		case types.ToolPreviewStreaming:
			return ui.ColorAccent2
		default:
			return ui.ColorAccent2
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

func buildToolTitleLine(msg types.ChatMessage, titlePhase string, spinFrame int, maxWidths ...int) PaintLine {
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
	isActive := toolRowFrameSensitive(msg)

	dot := "● "
	if isActive {
		dot = ui.ActivityDot(spinFrame) + " "
	} else if !isActive && msg.ToolPreviewPhase != types.ToolPreviewDone &&
		msg.ToolPreviewPhase != types.ToolPreviewError &&
		msg.ToolStatus != "success" && msg.ToolStatus != "error" {
		dot = "○ "
	}

	permSuffix := tools.PermTagSuffix(msg.PermTag)
	if len(maxWidths) > 0 && maxWidths[0] > 0 {
		fixedWidth := rendering.VisibleWidth("│ ") + rendering.VisibleWidth(dot) + rendering.VisibleWidth(permSuffix)
		title = rendering.TruncateMiddleVisual(title, max(1, maxWidths[0]-fixedWidth), "…")
	}
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: dot, Role: toolSpinnerRole(msg, titlePhase)},
		{Text: title, Role: ui.ColorMuted},
	}
	if permSuffix != "" {
		spans = append(spans, RowSpan{Text: permSuffix, Role: ui.ColorMuted})
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

func buildToolTruncationLine(remaining, width int) PaintLine {
	msg := fmt.Sprintf("+%s more lines", rendering.FormatCount(remaining))
	msg = rendering.TruncateLine(msg, max(1, width-toolBodyGutterWidth))
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

func renderToolMessageLines(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string, disclosure DisclosureState) messageLines {
	titlePhase := msg.ToolPreviewPhase
	if titlePhase == "" {
		titlePhase = phase
	}

	expandable := msg.ToolStatus == "success" && msg.Content != "" && toolSupportsDisclosure(msg.ToolName)
	titleWidth := contentWidth
	if titleWidth <= 0 {
		titleWidth = innerW
	}
	if expandable && titleWidth > 0 {
		titleWidth -= rendering.VisibleWidth(" ▸")
	}
	var out messageLines
	out.frameSensitive = toolRowFrameSensitive(msg)
	var titlePL PaintLine
	if titleWidth > 0 {
		titlePL = buildToolTitleLine(msg, titlePhase, spinFrame, max(1, titleWidth))
	} else {
		titlePL = buildToolTitleLine(msg, titlePhase, spinFrame)
	}
	if expandable {
		glyph := "▸"
		if disclosure == DisclosureExpanded {
			glyph = "▾"
		}
		titlePL = appendToolDisclosureGlyph(titlePL, glyph)
	}
	expanded := expandable && disclosure == DisclosureExpanded
	out.lines = append(out.lines, titlePL.PlainText())
	out.native = append(out.native, titlePL)
	if expanded && msg.ToolCommand != "" && (msg.ToolName == "execute_command" || msg.ToolName == "async_execute_command") {
		out.appendCommandContext(msg.ToolCommand, innerW)
	}
	if expanded && msg.ToolInput != "" {
		out.appendToolInputContext(msg, innerW)
	}

	// Completed tools render a budgeted preview by default; full output only
	// when the user expanded the block.
	if msg.ToolStatus == "success" && msg.Content != "" {
		resultContent := msg.Content
		fullLabel := rendering.PascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
		resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
		resultContent = strings.TrimSpace(resultContent)
		if resultContent != "" {
			switch {
			case msg.ToolName == "write_file" && !expanded:
				// Writes stay title-only in preview; counts already live in the
				// title and expansion reveals the full tinted additions diff.
			case isToolRichBody(msg, resultContent):
				out.appendRichToolBody(msg, resultContent, th, contentWidth, innerW, height, expanded)
			case tools.IsSummaryOnlyTool(msg.ToolName) && !expanded:
				// title only
			case isCompactEvidenceTool(msg.ToolName) && !expanded:
				// Compact evidence tools keep operands/count/duration in the title.
			case tools.IsSummaryOnlyTool(msg.ToolName):
				out.appendPlainToolBody(msg.ToolName, resultContent, innerW, height, false, true)
			default:
				// Search/listing tools lead with a self-describing count line
				// ("5 matches for X") that the title summary already renders as
				// "— 5 matches". Drop it from the body so it isn't printed twice.
				body := stripRedundantResultHeader(msg.ToolName, resultContent)
				out.appendPlainToolBody(msg.ToolName, body, innerW, height, false, expanded)
			}
		}
	} else if msg.ToolStatus == "error" {
		if msg.Content != "" {
			out.appendPlainToolBody(msg.ToolName, msg.Content, innerW, height, true, true)
		}
	} else if msg.ToolStatus == types.ToolStatusCancelled {
		if msg.Content != "" {
			out.appendPlainToolBody(msg.ToolName, msg.Content, innerW, height, false, true)
		}
	}

	// A cancelled tool call struck through entirely: the title line and any
	// body lines read as withdrawn work rather than a completed result.
	if msg.ToolStatus == types.ToolStatusCancelled {
		strikeBody(out.native)
	}

	return out
}

func (ml *messageLines) appendCommandContext(command string, width int) {
	command = rendering.SanitizeForTerminal(command)
	for _, line := range rendering.HardWrap(command, max(1, width-toolBodyGutterWidth)) {
		pl := PaintLine{Spans: []RowSpan{
			{Text: "│   ", Role: ui.ColorMuted},
			{Text: line, Role: ui.ColorFg},
		}}
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
}

func (ml *messageLines) appendToolInputContext(msg types.ChatMessage, width int) {
	label := "input: "
	switch msg.ToolName {
	case "grep_files":
		label = "pattern: "
	case "web_search", "arxiv_search":
		label = "query: "
	case "web_fetch":
		label = "url: "
	}
	text := label + rendering.SanitizeForTerminal(msg.ToolInput)
	if msg.ToolScope != "" && msg.ToolScope != "." {
		text += " in " + rendering.SanitizeForTerminal(msg.ToolScope)
	}
	for _, line := range rendering.HardWrap(text, max(1, width-toolBodyGutterWidth)) {
		pl := PaintLine{Spans: []RowSpan{
			{Text: "│   ", Role: ui.ColorMuted},
			{Text: line, Role: ui.ColorMuted, Attr: cell.AttrItalic},
		}}
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
}

// appendToolDisclosureGlyph adds the " ▸" affordance to a collapsed tool's
// title row so it reads as clickable even when the body is a budgeted preview.
func appendToolDisclosureGlyph(pl PaintLine, glyph string) PaintLine {
	pl.Spans = append(pl.Spans, RowSpan{Text: " " + glyph, Role: ui.ColorMuted})
	return pl
}

func toolSupportsDisclosure(toolName string) bool {
	switch toolName {
	case "execute_command", "async_execute_command", "read_async_output", "check_async_command",
		"edit_file", "write_file", "update_plan", "get_plan", "read_file", "grep_files", "glob_files",
		"list_directory", "web_search", "web_fetch", "arxiv_search":
		return true
	}
	return false
}

func isCompactEvidenceTool(name string) bool {
	switch name {
	case "execute_command", "async_execute_command", "read_async_output", "check_async_command",
		"read_file", "grep_files", "glob_files", "list_directory", "web_search", "web_fetch", "arxiv_search":
		return true
	default:
		return false
	}
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

// toolBodyGutterWidth is the width of the "│   " gutter every plain tool body
// row carries before its content. The wrap width must equal the painted
// content width so wrapped segments stay inside the shellchat LayoutRow band.
const toolBodyGutterWidth = 4

func (ml *messageLines) appendPlainToolBody(toolName, resultContent string, width, height int, errorStyle, expanded bool) {
	// Wrap at the real painted content width (not the full terminal width)
	// so every wrapped segment stays inside the LayoutRow band. The 4-col
	// margin mirrors the "│   " gutter in buildToolPlainResultLine.
	toolLineWidth := max(1, width-toolBodyGutterWidth)
	maxRes := toolPreviewHeight(toolName, height)
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
	previousBlank := false
	for _, rl := range rawLines {
		display := strings.TrimSuffix(rl, "\r")
		if strings.TrimSpace(display) == "" {
			if expanded || !previousBlank {
				wrapped = append(wrapped, "")
			}
			previousBlank = true
			continue
		}
		previousBlank = false
		if prefix != "" {
			display = strings.TrimPrefix(strings.TrimSpace(display), prefix)
		}
		sanitized := rendering.SanitizeForTerminal(display)
		// Word-wrap rather than character-wrap: breaking mid-token used to
		// strand 1–2 characters at the right edge of the preview band (e.g.
		// inside column-aligned Go identifiers). WordWrap falls back to a
		// hard break for tokenless runs, so width safety is preserved.
		for _, seg := range rendering.WordWrap(sanitized, toolLineWidth) {
			if seg != "" {
				wrapped = append(wrapped, seg)
			}
		}
	}
	indices, hidden := previewLineIndices(toolName, len(wrapped), maxRes, expanded)
	headCount := commandPreviewHeadCount(toolName, len(indices))
	for position, i := range indices {
		if hidden > 0 && isCommandEvidenceTool(toolName) && position == headCount {
			trunc := buildToolTruncationLine(hidden, width)
			ml.lines = append(ml.lines, trunc.PlainText())
			ml.native = append(ml.native, trunc)
		}
		seg := wrapped[i]
		pl := buildToolPlainResultLine("│   ", seg, errorStyle)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
	if hidden > 0 && !isCommandEvidenceTool(toolName) {
		trunc := buildToolTruncationLine(hidden, width)
		ml.lines = append(ml.lines, trunc.PlainText())
		ml.native = append(ml.native, trunc)
	}
}

func toolPreviewHeight(toolName string, height int) int {
	switch toolName {
	case "execute_command", "async_execute_command", "read_async_output", "check_async_command":
		return tools.TerminalResultHeight(height)
	case "grep_files", "web_search", "web_fetch", "arxiv_search":
		return min(4, tools.TerminalResultHeight(height))
	case "glob_files", "list_directory":
		return min(5, tools.TerminalResultHeight(height))
	case "search_history", "recall_memory":
		return min(4, tools.TerminalResultHeight(height))
	default:
		return min(12, tools.TerminalResultHeight(height))
	}
}

func isCommandEvidenceTool(toolName string) bool {
	switch toolName {
	case "execute_command", "async_execute_command", "read_async_output", "check_async_command":
		return true
	}
	return false
}

// previewLineIndices preserves both command setup and outcome. Less critical
// families keep their first useful rows and one explicit elision footer.
func previewLineIndices(toolName string, count, budget int, expanded bool) ([]int, int) {
	if expanded || budget <= 0 || count <= budget {
		indices := make([]int, count)
		for i := range indices {
			indices[i] = i
		}
		return indices, 0
	}
	visible := max(1, budget-1)
	hidden := count - visible
	if !isCommandEvidenceTool(toolName) {
		indices := make([]int, visible)
		for i := range indices {
			indices[i] = i
		}
		return indices, hidden
	}
	head := max(2, visible/3)
	tail := visible - head
	indices := make([]int, 0, visible)
	for i := 0; i < head; i++ {
		indices = append(indices, i)
	}
	for i := count - tail; i < count; i++ {
		indices = append(indices, i)
	}
	return indices, hidden
}

func commandPreviewHeadCount(toolName string, visible int) int {
	if !isCommandEvidenceTool(toolName) {
		return visible
	}
	return max(2, visible/3)
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

func (ml *messageLines) appendRichToolBody(msg types.ChatMessage, resultContent string, th *theme.Theme, contentWidth, innerW, height int, expanded bool) {
	_ = th
	if tools.IsPlanTool(msg.ToolName) {
		// Collapsed renders the windowed card around the live step; expanding
		// is what reveals the full list, so the old first-N cut is gone.
		planRows := BuildPlanViewRows(resultContent, contentWidth)
		if !expanded {
			planRows = BuildPlanViewRowsCollapsed(resultContent, contentWidth)
		}
		if len(planRows) == 0 {
			invalid := buildToolPlainResultLine("│   ", "Invalid canonical plan payload", true)
			ml.lines = append(ml.lines, invalid.PlainText())
			ml.native = append(ml.native, invalid)
			return
		}
		for _, pl := range planRows {
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		return
	}
	if tools.IsMemorySearchTool(msg.ToolName) {
		memLines := tools.BuildMemoryResultRows(msg.ToolName, resultContent, contentWidth)
		if len(memLines) > 0 {
			if !expanded && len(memLines) > 8 {
				hidden := len(memLines) - 7
				memLines = memLines[:7]
				for _, mline := range memLines {
					pl := buildMemoryToolRow(mline)
					ml.lines = append(ml.lines, pl.PlainText())
					ml.native = append(ml.native, pl)
				}
				trunc := buildToolTruncationLine(hidden, contentWidth)
				ml.lines = append(ml.lines, trunc.PlainText())
				ml.native = append(ml.native, trunc)
				return
			}
			for _, mline := range memLines {
				pl := buildMemoryToolRow(mline)
				ml.lines = append(ml.lines, pl.PlainText())
				ml.native = append(ml.native, pl)
			}
			return
		}
		// Non-JSON content (e.g. "No past turns matched...") renders as plain
		// body, not a diff.
		ml.appendPlainToolBody(msg.ToolName, resultContent, innerW, height, false, expanded)
		return
	}
	diffRows := BuildBandedDiffRows(resultContent, contentWidth, innerW, expanded)
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
