package chatprojection

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/glamour/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	"crude/types/reasoning"
)

// messageLines is one render pass producing parallel plain-text lines and
// native rows. After the ANSI-fallback deletion, native rows are always real
// PaintLines (never PreferANSI); lines is kept as a plain-text mirror for
// selection/copy.
type messageLines struct {
	lines          []string
	native         []PaintLine
	frameSensitive bool
}

func (ml messageLines) withCursor(cursorIdx, spinFrame int, activeTyping bool) messageLines {
	if cursorIdx < 0 || cursorIdx >= len(ml.lines) {
		return ml
	}
	out := ml
	out.frameSensitive = out.frameSensitive || !activeTyping
	out.native = append([]PaintLine(nil), ml.native...)
	out.native[cursorIdx] = appendBlockCursor(ml.native[cursorIdx], spinFrame, activeTyping)
	out.lines = append([]string(nil), ml.lines...)
	out.lines[cursorIdx] = out.native[cursorIdx].PlainText()
	return out
}

type renderContext struct {
	th                  *theme.Theme
	tokens              *ui.TokenSet
	contentWidth        int
	innerW              int
	width               int
	height              int
	chatHeight          int
	spinFrame           int
	activeTyping        bool
	legacyModelFallback string
	username            string
	streaming           bool
	isLastMsg           bool
	phase               string
	mdRenderer          *glamour.TermRenderer
	// disclosure is the resolved expand/collapse mode for this message
	// (see disclosure.go). Deterministic defaults keep live and hydrated
	// sessions rendering the same shape.
	disclosure DisclosureState
	// thinkingBodyOnly is set for promoted PaneThinking content. The parent
	// pane renders Thinking/Thought chrome and owns disclosure.
	thinkingBodyOnly bool
	// planSuperseded hides repeated plan snapshots by default while preserving
	// each tool's preparing/doing/done title and expandable content.
	planSuperseded bool
	// streamNative holds the pre-bridged native rows for the completed portion
	// of a streaming assistant message (built once per StreamCompletedLen change
	// by the Renderer, never per frame). Set only for the streaming cursor.
	streamNative []PaintLine
}

func renderUserMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	referenceLabels := userReferenceLabels(msg.Content, msg.ReferenceLabels)
	header := buildUserHeader(ctx.username, rendering.FormatTimeLabel(msg.Time), ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}

	rendered, isMarkdown := renderMarkdown(msg.Content, ctx.mdRenderer)
	if isMarkdown {
		for _, pl := range BridgeAnsiLines(strings.Split(rendered, "\n"), ctx.tokens) {
			pl = styleReferenceLabels(pl, referenceLabels)
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
	} else {
		wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
		body := buildPlainBodyLines(wrapped)
		for _, pl := range body {
			pl = styleReferenceLabels(pl, referenceLabels)
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
	}
	if msg.PendingBtw {
		ml.frameSensitive = true
		line := PaintLine{Spans: []RowSpan{
			{Text: "│ ", Role: ui.ColorMuted},
			{Text: ui.ActivityDot(ctx.spinFrame) + " ", Role: ui.ColorAccent},
			{Text: "Waiting to send", Role: ui.ColorMuted},
		}}
		ml.lines = append(ml.lines, line.PlainText())
		ml.native = append(ml.native, line)
	}
	return ml
}

func renderAssignmentMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	header := buildAssistantHeader("Main Agent Assignment", false, rendering.FormatTimeLabel(msg.Time), ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}
	rendered, isMarkdown := renderMarkdown(msg.Content, ctx.mdRenderer)
	if isMarkdown {
		for _, pl := range BridgeAnsiLines(strings.Split(rendered, "\n"), ctx.tokens) {
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		return ml
	}
	for _, pl := range buildPlainBodyLines(rendering.WrapText(msg.Content, ctx.contentWidth)) {
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
	return ml
}

func renderAssistantMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	timeLabel := rendering.FormatTimeLabel(msg.Time)
	modelName := msg.ModelID
	if modelName == "" {
		// Sessions written before per-turn attribution use the renderer's
		// explicitly configured legacy fallback. Attributed rows never do.
		modelName = ctx.legacyModelFallback
	}
	header := buildAssistantHeader(modelName, msg.Incomplete, timeLabel, ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}

	var bodyLines []string
	var bodyNative []PaintLine
	if ctx.streaming && ctx.isLastMsg {
		updateStreamingCache(&msg, ctx.mdRenderer, ctx.contentWidth)
		if len(msg.StreamCompletedLines) > 0 {
			native := ctx.streamNative
			if len(native) != len(msg.StreamCompletedLines) {
				native = BridgeAnsiLines(msg.StreamCompletedLines, ctx.tokens)
			}
			for _, pl := range native {
				bodyLines = append(bodyLines, pl.PlainText())
				bodyNative = append(bodyNative, pl)
			}
		}
		lastNL := strings.LastIndex(msg.Content, "\n")
		var lastLineText string
		if lastNL >= 0 {
			lastLineText = msg.Content[lastNL+1:]
		} else {
			lastLineText = msg.Content
		}
		lastLineText = strings.TrimRight(lastLineText, "\n")
		if lastLineText != "" {
			wrapped := rendering.WrapText(lastLineText, ctx.contentWidth)
			for _, line := range wrapped {
				pl := buildPlainBodyLine(line)
				bodyLines = append(bodyLines, line)
				bodyNative = append(bodyNative, pl)
			}
		}
	} else {
		rendered, isMarkdown := renderMarkdown(msg.Content, ctx.mdRenderer)
		if isMarkdown {
			bodyNative = BridgeAnsiLines(strings.Split(rendered, "\n"), ctx.tokens)
			bodyLines = make([]string, len(bodyNative))
			for index, pl := range bodyNative {
				bodyLines[index] = pl.PlainText()
			}
		} else {
			wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
			bodyNative = buildPlainBodyLines(wrapped)
			bodyLines = wrapped
		}
	}

	if len(bodyLines) == 0 {
		bodyLines = []string{""}
		bodyNative = []PaintLine{buildPlainBodyLine("")}
	}
	ml.lines = append(ml.lines, bodyLines...)
	ml.native = append(ml.native, bodyNative...)
	return ml
}

func renderThinkingMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	if msg.ThinkingDisplay.Kind.HasCompactPreview() {
		return renderThinkingSummary(msg, ctx)
	}
	var ml messageLines
	ml.frameSensitive = msg.ThinkingActive
	if ctx.thinkingBodyOnly {
		wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
		for _, line := range wrapped {
			pl := buildThinkingBodyLine(line, false, ctx.spinFrame, msg.ThinkingActive, msg.ThinkingInterrupted)
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		return ml
	}
	completed := !msg.ThinkingActive && !msg.ThinkingInterrupted

	if completed {
		// Completed thinking always renders its own summary row; that row is
		// the disclosure affordance (▸ collapsed, ▾ expanded). The body shows
		// only when expanded.
		label := "Thought"
		role := ui.ColorMuted
		if msg.ThinkingInterrupted {
			label = "Thought interrupted"
			role = ui.ColorWarning
		} else if msg.ThinkingDuration > 0 {
			label = "Thought for " + rendering.FormatThinkingDuration(msg.ThinkingDuration)
		}
		chevron := " ▸"
		if ctx.disclosure == DisclosureExpanded {
			chevron = " ▾"
		}
		header := PaintLine{Spans: []RowSpan{
			{Text: "│ ", Role: ui.ColorMuted},
			{Text: "● ", Role: role},
			{Text: label, Role: ui.ColorMuted},
			{Text: chevron, Role: ui.ColorMuted},
		}}
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
		if ctx.disclosure != DisclosureExpanded {
			return ml
		}
		wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
		for _, line := range wrapped {
			pl := buildThinkingBodyLine(line, false, ctx.spinFrame, false, msg.ThinkingInterrupted)
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		return ml
	}

	label := "Thinking"
	dot := ui.ActivityDot(ctx.spinFrame) + " "
	role := ui.ColorAccent
	if msg.ThinkingInterrupted {
		label = "Thought interrupted"
		dot = "● "
		role = ui.ColorWarning
	} else if !msg.Time.IsZero() {
		elapsed := time.Since(msg.Time)
		if elapsed < 0 {
			elapsed = 0
		}
		duration := rendering.FormatThinkingDuration(elapsed)
		if duration == "" {
			duration = "<1s"
		}
		label += " " + duration
	}
	header := PaintLine{Spans: []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: dot, Role: role},
		{Text: label, Role: ui.ColorAccent, Attr: cell.AttrBold},
	}}
	ml.lines = append(ml.lines, header.PlainText())
	ml.native = append(ml.native, header)

	wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
	for _, line := range wrapped {
		pl := buildThinkingBodyLine(line, false, ctx.spinFrame, msg.ThinkingActive, msg.ThinkingInterrupted)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}

	return ml
}

