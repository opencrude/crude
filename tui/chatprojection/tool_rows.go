package chatprojection

import (
	"fmt"
	"strings"

	toolargs "crude/tools"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types"
)

func toolRowFrameSensitive(msg types.ChatMessage) bool {
	return (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") && msg.ToolPreviewPhase != types.ToolPreviewDone && msg.ToolPreviewPhase != types.ToolPreviewError
}

func buildProjectToolTitleLine(msg types.ChatMessage, titlePhase string, spinFrame int, paths *tools.ProjectPaths) PaintLine {
	title := tools.BuildToolTitle(msg, titlePhase)
	title.Operand = paths.ToolOperand(&msg, title.Operand)
	isActive := toolRowFrameSensitive(msg)
	settled := msg.ToolPreviewPhase == types.ToolPreviewDone || msg.ToolPreviewPhase == types.ToolPreviewError
	return paintToolTitle(title, msg.ToolStatus, isActive, settled, spinFrame, tools.PermTagSuffix(msg.PermTag))
}

// paintToolTitle is shared by tool calls and code-authored command outcomes.
// Their action, operand, lifecycle indicator and metadata use identical ink.
func paintToolTitle(title tools.ToolTitle, status string, active, settled bool, spinFrame int, permSuffix string) PaintLine {
	dot := "● "
	if title.Failed {
		dot = "× "
	} else if status == types.ToolStatusCancelled || status == "interrupted" {
		dot = "○ "
	} else if active {
		dot = ui.ActivityDot(spinFrame) + " "
	} else if !settled && status != types.ToolStatusSuccess && status != types.ToolStatusError {
		dot = "○ "
	}

	titleRole := ui.ColorToolTitle
	iconRole := ui.ColorToolTitle
	if status == types.ToolStatusCancelled || status == "interrupted" {
		iconRole = ui.ColorMuted
	}
	if title.Failed {
		titleRole, iconRole = ui.ColorError, ui.ColorError
	} else if title.Warning {
		titleRole, iconRole = ui.ColorWarning, ui.ColorWarning
		dot = "○ "
	}
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: dot, Role: iconRole},
		{Text: rendering.SanitizeForTerminal(title.Action), Role: titleRole},
	}
	if title.Operand != "" {
		spans = append(spans, RowSpan{Text: " " + rendering.SanitizeForTerminal(title.Operand), Role: ui.ColorToolOperand})
	}
	if title.Metadata != "" {
		spans = append(spans, RowSpan{Text: " " + rendering.SanitizeForTerminal(title.Metadata), Role: ui.ColorMuted})
	}
	if permSuffix != "" {
		spans = append(spans, RowSpan{Text: permSuffix, Role: ui.ColorMuted})
	}
	return PaintLine{Spans: spans}
}

func buildProjectToolTitleLines(msg types.ChatMessage, phase string, frame, width int, disclosure DisclosureState, paths *tools.ProjectPaths) []PaintLine {
	titleMessage := msg
	if disclosure == DisclosureExpanded && proseFailure(msg) {
		titleMessage.Content = ""
		titleMessage.ToolOutcome.Summary = ""
	}
	header := buildProjectToolTitleLine(titleMessage, phase, frame, paths)
	var suffix []RowSpan
	if toolSupportsDisclosure(msg.ToolName) && (msg.Content != "" || msg.ToolCommand != "" || msg.ToolInput != "") {
		glyph := " ▸"
		if disclosure == DisclosureExpanded {
			glyph = " ▾"
		}
		suffix = []RowSpan{{Text: glyph, Role: ui.ColorMuted}}
	}
	if disclosure != DisclosureExpanded && !toolRowFrameSensitive(msg) {
		return wrapCompactToolTitle(header, width, suffix)
	}
	header.Spans = append(header.Spans, suffix...)
	return wrapToolTitle(header, width)
}

