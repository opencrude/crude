package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// BuildSubPaneHeaderLine builds the title bar ("│ ● agent-name 12s").
func BuildSubPaneHeaderLine(header workspace.SubPaneHeaderView, width, spinFrame int) SubPaneLine {
	var spans []RowSpan
	spans = append(spans, RowSpan{Text: "│ ", Role: ui.ColorBorder})

	if !header.Done {
		dot := ui.ActivityDot(spinFrame) + " "
		meta := rendering.FormatThinkingDuration(header.Elapsed)
		if meta == "" {
			meta = "<1s"
		}
		titleWidth := max(1, width-rendering.VisibleWidth("│ ")-rendering.VisibleWidth(dot)-rendering.VisibleWidth(" "+meta))
		title := rendering.TruncateMiddleVisual(header.Title, titleWidth, "…")
		switch header.Type {
		case workspace.PaneThinking:
			spans = append(spans, RowSpan{Text: dot, Role: ui.ColorAccent})
			spans = append(spans, RowSpan{Text: title, Role: ui.ColorAccent, Attr: cell.AttrBold})
		default:
			spans = append(spans, RowSpan{Text: dot + title, Role: ui.ColorAccent, Attr: cell.AttrBold})
		}
	} else if header.Type == workspace.PaneThinking {
		duration := rendering.FormatThinkingDuration(header.Elapsed)
		if duration == "" {
			duration = "<1s"
		}
		spans = append(spans, RowSpan{Text: "● Thought for " + duration, Role: ui.ColorMuted})
		spans = append(spans, RowSpan{Text: " ▾", Role: ui.ColorMuted})
	} else {
		status, failed := subAgentDisplayStatus(header.Status)
		role := ui.ColorMuted
		marker := "● "
		if failed {
			role, marker = ui.ColorError, "× "
		}
		if status != "" {
			status = " — " + status
		}
		titleWidth := max(1, width-rendering.VisibleWidth("│ "+marker)-rendering.VisibleWidth(status+" ▾"))
		title := rendering.TruncateMiddleVisual(header.Title, titleWidth, "…")
		spans = append(spans, RowSpan{Text: marker, Role: role}, RowSpan{Text: title, Role: ui.ColorMuted})
		if status != "" {
			spans = append(spans, RowSpan{Text: status, Role: role})
		}
		spans = append(spans, RowSpan{Text: " ▾", Role: ui.ColorMuted})
	}

	if !header.Done {
		meta := rendering.FormatThinkingDuration(header.Elapsed)
		if meta == "" {
			meta = "<1s"
		}
		spans = append(spans, RowSpan{Text: " " + meta, Role: ui.ColorMuted})
	}

	pl := padPaintLine(PaintLine{Spans: spans}, width)
	line := NativeLine(pl)
	line.FrameSensitive = !header.Done
	return line
}

// BuildSubPaneSummaryLine builds the collapsed summary ("│ ● agent for 5.0s").
func BuildSubPaneSummaryLine(view workspace.SubPaneView, width int) SubPaneLine {
	summary := ""
	if view.Summary != nil {
		summary = view.Summary.Text
	}
	status := ""
	statusRole := ui.ColorMuted
	marker := "● "
	if view.Header.Type == workspace.PaneAgent {
		label, failed := subAgentDisplayStatus(view.Header.Status)
		if label != "" {
			status = " — " + label
		}
		if failed {
			statusRole, marker = ui.ColorError, "× "
		}
	}
	summaryWidth := max(1, width-rendering.VisibleWidth("│ "+marker)-rendering.VisibleWidth(status+" ▸"))
	summary = rendering.TruncateMiddleVisual(summary, summaryWidth, "…")
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: marker, Role: statusRole},
		{Text: summary, Role: ui.ColorMuted},
	}
	if status != "" {
		spans = append(spans, RowSpan{Text: status, Role: statusRole})
	}
	spans = append(spans, RowSpan{Text: " ▸", Role: ui.ColorMuted})
	pl := padPaintLine(PaintLine{Spans: spans}, width)
	return NativeLine(pl)
}

func subAgentDisplayStatus(status string) (string, bool) {
	switch status {
	case "", "completed":
		return "", false
	case "cancelled":
		return "cancelled", false
	case "failed_timeout":
		return "timed out", true
	case "failed_error", "error", "failed":
		return "failed", true
	case "failed_quota":
		return "quota exceeded", true
	case "failed_policy":
		return "policy blocked", true
	case "crashed":
		return "crashed", true
	default:
		return "failed", true
	}
}

// BuildSubPaneToolLines uses the same semantic titles as ordinary tool leaves.
func BuildSubPaneToolLines(messages []*types.ChatMessage, width, frame int) ([]SubPaneLine, bool) {
	var lines []SubPaneLine
	active := false
	for _, msg := range messages {
		if msg.Role != types.RoleTool || msg.ToolName == "" {
			continue
		}
		active = active || toolRowFrameSensitive(*msg)
		if tools.IsStructuredTool(msg.ToolName) {
			body := renderToolMessageLines(*msg, nil, width-toolBodyGutterWidth, width, width, frame, 12, msg.ToolPreviewPhase, DisclosureCollapsed)
			for _, line := range body.native {
				lines = append(lines, NativeLine(padPaintLine(line, width)))
			}
			continue
		}
		for _, pl := range wrapToolTitle(buildToolTitleLine(*msg, msg.ToolPreviewPhase, frame), width) {
			lines = append(lines, NativeLine(padPaintLine(pl, width)))
		}
	}
	return lines, active
}

// BuildCompactWorkingLine builds the placeholder when a compact pane has no tools yet.
func BuildCompactWorkingLine(thinking bool, width int) SubPaneLine {
	msg := "Working..."
	if thinking {
		msg = "Thinking..."
	}
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: "● " + msg, Role: ui.ColorMuted},
	}
	pl := padPaintLine(PaintLine{Spans: spans}, width)
	return NativeLine(pl)
}

func padPaintLine(pl PaintLine, width int) PaintLine {
	visW := pl.VisibleWidth()
	padR := width - visW
	if padR < 0 {
		padR = 0
	}
	if padR > 0 {
		pl.Spans = append(pl.Spans, RowSpan{Text: strings.Repeat(" ", padR), Role: ui.ColorFg})
	}
	return pl
}