func renderThinkingSummary(msg types.ChatMessage, ctx renderContext) messageLines {
	label, chevron := "Thought", " ▸ "
	if msg.ThinkingActive {
		label = "Thinking"
	} else if msg.ThinkingInterrupted {
		label = "Thought interrupted"
	}
	if ctx.disclosure == DisclosureExpanded {
		chevron = " ▾ "
	}
	header := PaintLine{Spans: []RowSpan{{Text: label + chevron, Role: ui.ColorMuted}}}
	if msg.ThinkingActive && ctx.disclosure != DisclosureExpanded {
		// The live preview belongs to the fixed strip. Keep one transcript
		// disclosure affordance, not a duplicate animated copy of the thought.
		header.Spans[0].Text = strings.TrimRight(header.Spans[0].Text, " ")
		return messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header}}
	}
	md := ctx.mdRenderer
	if md == nil {
		// Unlike block chat, compact summaries always need Markdown parsing.
		// Compose at the shared readable minimum, then bound native spans below.
		md = rendering.MakeMdRenderer(40, ctx.th)
	}
	if ctx.disclosure != DisclosureExpanded {
		preview := ThinkingPreviewLine(msg.Content, msg.ThinkingDisplay, md, ctx.tokens)
		header.Spans = append(header.Spans, preview.Spans...)
		if header.VisibleWidth() > ctx.contentWidth {
			header = TruncatePaintLine(header, max(0, ctx.contentWidth-1))
			header.Spans = append(header.Spans, RowSpan{Text: "…", Role: ui.ColorMuted})
		}
		return messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header}}
	}
	rendered, markdown := renderMarkdown(msg.Content, md)
	if !markdown {
		rendered = msg.Content
	}
	var rows []PaintLine
	for _, pl := range BridgeAnsiLines(strings.Split(rendered, "\n"), ctx.tokens) {
		rows = append(rows, wrapPaintLine(pl, ctx.contentWidth)...)
	}
	native := append([]PaintLine{header}, rows...)
	lines := make([]string, len(native))
	for i, line := range native {
		lines[i] = line.PlainText()
	}
	return messageLines{lines: lines, native: native}
}

// ThinkingPreviewLine paints the latest semantic block/paragraph of live
// reasoning. It is shared by compact summary rows and the fixed activity strip;
// the complete canonical body is never truncated by this presentation.
func ThinkingPreviewLine(content string, display reasoning.Display, md *glamour.TermRenderer, tokens *ui.TokenSet) PaintLine {
	body := thinkingPreviewBody(content, display)
	rendered, ok := renderMarkdown(body, md)
	if !ok {
		rendered = body
	}
	line := PaintLine{}
	for _, pl := range BridgeAnsiLines(strings.Split(rendered, "\n"), tokens) {
		for len(pl.Spans) > 0 {
			last := &pl.Spans[len(pl.Spans)-1]
			last.Text = strings.TrimRight(last.Text, " \t")
			if last.Text != "" {
				break
			}
			pl.Spans = pl.Spans[:len(pl.Spans)-1]
		}
		if len(pl.Spans) == 0 {
			continue
		}
		pl.Spans[0].Text = strings.TrimLeft(pl.Spans[0].Text, " \t")
		if len(line.Spans) > 0 {
			line.Spans = append(line.Spans, RowSpan{Text: " ", Role: ui.ColorMuted})
		}
		line.Spans = append(line.Spans, pl.Spans...)
	}
	return line
}

