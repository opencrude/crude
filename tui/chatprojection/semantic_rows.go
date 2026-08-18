package chatprojection

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

func thinkingDotRole(spinFrame int) ui.SemanticColor {
	switch ui.ThinkingDotIndex(spinFrame) % 4 {
	case 0:
		return ui.ColorAccent
	case 1:
		return ui.ColorWarning
	case 2:
		return ui.ColorError
	default:
		return ui.ColorWarning
	}
}

func spanWithBg(text string, role, bg ui.SemanticColor, attr cell.Attrs) RowSpan {
	return RowSpan{Text: text, Role: role, BgRole: bg, BgSet: true, Attr: attr}
}

func buildUserHeader(username, timeLabel string, innerW int, suppressHeader bool) PaintLine {
	if suppressHeader {
		return PaintLine{}
	}
	label := username + " "
	pl := PaintLine{Spans: []RowSpan{
		spanWithBg(label, ui.ColorFg, ui.ColorPopupSubtleBg, cell.AttrBold),
	}}
	if timeLabel != "" {
		pl = padHeaderWithTime(pl, timeLabel, innerW)
	}
	return pl
}

func buildAssistantHeader(modelName string, incomplete bool, timeLabel string, innerW int, suppressHeader bool) PaintLine {
	if suppressHeader {
		return PaintLine{}
	}
	var label string
	var role, bg ui.SemanticColor
	var attr cell.Attrs
	if incomplete {
		label = "⚠ Crude " + modelName + " "
		role = ui.ColorError
		bg = ui.ColorIncompleteBg
		attr = cell.AttrBold | cell.AttrItalic
	} else {
		label = "Crude " + modelName + " "
		role = ui.ColorAccent2
		bg = ui.ColorPopupSubtleBg
		attr = cell.AttrBold
	}
	pl := PaintLine{Spans: []RowSpan{spanWithBg(label, role, bg, attr)}}
	if timeLabel != "" {
		pl = padHeaderWithTime(pl, timeLabel, innerW)
	}
	return pl
}

func padHeaderWithTime(header PaintLine, timeLabel string, innerW int) PaintLine {
	lineW := header.VisibleWidth()
	timeW := rendering.VisibleWidth(timeLabel)
	sp := innerW - lineW - timeW
	if sp < 1 {
		sp = 1
	}
	spans := append([]RowSpan{}, header.Spans...)
	spans = append(spans, RowSpan{Text: strings.Repeat(" ", sp), Role: ui.ColorFg})
	spans = append(spans, RowSpan{Text: timeLabel, Role: ui.ColorMuted})
	return PaintLine{Spans: spans}
}

func buildPlainBodyLine(text string) PaintLine {
	if text == "" {
		return PaintLine{Spans: []RowSpan{{Text: "", Role: ui.ColorFg}}}
	}
	return PaintLine{Spans: []RowSpan{{Text: text, Role: ui.ColorFg}}}
}

func buildPlainBodyLines(texts []string) []PaintLine {
	out := make([]PaintLine, len(texts))
	for i, t := range texts {
		out[i] = buildPlainBodyLine(t)
	}
	return out
}

func buildThinkingHeader(modelName string, incomplete bool, timeLabel string, innerW int, suppressHeader bool) (header PaintLine, blank PaintLine) {
	if suppressHeader {
		return PaintLine{}, buildPlainBodyLine("")
	}
	header = buildAssistantHeader(modelName, incomplete, timeLabel, innerW, false)
	return header, buildPlainBodyLine("")
}

func buildThinkingBodyLine(line string, firstLine bool, spinFrame int, active, interrupted bool) PaintLine {
	spans := []RowSpan{{Text: "│ ", Role: ui.ColorBorder}}
	if firstLine {
		dotRole := ui.ColorMuted
		switch {
		case active:
			dotRole = thinkingDotRole(spinFrame)
		case interrupted:
			dotRole = ui.ColorWarning
		}
		spans = append(spans, RowSpan{Text: "● ", Role: dotRole})
	} else {
		spans = append(spans, RowSpan{Text: "  ", Role: ui.ColorBorder})
	}
	spans = append(spans, RowSpan{Text: line, Role: ui.ColorMuted, Attr: cell.AttrItalic})
	return PaintLine{Spans: spans}
}

func appendIncompleteReason(pl PaintLine, reason string) PaintLine {
	if reason == "" {
		return pl
	}
	spans := append([]RowSpan{}, pl.Spans...)
	spans = append(spans, RowSpan{
		Text: " (" + reason + ")",
		Role: ui.ColorError,
		Attr: cell.AttrItalic,
	})
	return PaintLine{Spans: spans}
}

func appendBlockCursor(pl PaintLine, frame int, activeTyping bool) PaintLine {
	spans := append([]RowSpan{}, pl.Spans...)
	if activeTyping || ui.BlinkOn(frame) {
		spans = append(spans, RowSpan{Text: "█", Role: ui.ColorAccent})
	} else {
		spans = append(spans, RowSpan{Text: " ", Role: ui.ColorFg})
	}
	return PaintLine{Spans: spans}
}

func buildErrorHeaderLine(line, timeLabel string, innerW int, first bool) PaintLine {
	if first {
		spans := []RowSpan{
			{Text: "error", Role: ui.ColorError, Attr: cell.AttrBold},
			{Text: " ", Role: ui.ColorError},
			{Text: line, Role: ui.ColorError, Attr: cell.AttrBold},
		}
		pl := PaintLine{Spans: spans}
		if timeLabel != "" {
			timeW := rendering.VisibleWidth(timeLabel)
			lineW := pl.VisibleWidth()
			sp := innerW - lineW - timeW
			if sp < 1 {
				sp = 1
			}
			spans = append(spans, RowSpan{Text: strings.Repeat(" ", sp), Role: ui.ColorFg})
			spans = append(spans, RowSpan{Text: timeLabel, Role: ui.ColorMuted})
			pl = PaintLine{Spans: spans}
		}
		return pl
	}
	return PaintLine{Spans: []RowSpan{
		{Text: "  ", Role: ui.ColorError},
		{Text: line, Role: ui.ColorError, Attr: cell.AttrBold},
	}}
}
