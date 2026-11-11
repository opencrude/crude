package editor

import "strings"

// indentStyle is one indentation level of a document: one tab, or size spaces.
type indentStyle struct {
	tab  bool
	size int
}

// indentScanLines bounds indentation detection. The first lines of a file
// carry its style. Results are cached by the shared buffer revision.
const indentScanLines = 2000

func (s indentStyle) unit() string {
	if s.tab {
		return "\t"
	}
	return strings.Repeat(" ", s.size)
}

// width is the visual width of one level at a line start.
func (s indentStyle) width() int {
	if s.tab {
		return TabStopAdvance(0, tabWidth)
	}
	return s.size
}

// indentStyle reads the document's own indentation, as editors that detect
// indentation do: Tab, Shift+Tab, Enter and Backspace must continue the file's
// style, not impose four spaces on a tab-indented or two-space file. Lines
// that start with a tab vote for tabs. Space-indented lines vote for the most
// frequent change of indentation between neighbouring lines.
func (e *Editor) indentStyle() indentStyle {
	style := indentStyle{size: DefaultTabWidth}
	if e.buf == nil {
		return style
	}
	if e.buf.indentationRevision == e.buf.Revision() && e.buf.indentation.size != 0 {
		return e.buf.indentation
	}
	defer func() {
		e.buf.indentation, e.buf.indentationRevision = style, e.buf.Revision()
	}()
	tabLines, spaceLines := 0, 0
	var steps [9]int // steps[n]: neighbouring lines whose space indent differs by n
	previous, havePrevious := 0, false
	for line := 0; line < min(e.buf.LineCount(), indentScanLines); line++ {
		raw, err := e.buf.lineView(line)
		if err != nil {
			break
		}
		spaces := 0
		for spaces < min(len(raw), 256) && raw[spaces] == ' ' {
			spaces++
		}
		if spaces == len(raw) || spaces == 256 || raw[spaces] == '\r' {
			continue // blank
		}
		if raw[0] == '\t' {
			tabLines++
			havePrevious = false
			continue
		}
		if raw[spaces] == '\t' {
			continue // mixed leading whitespace is not evidence of either style
		}
		if spaces > 0 {
			spaceLines++
		}
		if havePrevious {
			if step := max(spaces-previous, previous-spaces); step >= 2 && step < len(steps) {
				steps[step]++
			}
		}
		previous, havePrevious = spaces, true
	}
	if tabLines > spaceLines {
		style = indentStyle{tab: true, size: tabWidth}
		return style
	}
	best := 0
	for step := 2; step < len(steps); step++ {
		if steps[step] > steps[best] || best == 0 && steps[step] > 0 {
			best = step
		}
	}
	if best != 0 && steps[best] > 0 {
		style.size = best
	}
	return style
}

// levelUnit returns one more level for a line whose leading whitespace is
// indent. A line that already uses tabs gets a tab even in a spaces document.
func (e *Editor) levelUnit(indent string) string {
	if strings.Contains(indent, "\t") {
		return "\t"
	}
	style := e.indentStyle()
	if style.tab && indent != "" {
		// Space-indented lines inside a tab document keep their own spaces.
		return strings.Repeat(" ", DefaultTabWidth)
	}
	return style.unit()
}