func thinkingPreviewBody(content string, display reasoning.Display) string {
	content = content[min(max(0, display.Start), len(content)):]
	// Raw traces can be long. Parse only a bounded suffix of the current block.
	if len(content) > 2048 {
		start := len(content) - 2048
		for start < len(content) && !utf8.RuneStart(content[start]) {
			start++
		}
		content = content[start:]
	}
	content = strings.TrimSpace(content)
	if split := strings.LastIndex(content, "\n\n"); split >= 0 {
		return strings.TrimSpace(content[split+2:])
	}
	if split := strings.LastIndex(content, "\n"); split >= 0 {
		return strings.TrimSpace(content[split+1:])
	}
	return content
}

func renderErrorMessage(msg types.ChatMessage, ctx renderContext) messageLines {
	return renderNoticeMessage(msg, ctx, "Turn failed", ui.ColorError)
}

func renderNoticeMessage(msg types.ChatMessage, ctx renderContext, title string, role ui.SemanticColor) messageLines {
	if ctx.innerW <= 0 {
		return messageLines{}
	}
	header := PaintLine{Spans: []RowSpan{{Text: rendering.TruncateVisual(title, ctx.innerW, ""), Role: role, Attr: cell.AttrBold}}}
	header = padHeaderWithTime(header, rendering.FormatTimeLabel(msg.Time), ctx.innerW)
	ml := messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header}}
	width := ctx.innerW
	if ctx.contentWidth > 0 {
		width = min(width, ctx.contentWidth)
	}
	for _, paragraph := range strings.Split(msg.Content, "\n") {
		for _, line := range rendering.WordWrap(paragraph, width) {
			body := buildPlainBodyLine(line)
			ml.lines = append(ml.lines, body.PlainText())
			ml.native = append(ml.native, body)
		}
	}
	return ml
}

