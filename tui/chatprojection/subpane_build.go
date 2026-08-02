package chatprojection

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/ui"
	"crude/workspace"
)

// BuildSubPaneHeaderLine builds the title bar ("│ ● agent-name 12s").
func BuildSubPaneHeaderLine(header workspace.SubPaneHeaderView, width, spinFrame int) SubPaneLine {
	var spans []RowSpan
	spans = append(spans, RowSpan{Text: "│ ", Role: ui.ColorBorder})

	if !header.Done {
		switch header.Type {
		case workspace.PaneThinking:
			spans = append(spans, RowSpan{Text: "● ", Role: thinkingDotRole(spinFrame)})
			spans = append(spans, RowSpan{Text: header.Title, Role: ui.ColorAccent, Attr: cell.AttrBold})
		default:
			spans = append(spans, RowSpan{Text: "● " + header.Title, Role: ui.ColorAccent, Attr: cell.AttrBold})
		}
	} else {
		spans = append(spans, RowSpan{Text: "● " + header.Title, Role: ui.ColorMuted})
	}

	meta := fmt.Sprintf("%.0fs", header.Elapsed.Seconds())
	spans = append(spans, RowSpan{Text: " " + meta, Role: ui.ColorMuted})

	pl := padPaintLine(PaintLine{Spans: spans}, width)
	return NativeLine(pl)
}

// BuildSubPaneSummaryLine builds the collapsed summary ("│ ● agent for 5.0s").
func BuildSubPaneSummaryLine(view workspace.SubPaneView, width int) SubPaneLine {
	summary := ""
	if view.Summary != nil {
		summary = view.Summary.Text
	}
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: "● " + summary, Role: ui.ColorMuted},
	}
	pl := padPaintLine(PaintLine{Spans: spans}, width)
	return NativeLine(pl)
}

// BuildSubPaneToolLines builds tool rows from SubPaneView.ToolLines.
func BuildSubPaneToolLines(view workspace.SubPaneView, width int) []SubPaneLine {
	var lines []SubPaneLine
	for _, tl := range view.ToolLines {
		if tl.Label == "" {
			continue
		}
		spans := []RowSpan{{Text: "│ ", Role: ui.ColorBorder}}
		switch {
		case tl.Status == "error" || tl.Phase == "error":
			spans = append(spans, RowSpan{Text: "✗ ", Role: ui.ColorError})
		case tl.Phase == "done" || tl.Status == "success":
			spans = append(spans, RowSpan{Text: "✓ ", Role: ui.ColorSuccess})
		case tl.Phase == "streaming" || tl.Status == "streaming" || tl.Status == "calling":
			spans = append(spans, RowSpan{Text: "● ", Role: ui.ColorFg})
		default:
			spans = append(spans, RowSpan{Text: "○ ", Role: ui.ColorFg})
		}
		spans = append(spans, RowSpan{Text: tl.Label, Role: ui.ColorFg})
		pl := padPaintLine(PaintLine{Spans: spans}, width)
		lines = append(lines, NativeLine(pl))
	}
	return lines
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
