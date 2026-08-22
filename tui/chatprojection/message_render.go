package chatprojection

import (
	"strings"

	"charm.land/glamour/v2"

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
	lines  []string
	native []PaintLine
}

func (ml messageLines) withCursor(cursorIdx, spinFrame int, activeTyping bool) messageLines {
	if cursorIdx < 0 || cursorIdx >= len(ml.lines) {
		return ml
	}
	out := ml
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
	spinFrame    int
	activeTyping bool
	modelName    string
	username     string
	streaming    bool
	isLastMsg    bool
	phase        string
	mdRenderer   *glamour.TermRenderer
	// streamNative holds the pre-bridged native rows for the completed portion
	// of a streaming assistant message (built once per StreamCompletedLen change
	// by the Renderer, never per frame). Set only for the streaming cursor.
	streamNative []PaintLine
}

func renderUserMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
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
			ml.lines = append(ml.lines, pl.PlainText())
			ml.native = append(ml.native, pl)
		}
	} else {
		wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
		body := buildPlainBodyLines(wrapped)
		for i, pl := range body {
			ml.lines = append(ml.lines, wrapped[i])
			ml.native = append(ml.native, pl)
		}
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
	ml.lines = append(ml.lines, bodyLines...)
	ml.native = append(ml.native, bodyNative...)

	if msg.Incomplete && msg.IncompleteReason != "" && len(ml.lines) > 0 {
		last := len(ml.lines) - 1
		ml.native[last] = appendIncompleteReason(ml.native[last], msg.IncompleteReason)
		ml.lines[last] = ml.native[last].PlainText()
	}
	return ml
}

func renderThinkingMessage(msg types.ChatMessage, ctx renderContext, suppressHeader bool) messageLines {
	var ml messageLines
	timeLabel := rendering.FormatTimeLabel(msg.Time)
	// Once a thinking block finishes, replace the clock label with how long
	// it ran ("thought for 12s"), matching the historical chat view.
	if !msg.ThinkingActive && !msg.ThinkingInterrupted && msg.ThinkingDuration > 0 {
		timeLabel = "thought for " + rendering.FormatThinkingDuration(msg.ThinkingDuration)
	}
	header, _ := buildThinkingHeader(ctx.modelName, msg.Incomplete, timeLabel, ctx.innerW, suppressHeader)
	if !header.IsZero() {
		ml.lines = append(ml.lines, header.PlainText())
		ml.native = append(ml.native, header)
	}

	wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
	// Only insert the header↔body gap when there is a body to separate. When
	// the thinking body is empty (a completed block collapsed to its label, or
	// a wrap that yields a single blank), adding a blank here stacks with the
	// following message's own separator and produces two blank rows between the
	// thinking header and the next block. Exactly one row separates blocks.
	if hasThinkingBody(wrapped) {
		ml.lines = append(ml.lines, "")
		ml.native = append(ml.native, buildPlainBodyLine(""))
	}
	for i, line := range wrapped {
		pl := buildThinkingBodyLine(line, i == 0, ctx.spinFrame, msg.ThinkingActive, msg.ThinkingInterrupted)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}

	if msg.Incomplete && msg.IncompleteReason != "" && len(ml.lines) > 0 {
		last := len(ml.lines) - 1
		ml.native[last] = appendIncompleteReason(ml.native[last], msg.IncompleteReason)
		ml.lines[last] = ml.native[last].PlainText()
	}
	return ml
}

func renderErrorMessage(msg types.ChatMessage, ctx renderContext) messageLines {
	var ml messageLines
	timeLabel := rendering.FormatTimeLabel(msg.Time)
	wrapped := rendering.WrapText(msg.Content, ctx.contentWidth)
	for i, line := range wrapped {
		pl := buildErrorHeaderLine(line, timeLabel, ctx.innerW, i == 0)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
	return ml
}

// hasThinkingBody reports whether a wrapped thinking body has real content
// (more than one line, or a single non-blank line). A body that wraps to just
// [""] is "no body" — a completed thinking block collapsed to its label — so
// the header↔body gap must be skipped to avoid stacking with the next block's
// separator and producing two blank rows.
func hasThinkingBody(wrapped []string) bool {
	if len(wrapped) == 1 {
		return strings.TrimSpace(wrapped[0]) != ""
	}
	return true
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
		return renderToolMessageLines(msg, ctx.th, ctx.contentWidth, ctx.innerW, ctx.width, ctx.spinFrame, ctx.height, ctx.phase)
	case types.RoleError:
		return renderErrorMessage(msg, ctx)
	default:
		return messageLines{lines: []string{""}, native: []PaintLine{buildPlainBodyLine("")}}
	}
}