func renderLifecycleMessage(msg types.ChatMessage, ctx renderContext) messageLines {
	switch msg.LifecyclePhase {
	case "yolo_enabled":
		return renderNoticeMessage(msg, ctx, "YOLO enabled", ui.ColorWarning)
	case "yolo_disabled":
		return renderNoticeMessage(msg, ctx, "YOLO disabled", ui.ColorSuccess)
	}
	if msg.LifecyclePhase == "history_restored" {
		return renderHistoryRestoreMessage(msg, ctx)
	}
	if strings.HasPrefix(msg.LifecyclePhase, "compaction_") {
		return renderCompactionMessage(msg, ctx)
	}
	label := msg.Content
	role := ui.ColorAccent2
	marker := "●"
	active := false
	switch msg.LifecyclePhase {
	case "preparing":
		marker = ui.ActivityDot(ctx.spinFrame)
		active = true
		label = "Getting next turn ready · ↑ "
		if msg.LifecycleTotalBytes > 0 {
			label += fmt.Sprintf("%s / %s · %d%%",
				rendering.FormatBytes(msg.LifecycleUploadedBytes),
				rendering.FormatBytes(msg.LifecycleTotalBytes),
				max(0, min(100, msg.LifecycleProgress)),
			)
		} else {
			label += fmt.Sprintf("%d%%", max(0, min(100, msg.LifecycleProgress)))
		}
		role = ui.ColorWarning
	case "waiting_provider":
		marker = ui.ActivityDot(ctx.spinFrame)
		active = true
		providerName := strings.TrimSpace(msg.LifecycleProvider)
		if providerName == "" {
			providerName = "provider"
		}
		label = "Waiting for " + providerName
		if !msg.Time.IsZero() {
			elapsed := time.Since(msg.Time)
			if elapsed >= time.Second {
				label += fmt.Sprintf(" · %ds", int(elapsed.Seconds()))
			}
		}
		role = ui.ColorAccent
	case "compacting":
		marker = ui.ActivityDot(ctx.spinFrame)
		active = true
		label = "Compacting conversation"
		if msg.LifecycleBeforeTokens > 0 {
			label += " · " + rendering.FormatTokens(msg.LifecycleBeforeTokens)
		}
		if !msg.Time.IsZero() {
			if elapsed := time.Since(msg.Time); elapsed >= time.Second {
				label += fmt.Sprintf(" · %ds", int(elapsed.Seconds()))
			}
		}
		label += " · esc to cancel"
		role = ui.ColorAccent
	case "compacted":
		label = "Context compacted"
		if msg.LifecycleBeforeTokens > 0 && msg.LifecycleAfterTokens > 0 {
			label += " · " + rendering.FormatTokens(msg.LifecycleBeforeTokens) + " → ~" + rendering.FormatTokens(msg.LifecycleAfterTokens)
		}
		role = ui.ColorSuccess
		marker = "✓"
		active = false
	case "compaction_failed":
		label = "Compaction failed · original context retained"
		role = ui.ColorError
		marker = "✕"
		active = false
	case "cancelling":
		marker = ui.ActivityDot(ctx.spinFrame)
		active = true
		label = "Stopping turn"
		role = ui.ColorWarning
	case "stopped":
		label = "Stopped by you · completed work is kept · send a message to continue"
		marker = "■"
		role = ui.ColorMuted
	case "retrying":
		marker = ui.ActivityDot(ctx.spinFrame)
		active = true
		role = ui.ColorWarning
		label = msg.Content
		if label == "" {
			label = "Connection interrupted"
		}
		remaining := max(0, int(msg.LifecycleRetryAt.Sub(time.UnixMilli(int64(ctx.spinFrame))).Seconds()+0.999))
		if remaining > 0 {
			label += fmt.Sprintf(" · retry %d/%d in %ds", msg.LifecycleRetryAttempt, msg.LifecycleRetryLimit, remaining)
		} else {
			label += fmt.Sprintf(" · retrying %d/%d", msg.LifecycleRetryAttempt, msg.LifecycleRetryLimit)
		}
		label += " · Esc to stop"
	}
	out := messageLines{frameSensitive: active}
	width := ctx.innerW
	if width <= 0 {
		width = rendering.VisibleWidth(label) + 4
	}
	for i, text := range rendering.WordWrap(label, max(1, width-4)) {
		prefix := []RowSpan{{Text: "│   ", Role: ui.ColorMuted}}
		if i == 0 {
			prefix = []RowSpan{{Text: "│ ", Role: ui.ColorMuted}, {Text: marker + " ", Role: role}}
		}
		line := PaintLine{Spans: append(prefix, RowSpan{Text: text, Role: ui.ColorMuted})}
		out.lines = append(out.lines, line.PlainText())
		out.native = append(out.native, line)
	}
	return out
}

func renderHistoryRestoreMessage(msg types.ChatMessage, ctx renderContext) messageLines {
	header := buildAssistantHeader("Local History", false, rendering.FormatTimeLabel(msg.Time), ctx.innerW, false)
	count := msg.HistoryPathCount
	files := fmt.Sprintf("%d files restored", count)
	if count == 1 {
		files = "1 file restored"
	}
	rows := []PaintLine{
		{},
		header,
		lifecycleBoundaryLine(ctx.innerW, "✓ Workspace restored", "complete", ui.ColorSuccess),
		lifecycleBoundaryLine(ctx.innerW, files, "agent-scoped", ui.ColorFg),
		lifecycleBoundaryLine(ctx.innerW, "Target "+shortHistoryID(msg.HistoryTargetSnapshot), "operation "+shortHistoryID(msg.LifecycleOperationID), ui.ColorMuted),
		lifecycleBoundaryLine(ctx.innerW, "Safety "+shortHistoryID(msg.HistorySafetySnapshot), "undo available", ui.ColorMuted),
		lifecycleBoundaryLine(ctx.innerW, "Conversation retained", "context updated", ui.ColorMuted),
		{},
	}
	lines := make([]string, len(rows))
	for index := range rows {
		lines[index] = rows[index].PlainText()
	}
	return messageLines{lines: lines, native: rows}
}

