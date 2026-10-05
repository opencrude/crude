package chatprojection

import (
	"sort"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/ui"
)

func buildUserHeader(username, timeLabel string, innerW int, suppressHeader bool) PaintLine {
	if suppressHeader {
		return PaintLine{}
	}
	label := username + " "
	pl := PaintLine{Spans: []RowSpan{
		{Text: label, Role: ui.ColorFg, Attr: cell.AttrBold},
	}}
	return padHeaderWithTime(pl, timeLabel, innerW)
}

func buildAssistantHeader(modelName string, incomplete bool, timeLabel string, innerW int, suppressHeader bool) PaintLine {
	if suppressHeader {
		return PaintLine{}
	}
	var label string
	var role ui.SemanticColor
	var attr cell.Attrs
	if incomplete {
		label = "Crude " + modelName + " · partial "
		role = ui.ColorMuted
		attr = cell.AttrBold
	} else {
		label = "Crude " + modelName + " "
		role = ui.ColorAccent2
		attr = cell.AttrBold
	}
	pl := PaintLine{Spans: []RowSpan{{Text: label, Role: role, Attr: attr}}}
	return padHeaderWithTime(pl, timeLabel, innerW)
}

func padHeaderWithTime(header PaintLine, timeLabel string, innerW int) PaintLine {
	lineW := header.VisibleWidth()
	timeW := rendering.VisibleWidth(timeLabel)
	if lineW+1+timeW > innerW {
		timeLabel = ""
		timeW = 0
	}
	sp := innerW - lineW - timeW
	spans := append([]RowSpan{}, header.Spans...)
	// A faint rule carries the header across available space without putting a
	// colored block behind the name. Keep short/narrow headers readable.
	if sp >= 3 {
		spans = append(spans, RowSpan{Text: " ", Role: ui.ColorBorder},
			RowSpan{Text: strings.Repeat("─", sp-2), Role: ui.ColorBorder})
		if timeLabel != "" {
			spans = append(spans, RowSpan{Text: " ", Role: ui.ColorBorder})
		} else {
			spans = append(spans, RowSpan{Text: "─", Role: ui.ColorBorder})
		}
	} else if timeLabel != "" {
		spans = append(spans, RowSpan{Text: " ", Role: ui.ColorFg})
	}
	if timeLabel != "" {
		spans = append(spans, RowSpan{Text: timeLabel, Role: ui.ColorMuted})
	}
	return PaintLine{Spans: spans, HeaderRule: true}
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

func buildThinkingBodyLine(line string, firstLine bool, spinFrame int, active, interrupted bool) PaintLine {
	spans := []RowSpan{{Text: "│ ", Role: ui.ColorBorder}}
	if firstLine {
		dot := "● "
		dotRole := ui.ColorMuted
		switch {
		case active:
			dot = ui.ActivityDot(spinFrame) + " "
			dotRole = ui.ColorAccent
		case interrupted:
			dotRole = ui.ColorWarning
		}
		spans = append(spans, RowSpan{Text: dot, Role: dotRole})
	} else {
		spans = append(spans, RowSpan{Text: "  ", Role: ui.ColorBorder})
	}
	spans = append(spans, RowSpan{Text: line, Role: ui.ColorMuted, Attr: cell.AttrItalic})
	return PaintLine{Spans: spans}
}

func appendBlockCursor(pl PaintLine, frame int, activeTyping bool) PaintLine {
	spans := append([]RowSpan{}, pl.Spans...)
	if activeTyping || ui.BlinkOn(frame) {
		spans = append(spans, RowSpan{Text: "█", Role: ui.ColorAccent})
	} else {
		spans = append(spans, RowSpan{Text: " ", Role: ui.ColorFg})
	}
	pl.Spans = spans
	return pl
}
