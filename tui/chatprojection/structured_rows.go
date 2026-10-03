package chatprojection

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
)

// appendStructuredToolBody budgets complete semantic entries rather than
// cutting a flat list midway through somebody's attribution. Expanded mode
// paints every retained value. The same rows serve live, hydrated and batch
// leaves, including their plain-text selection/copy mirrors.
func (ml *messageLines) appendStructuredToolBody(result tools.StructuredResult, input string, width, height int, expanded bool) {
	sectionLimit := 3
	if height < 18 {
		sectionLimit = 2
	}
	bodyLimit := max(3, min(6, height/6))
	visible, hidden, moreText := 0, 0, false
	for _, section := range result.Sections {
		if !expanded && section.DetailOnly {
			continue
		}
		if !expanded && visible >= sectionLimit {
			hidden++
			continue
		}
		if visible > 0 {
			ml.appendStructuredLine(PaintLine{}, width)
		}
		visible++
		ml.appendStructuredText(section.Title, ui.ColorToolOperand, cell.AttrBold, width, 0)
		ml.appendStructuredText(section.Metadata, ui.ColorMuted, 0, width, 0)
		limit := 0
		if !expanded {
			limit = bodyLimit
		}
		moreText = ml.appendStructuredText(section.Body, ui.ColorFg, 0, width, limit) || moreText
		if !expanded && section.Body == "" {
			// Generic MCP objects and receipt-only operations still need useful
			// values in compact form, not a heading over an empty body.
			for index, field := range section.Details {
				if index >= 3 {
					moreText = true
					break
				}
				moreText = ml.appendStructuredText(field.Label+": "+field.Value, ui.ColorFg, 0, width, bodyLimit) || moreText
			}
		}
		if expanded {
			for _, field := range section.Details {
				ml.appendStructuredField(field, width)
			}
		}
	}
	if !expanded && (hidden > 0 || moreText) {
		label := "Expand for full text and details"
		if hidden > 0 {
			unit := result.Unit
			if unit == "" {
				unit = "entries"
			}
			label = fmt.Sprintf("+%d %s · %s", hidden, unit, strings.ToLower(label))
		}
		ml.appendStructuredText(label, ui.ColorMuted, 0, width, 0)
	}
	if expanded && input != "" {
		request := tools.BuildReadableResult(input)
		if len(request.Sections) > 0 {
			ml.appendStructuredLine(PaintLine{}, width)
			ml.appendStructuredText("Request", ui.ColorMuted, cell.AttrBold, width, 0)
			for _, section := range request.Sections {
				ml.appendStructuredText(section.Title, ui.ColorMuted, 0, width, 0)
				ml.appendStructuredText(section.Metadata, ui.ColorMuted, 0, width, 0)
				ml.appendStructuredText(section.Body, ui.ColorFg, 0, width, 0)
				for _, field := range section.Details {
					ml.appendStructuredField(field, width)
				}
			}
		}
	}
}

func structuredGutter(width int) string {
	return rendering.TruncateVisual("│   ", max(0, width-1), "")
}

func (ml *messageLines) appendStructuredText(text string, role ui.SemanticColor, attr cell.Attrs, width, limit int) bool {
	if text == "" || width <= 0 {
		return false
	}
	gutter := structuredGutter(width)
	bodyWidth := max(1, width-rendering.VisibleWidth(gutter))
	count := 0
	for _, paragraph := range strings.Split(rendering.SanitizeForTerminal(text), "\n") {
		for _, part := range rendering.WordWrap(paragraph, bodyWidth) {
			if limit > 0 && count >= limit {
				return true
			}
			ml.appendStructuredLine(PaintLine{Spans: []RowSpan{{Text: part, Role: role, Attr: attr}}}, width)
			count++
		}
	}
	return false
}

func (ml *messageLines) appendStructuredField(field tools.ResultField, width int) {
	if field.Value == "" || width <= 0 {
		return
	}
	for i, paragraph := range strings.Split(rendering.SanitizeForTerminal(field.Value), "\n") {
		line := PaintLine{}
		if i == 0 && field.Label != "" {
			line.Spans = append(line.Spans, RowSpan{Text: rendering.SanitizeForTerminal(field.Label) + ": ", Role: ui.ColorMuted})
		}
		line.Spans = append(line.Spans, RowSpan{Text: paragraph, Role: ui.ColorFg})
		for _, row := range wrapPaintLine(line, max(1, width-rendering.VisibleWidth(structuredGutter(width)))) {
			ml.appendStructuredLine(row, width)
		}
	}
}

func (ml *messageLines) appendStructuredLine(line PaintLine, width int) {
	if width <= 0 {
		return
	}
	line.Spans = append([]RowSpan{{Text: structuredGutter(width), Role: ui.ColorMuted}}, line.Spans...)
	ml.native = append(ml.native, line)
	ml.lines = append(ml.lines, line.PlainText())
}