// Closed results use at most two title rows on the actual terminal grid.
// This bounds presentation, not evidence: expansion restores the complete title
// and body. Keep the disclosure control visible even on constrained grids.
func wrapCompactToolTitle(header PaintLine, width int, suffix []RowSpan) []PaintLine {
	if width <= 0 {
		return nil
	}
	trailer := PaintLine{Spans: suffix}
	trailerWidth := trailer.VisibleWidth()
	clip := func(line PaintLine, columns int) PaintLine {
		if line.VisibleWidth() <= columns {
			return line
		}
		if columns <= 0 {
			return PaintLine{}
		}
		line = TruncatePaintLine(line, columns-1)
		line.Spans = append(line.Spans, RowSpan{Text: "…", Role: ui.ColorMuted})
		return line
	}
	if header.VisibleWidth()+trailerWidth <= width {
		header.Spans = append(header.Spans, suffix...)
		return []PaintLine{header}
	}
	// A long indivisible path must not push the factual reason offscreen.
	// Keep operation/operand on the first row and reason/metadata on the second.
	// The expanded header uses the full shared wrapping path instead.
	metaStart := len(header.Spans)
	for i := 3; i < len(header.Spans); i++ {
		if header.Spans[i].Role == ui.ColorMuted {
			metaStart = i
			break
		}
	}
	if metaStart < len(header.Spans) {
		first := clip(PaintLine{Spans: header.Spans[:metaStart]}, width)
		prefix := header.Spans[0].Text + strings.Repeat(" ", rendering.VisibleWidth(header.Spans[1].Text))
		second := PaintLine{Spans: []RowSpan{{Text: prefix, Role: ui.ColorMuted}}}
		if width <= rendering.VisibleWidth(prefix)+trailerWidth {
			second.Spans = nil
		}
		second.Spans = append(second.Spans, clip(PaintLine{Spans: header.Spans[metaStart:]}, max(0, width-second.VisibleWidth()-trailerWidth)).Spans...)
		second.Spans = append(second.Spans, suffix...)
		return []PaintLine{first, TruncatePaintLine(second, width)}
	}
	rows := wrapToolTitle(header, width)
	if len(rows) == 0 {
		return rows
	}
	more := len(rows) > 2
	if more {
		rows = rows[:2]
	}
	last := len(rows) - 1
	rows[last] = clip(rows[last], max(0, width-trailerWidth))
	if more && rows[last].VisibleWidth() < width-trailerWidth {
		rows[last].Spans = append(rows[last].Spans, RowSpan{Text: "…", Role: ui.ColorMuted})
	}
	rows[last].Spans = append(rows[last].Spans, suffix...)
	return rows
}