func shortHistoryID(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	if value == "" {
		return "unavailable"
	}
	return value
}

func lifecycleMessageFrameSensitive(msg types.ChatMessage) bool {
	switch msg.LifecyclePhase {
	case "preparing", "waiting_provider", "compacting", "cancelling",
		"compaction_extracting", "compaction_retrying", "compaction_committing", "compaction_cancelling":
		return true
	default:
		return false
	}
}

func renderCompactionMessage(msg types.ChatMessage, ctx renderContext) messageLines {
	active := true
	state, marker, stateRole := "Compaction", ui.ActivityDot(ctx.spinFrame), ui.ColorAccent2
	stateMeta := "active"
	stage := strings.TrimSpace(msg.LifecycleStageName)
	if stage == "" {
		stage = "Preparing"
	}
	switch msg.LifecyclePhase {
	case "compaction_retrying":
		stateMeta = "recovering"
	case "compaction_committing":
		stage = "Saving checkpoint"
		stateMeta = "committing"
	case "compaction_cancelling":
		stage = "Stopping safely"
		stateMeta = "cancelling"
		stateRole = ui.ColorWarning
	case "compaction_completed":
		active = false
		state, marker, stateRole, stateMeta = "Compacted", "✓", ui.ColorSuccess, "complete"
	case "compaction_cancelled":
		active = false
		state, marker, stateRole, stateMeta = "Cancelled", "■", ui.ColorMuted, "stopped"
	case "compaction_failed":
		active = false
		state, marker, stateRole, stateMeta = "Failed", "✕", ui.ColorError, "stopped"
	case "compaction_interrupted":
		active = false
		state, marker, stateRole, stateMeta = "Interrupted", "⚠", ui.ColorWarning, "restart"
	}
	if msg.LifecycleTrigger != "" {
		stateMeta += " · " + msg.LifecycleTrigger
	}

	stageCount := msg.LifecycleStageCount
	if stageCount <= 0 {
		stageCount = 4
	}
	completed := max(0, min(stageCount, msg.LifecycleCompleted))
	bar := compactionProgressBar(completed, stageCount, active && msg.LifecycleStageIndex > 0 && msg.LifecyclePhase != "compaction_committing")
	progress := fmt.Sprintf("%s  %d / %d", bar, completed, stageCount)

	elapsed := ""
	if active && !msg.Time.IsZero() {
		if duration := time.Since(msg.Time); duration >= time.Second {
			elapsed = fmt.Sprintf("%ds", int(duration.Seconds()))
		}
	}
	modelName := msg.ModelID
	if modelName == "" {
		modelName = ctx.legacyModelFallback
	}
	if modelName == "" {
		modelName = "Aeron"
	}
	header := buildAssistantHeader(modelName, false, rendering.FormatTimeLabel(msg.Time), ctx.innerW, false)
	rows := []PaintLine{{}, header}
	rows = append(rows, lifecycleBoundaryLine(ctx.innerW, marker+" "+state, stateMeta, stateRole))
	if active {
		stagePosition := msg.LifecycleStageIndex
		if stagePosition == 0 {
			stagePosition = completed
		}
		rows = append(rows, lifecycleBoundaryLine(ctx.innerW, stage, fmt.Sprintf("%s  %d / %d", bar, stagePosition, stageCount), ui.ColorFg))
		trailer := "esc cancel"
		if elapsed != "" {
			trailer = elapsed + " · " + trailer
		}
		if msg.LifecyclePhase == "compaction_retrying" {
			retryLimit := msg.LifecycleRetryLimit
			if retryLimit <= 0 {
				retryLimit = 3
			}
			trailer = fmt.Sprintf("retry %d / %d · %s", msg.LifecycleRetryAttempt, retryLimit, trailer)
		}
		source := "Calculating"
		if msg.LifecycleBeforeTokens > 0 {
			source = rendering.FormatTokens(msg.LifecycleBeforeTokens) + " source · full history retained"
		}
		rows = append(rows, lifecycleBoundaryLine(ctx.innerW, source, trailer, ui.ColorMuted))
	} else if msg.LifecyclePhase == "compaction_completed" {
		tokens := rendering.FormatTokens(msg.LifecycleBeforeTokens)
		if msg.LifecycleAfterTokens > 0 {
			tokens += " → ~" + rendering.FormatTokens(msg.LifecycleAfterTokens)
		}
		rows = append(rows, lifecycleBoundaryLine(ctx.innerW, "All extraction stages", progress, ui.ColorFg))
		rows = append(rows, lifecycleBoundaryLine(ctx.innerW, tokens+" · full transcript retained", "checkpoint active", ui.ColorMuted))
	} else {
		rows = append(rows, lifecycleBoundaryLine(ctx.innerW, stage, progress, ui.ColorFg))
		detail := "Stopped before context commit"
		switch msg.LifecyclePhase {
		case "compaction_failed":
			if strings.TrimSpace(msg.Content) != "" {
				detail = msg.Content
			} else {
				detail = "Recovery attempts exhausted"
			}
		case "compaction_interrupted":
			detail = "App stopped before atomic commit"
		}
		rows = append(rows, lifecycleBoundaryLine(ctx.innerW, detail, "original context retained", ui.ColorMuted))
	}
	rows = append(rows, PaintLine{})
	lines := make([]string, len(rows))
	for i := range rows {
		lines[i] = rows[i].PlainText()
	}
	return messageLines{lines: lines, native: rows, frameSensitive: active}
}

