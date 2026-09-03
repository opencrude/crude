package chatprojection

import (
	"fmt"
	"strings"
	"time"

	"charm.land/glamour/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
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
	th           *theme.Theme
	tokens       *ui.TokenSet
	contentWidth int
	innerW       int
	width        int
	height       int
	chatHeight   int
	spinFrame    int
	activeTyping bool
	modelName    string
	username     string
	streaming    bool
	isLastMsg    bool
	phase        string
	mdRenderer   *glamour.TermRenderer
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
		bodyLines := strings.Split(rendered, "\n")
		for _, line := range bodyLines {
			pl := BridgeAnsiLine(line, ctx.tokens)
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
		for _, line := range strings.Split(rendered, "\n") {
			pl := BridgeAnsiLine(line, ctx.tokens)
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
	header := buildAssistantHeader(ctx.modelName, msg.Incomplete, timeLabel, ctx.innerW, suppressHeader)
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
			for i, line := range msg.StreamCompletedLines {
				var pl PaintLine
				if i < len(native) {
					pl = native[i]
				} else {
					pl = BridgeAnsiLine(line, ctx.tokens)
				}
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
			split := strings.Split(rendered, "\n")
			for _, line := range split {
				pl := BridgeAnsiLine(line, ctx.tokens)
				bodyLines = append(bodyLines, pl.PlainText())
				bodyNative = append(bodyNative, pl)
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
	if msg.Incomplete {
		strikeBody(bodyNative)
	}
	ml.lines = append(ml.lines, bodyLines...)
	ml.native = append(ml.native, bodyNative...)
	return ml
}

func renderThinkingMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	ml.frameSensitive = msg.ThinkingActive
	if ctx.thinkingBodyOnly {
		wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
		bodyStart := len(ml.native)
		for _, line := range wrapped {
			pl := buildThinkingBodyLine(line, false, ctx.spinFrame, msg.ThinkingActive, msg.ThinkingInterrupted)
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		if msg.Incomplete || msg.ThinkingInterrupted {
			strikeBody(ml.native[bodyStart:])
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
		bodyStart := len(ml.native)
		for _, line := range wrapped {
			pl := buildThinkingBodyLine(line, false, ctx.spinFrame, false, msg.ThinkingInterrupted)
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
		if msg.ThinkingInterrupted {
			strikeBody(ml.native[bodyStart:])
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
	bodyStart := len(ml.native)
	for _, line := range wrapped {
		pl := buildThinkingBodyLine(line, false, ctx.spinFrame, msg.ThinkingActive, msg.ThinkingInterrupted)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}

	if msg.Incomplete || msg.ThinkingInterrupted {
		strikeBody(ml.native[bodyStart:])
	}
	return ml
}

func renderErrorMessage(msg types.ChatMessage, ctx renderContext) messageLines {
	var ml messageLines
	timeLabel := rendering.FormatTimeLabel(msg.Time)
	// Wrap at contentWidth-6 so the "error " first-line prefix (6 cols) and the
	// matching 6-col continuation indent keep every visual row within
	// contentWidth. This prevents continuation lines from overrunning the width
	// and producing ragged, off-screen wraps.
	wrapped := rendering.WrapText(msg.Content, ctx.contentWidth-6)
	for i, line := range wrapped {
		pl := buildErrorHeaderLine(line, timeLabel, ctx.innerW, i == 0)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
	return ml
}

func renderLifecycleMessage(msg types.ChatMessage, spinFrame int) messageLines {
	label := msg.Content
	role := ui.ColorAccent2
	switch msg.LifecyclePhase {
	case "preparing":
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
	case "cancelling":
		label = "Stopping turn"
		role = ui.ColorWarning
	}
	line := PaintLine{Spans: []RowSpan{
		{Text: "│ ", Role: ui.ColorMuted},
		{Text: ui.ActivityDot(spinFrame) + " ", Role: role},
		{Text: label, Role: ui.ColorMuted},
	}}
	return messageLines{lines: []string{line.PlainText()}, native: []PaintLine{line}, frameSensitive: true}
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
		return renderToolMessageLines(msg, ctx.th, ctx.contentWidth, ctx.innerW, ctx.width, ctx.spinFrame, ctx.chatHeight, ctx.phase, ctx.disclosure)
	case types.RoleError:
		return renderErrorMessage(msg, ctx)
	case types.RoleStatus:
		return renderLifecycleMessage(msg, ctx.spinFrame)
	case types.RoleAssignment:
		return renderAssignmentMessage(msg, ctx, suppressHeader)
	default:
		return messageLines{lines: []string{""}, native: []PaintLine{buildPlainBodyLine("")}}
	}
}
