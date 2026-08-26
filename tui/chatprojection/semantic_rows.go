package chatprojection

import (
	"sort"
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

type referenceTextRange struct {
	start int
	end   int
}

// userReferenceLabels falls back to generated attachment labels for sessions
// persisted before reference-label display metadata existed.
func userReferenceLabels(content string, labels []string) []string {
	if len(labels) > 0 {
		return labels
	}
	var inferred []string
	seen := make(map[string]bool)
	for offset := 0; offset < len(content); {
		start := strings.IndexByte(content[offset:], '[')
		if start < 0 {
			break
		}
		start += offset
		closeAt := strings.IndexByte(content[start:], ']')
		if closeAt < 0 {
			break
		}
		end := start + closeAt + 1
		label := content[start:end]
		if generatedReferenceLabel(label) && !seen[label] {
			seen[label] = true
			inferred = append(inferred, label)
		}
		offset = end
	}
	return inferred
}

func generatedReferenceLabel(label string) bool {
	if len(label) < 3 || label[0] != '[' || label[len(label)-1] != ']' {
		return false
	}
	inner := label[1 : len(label)-1]
	for _, prefix := range []string{"Pasted Text File", "Pasted Text", "Pasted IMG", "File"} {
		if !strings.HasPrefix(inner, prefix) {
			continue
		}
		suffix := inner[len(prefix):]
		if suffix == "" {
			return false
		}
		for _, ch := range suffix {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// styleReferenceLabels gives sent attachment labels the same semantic chip
// treatment as the composer: bold Accent2 text on Selection background. It
// splits existing markdown spans rather than flattening them, so surrounding
// user markdown keeps its original styling.
func styleReferenceLabels(line PaintLine, labels []string) PaintLine {
	if len(labels) == 0 || len(line.Spans) == 0 {
		return line
	}
	plain := line.PlainText()
	ranges := referenceLabelRanges(plain, labels)
	if len(ranges) == 0 {
		return line
	}

	styled := PaintLine{ANSIFallback: line.ANSIFallback}
	spanStart := 0
	for _, span := range line.Spans {
		spanEnd := spanStart + len(span.Text)
		cuts := []int{0, len(span.Text)}
		for _, rr := range ranges {
			if rr.end <= spanStart || rr.start >= spanEnd {
				continue
			}
			cuts = append(cuts,
				max(0, rr.start-spanStart),
				min(len(span.Text), rr.end-spanStart),
			)
		}
		sort.Ints(cuts)
		uniqueCuts := cuts[:0]
		for _, cut := range cuts {
			if len(uniqueCuts) == 0 || uniqueCuts[len(uniqueCuts)-1] != cut {
				uniqueCuts = append(uniqueCuts, cut)
			}
		}
		for i := 0; i+1 < len(uniqueCuts); i++ {
			start, end := uniqueCuts[i], uniqueCuts[i+1]
			if start == end {
				continue
			}
			fragment := span
			fragment.Text = span.Text[start:end]
			absolute := spanStart + start
			if referenceRangeContains(ranges, absolute) {
				fragment.Role = ui.ColorAccent2
				fragment.BgRole = ui.ColorSelection
				fragment.BgSet = true
				fragment.FGSet = false
				fragment.BGSet = false
				fragment.Attr |= cell.AttrBold
			}
			styled.Spans = append(styled.Spans, fragment)
		}
		spanStart = spanEnd
	}
	return styled
}

func referenceLabelRanges(text string, labels []string) []referenceTextRange {
	var ranges []referenceTextRange
	for _, label := range labels {
		if label == "" {
			continue
		}
		for offset := 0; offset <= len(text)-len(label); {
			index := strings.Index(text[offset:], label)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(label)
			ranges = append(ranges, referenceTextRange{start: start, end: end})
			offset = end
		}
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end > ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})
	filtered := ranges[:0]
	lastEnd := -1
	for _, rr := range ranges {
		if rr.start < lastEnd {
			continue
		}
		filtered = append(filtered, rr)
		lastEnd = rr.end
	}
	return filtered
}

func referenceRangeContains(ranges []referenceTextRange, offset int) bool {
	for _, rr := range ranges {
		if offset >= rr.start && offset < rr.end {
			return true
		}
		if rr.start > offset {
			return false
		}
	}
	return false
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

// strikeBody applies a strikethrough attribute to every span of every supplied
// PaintLine. Used for cancelled/interrupted content (assistant text, thinking,
// tool calls) so the partial work reads as struck-through rather than truncated
// with an inline "(operation cancelled by user)" suffix that wraps off-screen.
func strikeBody(lines []PaintLine) {
	for i := range lines {
		spans := lines[i].Spans
		for j := range spans {
			spans[j].Attr |= cell.AttrStrikethrough
		}
	}
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
	// Continuation lines hang under the message text (after "error "), not flush
	// to the margin, and drop the bold so a long multi-line error reads as a
	// wrapped paragraph rather than a wall of shouting red.
	return PaintLine{Spans: []RowSpan{
		{Text: "      ", Role: ui.ColorError},
		{Text: line, Role: ui.ColorError},
	}}
}