func lifecycleBoundaryLine(width int, value, trailer string, valueRole ui.SemanticColor) PaintLine {
	if width < 1 {
		return PaintLine{}
	}
	trailerW := rendering.VisibleWidth(trailer)
	if trailer != "" && trailerW+ui.TableMinGap+4 > width {
		return PaintLine{Spans: []RowSpan{{Text: rendering.TruncateVisual(value, width, "…"), Role: valueRole}}}
	}
	valueW := width
	if trailer != "" {
		valueW -= trailerW + ui.TableMinGap
	}
	value = rendering.TruncateVisual(value, max(1, valueW), "…")
	used := rendering.VisibleWidth(value)
	gap := 0
	if trailer != "" {
		gap = max(ui.TableMinGap, width-used-trailerW)
	}
	spans := []RowSpan{{Text: value, Role: valueRole}}
	if trailer != "" && used+gap+trailerW <= width {
		spans = append(spans, RowSpan{Text: strings.Repeat(" ", gap) + trailer, Role: ui.ColorMuted})
	}
	return PaintLine{Spans: spans}
}

func compactionProgressBar(completed, total int, current bool) string {
	if total <= 0 {
		return ""
	}
	parts := make([]string, total)
	for index := range parts {
		switch {
		case index < completed:
			parts[index] = "■"
		case current && index == completed:
			parts[index] = "●"
		default:
			parts[index] = "□"
		}
	}
	return strings.Join(parts, " ")
}

func renderMessageContent(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	switch msg.Role {
	case types.RoleUser:
		return renderUserMessage(msg, ctx, suppressHeader)
	case types.RoleAssistant:
		return renderAssistantMessage(msg, ctx, suppressHeader)
	case types.RoleThinking:
		return renderThinkingMessage(msg, ctx, suppressHeader)
	case types.RoleTool:
		if ctx.planSuperseded && ctx.disclosure != DisclosureExpanded {
			msg.Content = ""
		}
		ml := renderToolMessageLines(msg, ctx.th, ctx.contentWidth, ctx.innerW, ctx.width, ctx.spinFrame, ctx.chatHeight, ctx.phase, ctx.disclosure)
		return ml
	case types.RoleError:
		return renderErrorMessage(msg, ctx)
	case types.RoleStatus:
		return renderLifecycleMessage(msg, ctx)
	case types.RoleAssignment:
		return renderAssignmentMessage(msg, ctx, suppressHeader)
	default:
		return messageLines{lines: []string{""}, native: []PaintLine{buildPlainBodyLine("")}}
	}
}
