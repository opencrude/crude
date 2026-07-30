package chatprojection

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

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
				out.appendPlainToolBody(resultContent, width, height, false)
			}
		}
	} else if msg.ToolStatus == "error" || msg.ToolStatus == "cancelled" {
		if msg.Content != "" {
			out.appendPlainToolBody(msg.Content, width, height, true)
		}
	}

	return out
}

func (ml *messageLines) appendPlainToolBody(resultContent string, width, height int, errorStyle bool) {
	toolLineWidth := width - 6
	if toolLineWidth < 20 {
		toolLineWidth = 20
	}
	maxRes := tools.TerminalResultHeight(height)
	var wrapped []string
	for _, rl := range strings.Split(resultContent, "\n") {
		trimmed := strings.TrimSpace(rl)
		if trimmed == "" {
			wrapped = append(wrapped, "")
			continue
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

func (ml *messageLines) appendRichToolBody(msg types.ChatMessage, resultContent string, th *theme.Theme, contentWidth, innerW, height int) {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	if tools.IsPlanTool(msg.ToolName) {
		planBox := tools.RenderPlanBox(resultContent, contentWidth-4, th)
		for _, pl := range strings.Split(planBox, "\n") {
			ansiLine := dim.Render("│ ") + pl
			ml.lines = append(ml.lines, ansiLine)
			ml.native = append(ml.native, ansiFallbackLine(ansiLine))
		}
		return
	}
	maxRes := tools.TerminalResultHeight(height)
	diffWidth := innerW - 4
	if diffWidth < 20 {
		diffWidth = 20
	}
	renderedDiff := tools.RenderDiffNoHeader(resultContent, maxRes, diffWidth, th)
	if renderedDiff != "" {
		for _, dl := range strings.Split(renderedDiff, "\n") {
			ansiLine := dim.Render("│ ") + dl
			ml.lines = append(ml.lines, ansiLine)
			ml.native = append(ml.native, ansiFallbackLine(ansiLine))
		}
	} else {
		firstLine := strings.SplitN(resultContent, "\n", 2)[0]
		pl := buildToolPlainResultLine("│   ", firstLine, false)
		ml.lines = append(ml.lines, pl.PlainText())
		ml.native = append(ml.native, pl)
	}
}