func wrapToolTitle(header PaintLine, width int) []PaintLine {
	if width <= 0 {
		return nil
	}
	label := PaintLine{Spans: header.Spans[2:]}
	var lines []PaintLine
	prefixWidth := rendering.VisibleWidth(header.Spans[0].Text + header.Spans[1].Text)
	if width <= prefixWidth {
		return wrapPaintLine(label, width)
	}
	continuation := header.Spans[0].Text + strings.Repeat(" ", rendering.VisibleWidth(header.Spans[1].Text))
	for i, line := range wrapPaintLine(label, max(1, width-prefixWidth)) {
		prefix := []RowSpan{{Text: continuation, Role: ui.ColorMuted}}
		if i == 0 {
			prefix = append([]RowSpan(nil), header.Spans[:2]...)
		}
		line.Spans = append(prefix, line.Spans...)
		lines = append(lines, line)
	}
	return lines
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

func buildToolTruncationLine(remaining, width int) PaintLine {
	msg := fmt.Sprintf("+%s more lines", rendering.FormatCount(remaining))
	msg = rendering.TruncateLine(msg, max(1, width-toolBodyGutterWidth))
	return PaintLine{Spans: []RowSpan{
		{Text: "│   ", Role: ui.ColorMuted},
		{Text: msg, Role: ui.ColorMuted},
	}}
}

// isToolRichBody reports whether a tool result renders through a rich body
// builder instead of the plain prose rows. A patch card must contain a patch
// from a tool that writes file content: the route needs both the patching tool
// name and a payload that really is a unified diff. Content sniffing alone
// misroutes evidence: a read of a fixture that embeds "```diff" in a string
// literal would be painted in the patch gutter, and a tool name alone
// misroutes prose: a no-change edit whose body is only the result's own
// summary sentence would become a fake hunk row through the lenient parser.
func isToolRichBody(msg types.ChatMessage, resultContent string) bool {
	if tools.IsPlanTool(msg.ToolName) {
		return true
	}
	return tools.IsPatchCardTool(msg.ToolName) && tools.IsDiffContent(resultContent)
}

func renderProjectToolMessageLines(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string, disclosure DisclosureState, paths *tools.ProjectPaths) messageLines {
	commandResult, commandOK := toolargs.ParseCommandResult(msg.ToolName, msg.Content)
	if commandOK {
		if commandResult.Cancelled {
			msg.ToolStatus, msg.ToolPreviewPhase = types.ToolStatusCancelled, types.ToolPreviewDone
		} else if commandResult.Failed() {
			msg.ToolStatus, msg.ToolPreviewPhase = types.ToolStatusError, types.ToolPreviewError
		}
	}
	titlePhase := msg.ToolPreviewPhase
	if titlePhase == "" {
		titlePhase = phase
	}
	switch msg.ToolStatus {
	case types.ToolStatusSuccess:
		titlePhase = types.ToolPreviewDone
	case types.ToolStatusError:
		titlePhase = types.ToolPreviewError
	case types.ToolStatusCalling, types.ToolStatusStreaming:
		if titlePhase == "" {
			titlePhase = types.ToolPreviewStreaming
		}
	}

	var out messageLines
	out.frameSensitive = toolRowFrameSensitive(msg)
	expanded := disclosure == DisclosureExpanded
	for _, line := range buildProjectToolTitleLines(msg, titlePhase, spinFrame, innerW, disclosure, paths) {
		out.lines = append(out.lines, line.PlainText())
		out.native = append(out.native, line)
	}
	if !expanded && msg.ToolStatus != types.ToolStatusCalling && msg.ToolStatus != types.ToolStatusStreaming {
		return out
	}
	if isCommandEvidenceTool(msg.ToolName) && !expanded {
		return out
	}
	if tools.IsStructuredTool(msg.ToolName) {
		if msg.ToolStatus == types.ToolStatusSuccess {
			out.appendStructuredToolBody(tools.BuildStructuredResult(msg.ToolName, msg.Content, msg.ToolInput), msg.ToolInput, innerW, height, expanded)
		} else if expanded {
			out.appendStructuredToolBody(tools.BuildReadableResult(msg.Content), msg.ToolInput, innerW, height, expanded)
		}
		return out
	}
	if commandOK {
		out.appendPlainToolBody(msg.ToolName, tools.CommandDetails(commandResult), innerW, height, false, true)
		if commandResult.Error != "" {
			out.appendPlainToolBody(msg.ToolName, commandResult.Error, innerW, height, false, true)
		}
		output := tools.CommandOutput(commandResult)
		output = rendering.CommandOutputText(output)
		if output == "" {
			output = "(no output captured)"
			switch {
			case msg.ToolName == "check_async_command":
				output = "(status only; output is not included in this result)"
			case commandResult.Page != nil:
				output = "(no bytes in this output range)"
			case commandResult.Output != "":
				output = "(no printable output; control sequences only)"
			case commandResult.OutputDescriptor.TotalBytes > 0:
				output = "(output retained under this handle; not included in this result)"
			case commandResult.OutputDescriptor.Status == "running":
				output = "(no output captured yet; command is running)"
			}
		}
		out.appendPlainToolBody(msg.ToolName, output, innerW, height, false, expanded)
		return out
	}
	if isCommandEvidenceTool(msg.ToolName) && (msg.ToolStatus == types.ToolStatusSuccess || msg.ToolStatus == types.ToolStatusError || expanded && msg.ToolStatus == types.ToolStatusCancelled) {
		// Plain failures and retained historical output use the same literal
		// body, without guessing headers, stripping paths or trimming indentation.
		out.appendPlainToolBody(msg.ToolName, msg.Content, innerW, height, false, expanded)
		return out
	}

	// Settled results are title-only until the user asks for full evidence.
	if msg.ToolStatus == "success" && msg.Content != "" {
		resultContent := msg.Content
		fullLabel := rendering.PascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
		resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
		resultContent = strings.TrimSpace(resultContent)
		if resultContent != "" {
			switch {
			case tools.IsPlanTool(msg.ToolName) && msg.ToolOutcome.Code == "no_active_plan":
				// The owner supplied an empty, successful lookup, not a plan card.
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
				// a count. Drop it from the body so it isn't printed twice.
				body := stripRedundantResultHeader(msg.ToolName, resultContent)
				out.appendPlainToolBody(msg.ToolName, body, innerW, height, false, expanded)
			}
		}
	} else if msg.ToolStatus != types.ToolStatusCalling && msg.ToolStatus != types.ToolStatusStreaming {
		if msg.ToolName == "apply_patch" && tools.IsDiffContent(msg.Content) {
			prefix, _, _ := strings.Cut(msg.Content, "```diff")
			prose := msg
			prose.Content = prefix
			out.appendPlainToolBody(msg.ToolName, prose.Content, innerW, height, false, true)
			out.appendRichToolBody(msg, msg.Content, th, contentWidth, innerW, height, true)
		} else if msg.Content != "" {
			if tools.IsPlanTool(msg.ToolName) && strings.HasPrefix(strings.TrimSpace(msg.Content), "{") {
				out.appendStructuredToolBody(tools.BuildReadableResult(msg.Content), "", innerW, height, true)
			} else {
				content := msg.Content
				out.appendPlainToolBody(msg.ToolName, content, innerW, height, false, true)
			}
		}
	}

	return out
}

func toolSupportsDisclosure(toolName string) bool {
	return toolName != "" && toolName != "reply"
}

func proseFailure(message types.ChatMessage) bool {
	return message.Content != "" && message.ToolStatus != types.ToolStatusSuccess &&
		message.ToolStatus != types.ToolStatusCalling && message.ToolStatus != types.ToolStatusStreaming &&
		!toolargs.IsCommandTool(message.ToolName) && !tools.IsStructuredTool(message.ToolName) && !tools.IsPlanTool(message.ToolName)
}

func isCompactEvidenceTool(name string) bool {
	switch name {
	case "read_file", "grep_files", "glob_files", "list_directory", "web_search", "web_fetch", "arxiv_search":
		return true
	default:
		return false
	}
}

// stripRedundantResultHeader removes a leading self-describing count line from
// search/listing tool bodies because the title summary already shows the count
// (e.g. "5 matches"). Only tools that emit such a header participate, and
// only a first line matching the "<n> (match|matches|file|files|entries|result|results)"
// shape is dropped; a first line with real content is never removed.
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
	if resultContent == "" {
		return
	}
	// Wrap at the real painted content width (not the full terminal width)
	// so every wrapped segment stays inside the LayoutRow band. The 4-col
	// margin mirrors the "│   " gutter in buildToolPlainResultLine.
	gutter := "│   "
	if width <= toolBodyGutterWidth {
		gutter = ""
	}
	toolLineWidth := max(1, width-rendering.VisibleWidth(gutter))
	maxRes := toolPreviewHeight(toolName, height)
	// Grep/find-style output repeats the same absolute path prefix on every
	// line ("/home/u/proj/tui/file.go:12: ..."). Stripping that shared prefix
	// fits far more matches per line; a short header shows what was stripped.
	var nonBlank []string
	resultContent = rendering.CommandOutputText(resultContent)
	rawLines := strings.Split(strings.TrimSuffix(resultContent, "\n"), "\n")
	for _, rl := range rawLines {
		if strings.TrimSpace(rl) != "" {
			nonBlank = append(nonBlank, strings.TrimSpace(rl))
		}
	}
	prefix := ""
	if !isCommandEvidenceTool(toolName) {
		prefix = commonDirPrefix(nonBlank)
	}
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
		sanitized := rendering.ExpandTabs(display, 8)
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
	visible := len(wrapped)
	if !expanded && maxRes > 0 && visible > maxRes {
		visible = max(1, maxRes-1)
	}
	for _, seg := range wrapped[:visible] {
		pl := buildToolPlainResultLine(gutter, seg, errorStyle)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
	if visible < len(wrapped) {
		trunc := buildToolTruncationLine(len(wrapped)-visible, width)
		ml.lines = append(ml.lines, trunc.PlainText())
		ml.native = append(ml.native, trunc)
	}
}

func toolPreviewHeight(toolName string, height int) int {
	switch toolName {
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
	return toolargs.IsCommandTool(toolName)
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
	diffRows, syntaxPending := buildBandedDiffRows(resultContent, contentWidth, innerW, expanded)
	// Rows whose colors are still being lexed repaint on the next activity
	// phase, like an animating row, and stop once the colors arrive.
	ml.frameSensitive = ml.frameSensitive || syntaxPending
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
