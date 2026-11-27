package chatprojection

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types/conversation"
	"crude/types/reasoning"
)

// messageLines is one render pass producing parallel plain-text lines and
// native rows; lines is the plain-text mirror for selection/copy.
type messageLines struct {
	lines          []string
	native         []PaintLine
	frameSensitive bool
	syntaxPending  bool
}

// withCursor places the streaming caret right after the last streamed text,
// like a typing caret after the last character. Rendered rows are padded to
// the block width and may end in chrome (an open code fence's Copy row, a
// blank spacer). The caret takes the first padding cell after the last source
// span of the last row with source text; appending it after the padding left
// it at the far edge of the row. Rows without source text keep the old place.
func (ml messageLines) withCursor(cursorIdx, spinFrame int, activeTyping bool) messageLines {
	if cursorIdx < 0 || cursorIdx >= len(ml.lines) {
		return ml
	}
	row, at := cursorIdx, len(ml.native[cursorIdx].Spans)
	for i := cursorIdx; i >= 0 && ml.native[i].SourceAware; i-- {
		if end := sourceTextEnd(ml.native[i]); end > 0 {
			row, at = i, end
			break
		}
	}
	out := ml
	out.frameSensitive = out.frameSensitive || !activeTyping
	out.native = append([]PaintLine(nil), ml.native...)
	out.native[row] = insertBlockCursor(ml.native[row], at, spinFrame, activeTyping)
	out.lines = append([]string(nil), ml.lines...)
	out.lines[row] = out.native[row].PlainText()
	return out
}

// sourceTextEnd returns the span index after the last span that shows source
// text, or 0 when the row has none.
func sourceTextEnd(pl PaintLine) int {
	for i := len(pl.Spans) - 1; i >= 0; i-- {
		if span := pl.Spans[i]; span.Copy.Valid() && strings.TrimSpace(span.Text) != "" {
			return i + 1
		}
	}
	return 0
}

type renderContext struct {
	sourceMessage *conversation.ChatMessage // Canonical source; Content below is a paint-safe projection.
	th            *theme.Theme
	tokens        *ui.TokenSet
	contentWidth  int
	innerW        int
	width         int
	height        int
	chatHeight    int
	spinFrame     int
	activeTyping  bool
	username      string
	streaming     bool
	isLastMsg     bool
	phase         string
	mdRenderer    *rendering.MarkdownRenderer
	projectPaths  *tools.ProjectPaths
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
	streamBody     *streamBodyCache
	markdownReflow *markdownReflowCache
}

func (c renderContext) markdownContent(message conversation.ChatMessage) string {
	if c.sourceMessage != nil {
		return c.sourceMessage.Content
	}
	return message.Content
}

func renderUserMessage(msg conversation.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	referenceLabels := userReferenceLabels(msg.Content, msg.ReferenceLabels)
	header := buildUserHeader(ctx.username, rendering.FormatTimeLabel(msg.Time), ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}

	rawMessage := msg
	rawMessage.Content = ctx.markdownContent(msg)
	source, markers := attachmentSource(rawMessage)
	offsets := make(map[rune]attachmentCopyOffset)
	mapped, isMarkdown := ctx.markdownReflow.render(source, msg.InternalID, ctx.mdRenderer, ctx.contentWidth, ctx.tokens, false)
	if isMarkdown {
		for _, pl := range mapped {
			if len(markers) == 0 {
				pl = styleReferenceLabels(pl, referenceLabels)
			}
			pl = restoreAttachmentSpans(pl, msg.InternalID, markers, offsets)
			for _, row := range wrapPaintLine(pl, max(1, ctx.contentWidth)) {
				ml.lines = append(ml.lines, row.PlainText())
				ml.native = append(ml.native, row)
			}
		}
	} else {
		wrapped := rendering.WrapText(source, ctx.contentWidth)
		body := buildPlainBodyLines(wrapped)
		for _, pl := range body {
			if len(markers) == 0 {
				pl = styleReferenceLabels(pl, referenceLabels)
			}
			pl = restoreAttachmentSpans(pl, msg.InternalID, markers, offsets)
			for _, row := range wrapPaintLine(pl, max(1, ctx.contentWidth)) {
				ml.lines = append(ml.lines, row.PlainText())
				ml.native = append(ml.native, row)
			}
		}
	}
	if msg.PendingSteering {
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

const collaborationRail = "│ "

func renderCollaborationMessage(msg conversation.ChatMessage, ctx renderContext) messageLines {
	if isCommandOutcome(&msg) {
		return renderCommandOutcome(msg, ctx)
	}
	var ml messageLines
	attribution := msg.Collaboration
	if attribution == nil {
		return renderUserMessage(msg, ctx, false)
	}
	recipients := attribution.RecipientNames
	if len(recipients) == 0 {
		recipients = attribution.RecipientIDs
	}
	headerText := attribution.SenderName
	if headerText == "" {
		headerText = attribution.SenderID
	}
	if attribution.Broadcast {
		headerText += fmt.Sprintf(" → %d running agents", len(recipients))
	} else if len(recipients) > 0 {
		headerText += " → " + strings.Join(recipients, ", ")
	}
	label := collaborationMessageLabel(attribution.Kind)
	if attribution.Broadcast {
		label = "Broadcast"
	}
	headerText += " · " + label
	if label := rendering.FormatTimeLabel(msg.Time); label != "" {
		headerText += " · " + label
	}
	headerText = rendering.TruncateVisual(headerText, ctx.innerW, "")
	header := PaintLine{Spans: []RowSpan{{Text: headerText, Role: ui.ColorAccent, Attr: cell.AttrBold}}}
	ml.lines = append(ml.lines, header.PlainText())
	ml.native = append(ml.native, header)
	ml.appendCollaborationBody(ctx.markdownContent(msg), msg.InternalID, ctx)
	return ml
}

// appendCollaborationBody paints a message between agents: Markdown under the
// collaboration rail. Received and sent messages share it.
func (ml *messageLines) appendCollaborationBody(source, id string, ctx renderContext) {
	mapped, isMarkdown := ctx.markdownReflow.render(source, id, ctx.mdRenderer, ctx.contentWidth, ctx.tokens, false)
	var body []PaintLine
	if isMarkdown {
		body = mapped
	} else {
		body = buildPlainBodyLines(rendering.WrapText(source, max(1, ctx.contentWidth)))
	}
	for _, line := range body {
		withRail := line
		shift := rendering.VisibleWidth(collaborationRail)
		withRail.prose = line.prose.shifted(shift)
		withRail.Spans = append([]RowSpan{{Text: collaborationRail, Role: ui.ColorMuted}}, line.Spans...)
		ml.lines = append(ml.lines, withRail.PlainText())
		ml.native = append(ml.native, withRail)
	}
}

// renderSentMessage paints a message this agent sent to other agents the way
// a received message reads: the tool title, then the message under the same
// rail. Expansion adds the delivery details, without the message again.
func renderSentMessage(msg conversation.ChatMessage, body string, ctx renderContext) messageLines {
	var ml messageLines
	for _, line := range buildProjectToolTitleLines(msg, conversation.ToolPreviewDone, ctx.spinFrame, ctx.innerW, ctx.disclosure, ctx.projectPaths) {
		ml.lines = append(ml.lines, line.PlainText())
		ml.native = append(ml.native, line)
	}
	ml.appendCollaborationBody(body, msg.InternalID, ctx)
	if ctx.disclosure == DisclosureExpanded {
		details := tools.BuildStructuredResult(msg.ToolName, msg.Content, msg.ToolInput)
		for i := range details.Sections {
			if strings.TrimSpace(details.Sections[i].Body) == body {
				details.Sections[i].Body = ""
			}
		}
		ml.appendStructuredToolBody(details, msg.ToolInput, ctx.innerW, ctx.chatHeight, true)
	}
	return ml
}

func collaborationMessageLabel(kind string) string {
	switch kind {
	case "work_request":
		return "Work request"
	case "work_revision":
		return "Work revision"
	case "request_disposition":
		return "Request response"
	case "report_published":
		return "Report"
	case "run_outcome":
		return "Run outcome"
	case "lifecycle_control":
		return "Control"
	case "control_outcome":
		return "Control outcome"
	case "peer_reply":
		return "Reply"
	default:
		return "Message"
	}
}

func renderAssignmentMessage(msg conversation.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	header := buildAssistantHeader("Main Agent Assignment", false, rendering.FormatTimeLabel(msg.Time), ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}
	mapped, isMarkdown := ctx.markdownReflow.render(ctx.markdownContent(msg), msg.InternalID, ctx.mdRenderer, ctx.contentWidth, ctx.tokens, false)
	if isMarkdown {
		for _, pl := range mapped {
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

func renderAssistantMessage(msg conversation.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	timeLabel := rendering.FormatTimeLabel(msg.Time)
	header := buildAssistantHeader(msg.ModelID, msg.Incomplete, timeLabel, ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}

	var bodyLines []string
	var bodyNative []PaintLine
	var isMarkdown bool
	if ctx.streaming && ctx.isLastMsg {
		cache := ctx.streamBody
		if cache == nil {
			cache = &streamBodyCache{}
		}
		head, tail := cache.project(ctx.markdownContent(msg), msg.InternalID, ctx.mdRenderer, ctx.tokens, ctx.contentWidth)
		bodyLines = append(bodyLines, head.lines...)
		bodyLines = append(bodyLines, tail.lines...)
		bodyNative = append(bodyNative, head.native...)
		bodyNative = append(bodyNative, tail.native...)
	} else {
		bodyNative, isMarkdown = ctx.markdownReflow.render(ctx.markdownContent(msg), msg.InternalID, ctx.mdRenderer, ctx.contentWidth, ctx.tokens, true)
		if !isMarkdown {
			bodyNative = plainSourceLines(presentPlan(msg.Content, false), ctx.contentWidth)
		}
		bodyLines = make([]string, len(bodyNative))
		for i, line := range bodyNative {
			bodyLines[i] = line.PlainText()
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

// Inline thinking promotion constants. Promotion is decided only from what the
// UI can observe: how many wrapped rows the reasoning block occupies and how fast
// those rows arrive. No provider field, declared reasoning kind or reasoning
// token count participates, because none of them reliably describe the block.
const (
	// inlineThinkingBodyRows is the inline body budget in wrapped rows. Past it
	// the inline transcript form is no longer a readable live one-liner.
	inlineThinkingBodyRows = 5
	// inlineThinkingBurstRowsPerSecond is the arrival rate that separates a model
	// emitting a thinking burst from a condensed gateway dripping occasional
	// summary headers. Real reasoning streams measure ~15 rows/s; a gateway
	// emitting a header every few seconds measures ~0.3 rows/s.
	inlineThinkingBurstRowsPerSecond = 2.0
)

// InlineThinkingBlock returns the reasoning text the inline transcript form is
// currently showing: content from the latest semantic block boundary. Promotion
// and the row measurement shared with the renderer both use it, so they cannot
// disagree about what the reader sees.
//
// A gateway that sends one unlabelled field records no block boundary, so Start
// stays zero and this is the whole trace; a decoder that does tag semantic blocks
// (the OpenAI Responses and OpenRouter contracts) lets each block be measured on
// its own.
func InlineThinkingBlock(content string, start int) string {
	if start < 0 {
		start = 0
	}
	if start > len(content) {
		start = len(content)
	}
	return content[start:]
}

// ThinkingOverflowsInline reports whether an active reasoning block has outgrown
// the inline transcript form. It is the one promotion rule for the main chat and
// for every sub-agent, and it is decided purely at the UI level.
//
// A block must both read as long (its current semantic block is past
// inlineThinkingBodyRows wrapped rows) and have arrived fast (at or above
// inlineThinkingBurstRowsPerSecond over the time that block took to arrive).
// Neither half alone is enough:
//
//   - Length alone eventually ejects every condensed gateway, because its many
//     short summary headers also add up over a long turn.
//   - Rate alone opens a pane for a couple of rows the inline form already
//     renders comfortably.
//
// No provider field name, declared reasoning kind or reported reasoning-token
// count participates: none of them reliably describe the block. Content with no
// measurable elapsed time (a synchronous or already-inflated delta) counts as
// a burst, since it was never observed trickling in.
func ThinkingOverflowsInline(content string, start int, started time.Time, contentWidth int) bool {
	block := InlineThinkingBlock(content, start)
	if strings.TrimSpace(block) == "" {
		return false
	}
	rows := float64(len(rendering.WrapText(block, max(1, contentWidth))))
	if rows <= inlineThinkingBodyRows {
		return false
	}
	if started.IsZero() {
		return true
	}
	span := time.Since(started)
	if span <= 0 {
		return true
	}
	return rows/span.Seconds() >= inlineThinkingBurstRowsPerSecond
}

// ThinkingOverflowWidth is the inline reasoning body width for a chat inner
// width. The promoter and the renderer both derive from this one helper, so the
// width the decision is made at is the width the body is painted at.
func ThinkingOverflowWidth(innerW int) int {
	return max(1, innerW-messageBodyChromeWidth)
}

// thinkingBody drops trailing line breaks. They painted as empty body rows,
// which left the live caret on a blank row below the last thought.
func thinkingBody(content string) string { return strings.TrimRight(content, "\r\n") }

func renderThinkingMessage(msg conversation.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	if msg.ThinkingDisplay.Kind.HasCompactPreview() {
		return renderThinkingSummary(msg, ctx)
	}
	var ml messageLines
	ml.frameSensitive = msg.ThinkingActive
	if ctx.thinkingBodyOnly {
		wrapped := rendering.WrapText(thinkingBody(msg.Content), ctx.contentWidth)
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
		wrapped := rendering.WrapText(thinkingBody(msg.Content), ctx.contentWidth)
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

	wrapped := rendering.WrapText(thinkingBody(msg.Content), ctx.contentWidth)
	for _, line := range wrapped {
		pl := buildThinkingBodyLine(line, false, ctx.spinFrame, msg.ThinkingActive, msg.ThinkingInterrupted)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}

	return ml
}

func renderThinkingSummary(msg conversation.ChatMessage, ctx renderContext) messageLines {
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
	if msg.ThinkingActive && ctx.disclosure != DisclosureExpanded && !ctx.thinkingBodyOnly {
		// The live preview belongs to the fixed strip. Keep one transcript
		// disclosure affordance, not a duplicate animated copy of the thought.
		header.Spans[0].Text = strings.TrimRight(header.Spans[0].Text, " ")
		return messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header}}
	}
	md := ctx.mdRenderer
	if md == nil {
		// Unlike block chat, compact summaries always need Markdown parsing.
		// Compose at the shared readable minimum, then bound native spans below.
		md = rendering.MakeMdRenderer(20, ctx.th)
	}
	if ctx.disclosure != DisclosureExpanded && !ctx.thinkingBodyOnly {
		preview := ThinkingPreviewLine(msg.Content, msg.ThinkingDisplay, md, ctx.tokens)
		header.Spans = append(header.Spans, preview.Spans...)
		if header.VisibleWidth() > ctx.contentWidth {
			header = TruncatePaintLine(header, max(0, ctx.contentWidth-1))
			header.Spans = append(header.Spans, RowSpan{Text: "…", Role: ui.ColorMuted})
		}
		return messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header}}
	}
	mapped, markdown := renderMarkdownRows(ctx.markdownContent(msg), md, ctx.tokens, msg.InternalID, false)
	if !markdown {
		mapped = buildPlainBodyLines(strings.Split(msg.Content, "\n"))
	}
	var rows []PaintLine
	for _, pl := range mapped {
		rows = append(rows, wrapPaintLine(pl, ctx.contentWidth)...)
	}
	native := rows
	if !ctx.thinkingBodyOnly {
		native = append([]PaintLine{header}, rows...)
	}
	lines := make([]string, len(native))
	for i, line := range native {
		lines[i] = line.PlainText()
	}
	return messageLines{lines: lines, native: native}
}

// ThinkingPreviewLine paints the latest semantic block/paragraph of live
// reasoning. It is shared by compact summary rows and the fixed activity strip;
// the complete canonical body is never truncated by this presentation.
func ThinkingPreviewLine(content string, display reasoning.Display, md *rendering.MarkdownRenderer, tokens *ui.TokenSet) PaintLine {
	body := thinkingPreviewBody(content, display)
	mapped, ok := renderMarkdownRows(body, md, tokens, "", false)
	if !ok {
		mapped = buildPlainBodyLines(strings.Split(body, "\n"))
	}
	line := PaintLine{}
	for _, pl := range mapped {
		for len(pl.Spans) > 0 {
			last := &pl.Spans[len(pl.Spans)-1]
			*last = last.Slice(0, len(strings.TrimRight(last.Text, " \t")))
			if last.Text != "" {
				break
			}
			pl.Spans = pl.Spans[:len(pl.Spans)-1]
		}
		if len(pl.Spans) == 0 {
			continue
		}
		pl.Spans[0] = pl.Spans[0].Slice(len(pl.Spans[0].Text)-len(strings.TrimLeft(pl.Spans[0].Text, " \t")), len(pl.Spans[0].Text))
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

// renderErrorMessage paints a turn error with the same card as a failed
// recovery incident: the reason in the heading, all evidence when expanded.
func renderErrorMessage(msg conversation.ChatMessage, ctx renderContext) messageLines {
	if ctx.innerW <= 0 {
		return messageLines{}
	}
	content := strings.TrimSpace(msg.Content)
	reason, _, _ := strings.Cut(content, "\n")
	header := failedTurnStyle.header(ctx, "Turn failed", tools.CollapseLine(reason), ui.ColorFg, nil)
	out := messageLines{lines: []string{header.PlainText()}, native: []PaintLine{header}}
	if ctx.disclosure == DisclosureExpanded {
		failedTurnStyle.appendBody(&out, ctx, content, failedTurnGuidance)
	}
	return out
}

func renderNoticeMessage(msg conversation.ChatMessage, ctx renderContext, title string, role ui.SemanticColor) messageLines {
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

func renderLifecycleMessage(msg conversation.ChatMessage, ctx renderContext) messageLines {
	if msg.Recovery != nil {
		return renderRecoveryMessage(msg, ctx)
	}
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
	case "compaction_failed":
		label = "Compaction failed · original context kept"
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

func renderHistoryRestoreMessage(msg conversation.ChatMessage, ctx renderContext) messageLines {
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

func lifecycleMessageFrameSensitive(msg conversation.ChatMessage) bool {
	switch msg.LifecyclePhase {
	case "preparing", "waiting_provider", "cancelling",
		"compaction_preparing", "compaction_extracting", "compaction_retrying", "compaction_committing", "compaction_cancelling":
		return true
	default:
		return false
	}
}

func settledCompaction(msg conversation.ChatMessage) bool {
	switch msg.LifecyclePhase {
	case "compaction_completed", "compaction_cancelled", "compaction_interrupted", "compaction_failed":
		return true
	}
	return false
}

// renderCompactionMessage paints the same tool row for live events and restored
// checkpoints. The recorded trigger changes the label, never the row geometry.
func renderCompactionMessage(msg conversation.ChatMessage, ctx renderContext) messageLines {
	title := tools.ToolTitle{Action: "Compacting context"}
	status, active, detail := conversation.ToolStatusStreaming, true, ""
	switch msg.LifecyclePhase {
	case "compaction_completed":
		title.Action, status, active = "Compacted context", conversation.ToolStatusSuccess, false
		title.Operand = compactionTokenSummary(msg)
		switch msg.LifecycleTrigger {
		case "background":
			detail = "prepared in background"
		case "decay":
			detail = "older tool output trimmed"
		case "loop_end":
			detail = "after the work finished"
		}
	case "compaction_cancelled":
		title.Action, detail = "Compaction cancelled", "original context kept"
		status, active = conversation.ToolStatusCancelled, false
	case "compaction_interrupted":
		title.Action, detail = "Compaction interrupted", "original context kept"
		status, active = "interrupted", false
	case "compaction_failed":
		title.Action, title.Failed = "Compaction failed", true
		status, active = conversation.ToolStatusError, false
		detail, _, _ = strings.Cut(strings.TrimSpace(msg.Content), "\n")
		if detail == "" {
			detail = "original context kept"
		}
	case "compaction_cancelling":
		title.Action = "Stopping compaction"
	case "compaction_retrying":
		detail = "retrying"
		if attempt := msg.LifecycleRetryAttempt; attempt > 0 {
			detail = fmt.Sprintf("retry %d", attempt)
			if limit := msg.LifecycleRetryLimit; limit > 0 {
				detail += fmt.Sprintf("/%d", limit)
			}
		}
	case "compaction_committing":
		detail = "saving checkpoint"
	default: // preparing, extracting
		if msg.LifecycleBeforeTokens > 0 {
			title.Operand = rendering.FormatTokens(msg.LifecycleBeforeTokens)
		}
		// The title names the writer; the detail says what it does now. The
		// model is in the expanded result, not on the live row.
		detail = strings.TrimSpace(msg.LifecycleStageName)
		if detail == "" && (msg.LifecycleProvider == "errand" || msg.LifecycleProvider == "native") {
			detail = "Preparing"
		}
	}
	var route []string
	switch msg.LifecycleTrigger {
	case "manual", "requested":
		route = append(route, "Manual")
	case "automatic", "background", "loop_end", "decay":
		route = append(route, "Auto")
	}
	if writer := compactionWriterLabel(msg); writer != "" {
		route = append(route, writer)
	}
	if len(route) > 0 {
		title.Action += " (" + strings.Join(route, " - ") + ")"
	}
	if detail != "" {
		title.Metadata = "· " + detail
	}
	out := messageLines{frameSensitive: active}
	header := paintToolTitle(title, status, active, !active, ctx.spinFrame, "")
	var suffix []RowSpan
	if !active && (msg.Content != "" || msg.LifecycleBeforeTokens > 0) {
		glyph := " ▸"
		if ctx.disclosure == DisclosureExpanded {
			glyph = " ▾"
		}
		suffix = []RowSpan{{Text: glyph, Role: ui.ColorMuted}}
	}
	var lines []PaintLine
	if active || ctx.disclosure == DisclosureExpanded {
		header.Spans = append(header.Spans, suffix...)
		lines = wrapToolTitle(header, ctx.innerW)
	} else {
		lines = wrapCompactToolTitle(header, ctx.innerW, suffix)
	}
	for _, line := range lines {
		out.lines = append(out.lines, line.PlainText())
		out.native = append(out.native, line)
	}
	if !active && ctx.disclosure == DisclosureExpanded {
		body := msg.Content
		if first, rest, _ := strings.Cut(strings.TrimSpace(body), "\n"); first == detail {
			body = rest
		}
		if msg.LifecycleBeforeTokens > 0 {
			body += fmt.Sprintf("\nSource: %d tokens\nKept: %d tokens", msg.LifecycleBeforeTokens, msg.LifecycleAfterTokens)
		}
		if msg.ModelID != "" {
			body += "\nModel: " + msg.ModelID
		}
		out.appendPlainToolBody("", strings.TrimSpace(body), ctx.innerW, ctx.height, false, true)
	}
	return out
}

// compactionWriterLabel names who writes the checkpoint: the provider's own
// compaction, Errand, or the agent's model by name. A checkpoint saved before
// writers were recorded names none.
func compactionWriterLabel(msg conversation.ChatMessage) string {
	switch msg.LifecycleProvider {
	case "native":
		return "Native"
	case "errand":
		return "Errand"
	case "model":
		if _, name, found := strings.Cut(msg.ModelID, "/"); found && name != "" {
			return name
		}
		if msg.ModelID != "" {
			return msg.ModelID
		}
		return "Model"
	}
	return ""
}

// compactionTokenSummary renders the token result of a completed compaction, or
// "" when the source size is unknown.
func compactionTokenSummary(msg conversation.ChatMessage) string {
	if msg.LifecycleBeforeTokens <= 0 {
		return ""
	}
	tokens := rendering.FormatTokens(msg.LifecycleBeforeTokens)
	if msg.LifecycleAfterTokens > 0 {
		tokens += " → ~" + rendering.FormatTokens(msg.LifecycleAfterTokens)
	}
	return tokens
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

func renderMessageContent(msg conversation.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	switch msg.Role {
	case conversation.RoleUser:
		if isCollaborationRow(&msg) {
			ml = renderCollaborationMessage(msg, ctx)
		} else {
			ml = renderUserMessage(msg, ctx, suppressHeader)
		}
	case conversation.RoleAssistant:
		ml = renderAssistantMessage(msg, ctx, suppressHeader)
	case conversation.RoleThinking:
		ml = renderThinkingMessage(msg, ctx, suppressHeader)
	case conversation.RoleTool:
		if ctx.planSuperseded && ctx.disclosure != DisclosureExpanded {
			msg.Content = ""
		}
		if msg.ToolStatus == conversation.ToolStatusSuccess {
			if body := tools.SentMessageBody(msg.ToolName, msg.Content); body != "" {
				ml = renderSentMessage(msg, body, ctx)
				break
			}
		}
		ml = renderProjectToolMessageLines(msg, ctx.th, ctx.contentWidth, ctx.innerW, ctx.width, ctx.spinFrame, ctx.chatHeight, ctx.phase, ctx.disclosure, ctx.projectPaths)
	case conversation.RoleError:
		ml = renderErrorMessage(msg, ctx)
	case conversation.RoleStatus:
		ml = renderLifecycleMessage(msg, ctx)
	case conversation.RoleAssignment:
		ml = renderAssignmentMessage(msg, ctx, suppressHeader)
	default:
		ml = messageLines{lines: []string{""}, native: []PaintLine{buildPlainBodyLine("")}}
	}
	if messageHasPaneBackground(&msg) {
		for i := range ml.native {
			ml.native[i].PaneBackground = true
		}
	}
	return ml
}

// A steering prompt retains journal provenance on replay, but the visible
// message is still the human-authored prompt, not a collaboration card.
func isCollaborationRow(msg *conversation.ChatMessage) bool {
	return msg.Collaboration != nil && !msg.IsHumanSteering()
}

func messageHasPaneBackground(msg *conversation.ChatMessage) bool {
	return msg.Role == conversation.RoleThinking || isCollaborationRow(msg) && !isCommandOutcome(msg) ||
		msg.Role == conversation.RoleTool && tools.IsCollaborationTool(msg.ToolName)
}
