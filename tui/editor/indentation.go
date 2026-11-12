package editor

import (
	"crude/document"
	"crude/indentation"
	"strings"
)

// indentStyle is one indentation level of a document: one tab, or size spaces.
type indentStyle struct {
	tab     bool
	size    int
	tabSize int
}

// indentScanLines bounds indentation detection. The first lines of a file
// carry its style. Results are cached by the shared buffer revision.
const indentScanLines = 2000

func (s indentStyle) unit() string {
	if s.tab {
		return s.padding(0, s.size)
	}
	return strings.Repeat(" ", s.size)
}

// indentStyle reads the document's own indentation, as editors that detect
// indentation do: Tab, Shift+Tab, Enter and Backspace must continue the file's
// style, not impose four spaces on a tab-indented or two-space file. Lines
// that start with a tab vote for tabs. Space-indented lines vote for the most
// frequent change of indentation between neighbouring lines.
func (e *Editor) detectedIndentStyle() indentStyle {
	style := indentStyle{size: e.TabWidth()}
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
		style = indentStyle{tab: true, size: e.TabWidth()}
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
	style := e.indentStyle()
	if e.indentationExplicit() {
		col := LineVisualWidth(indent, e.TabWidth())
		return style.padding(col, col+style.size)
	}
	if strings.Contains(indent, "\t") {
		return "\t"
	}
	if style.tab && indent != "" {
		// Space-indented lines inside a tab document keep their own spaces.
		return strings.Repeat(" ", e.TabWidth())
	}
	return style.unit()
}

func (e *Editor) indentStyle() indentStyle {
	value := e.Indentation()
	return indentStyle{tab: value.Style == indentation.Tabs, size: value.Size, tabSize: value.TabWidth}
}
func (s indentStyle) padding(start, end int) string {
	var out strings.Builder
	s.writePadding(&out, start, end)
	return out.String()
}

// writePadding uses the same indentation owner without a temporary string per
// line when a multiline insertion is assembled into an existing builder.
func (s indentStyle) writePadding(out *strings.Builder, start, end int) {
	for col := start; col < end; {
		width := TabStopAdvance(col, max(1, s.tabSize))
		if s.tab && col+width <= end {
			out.WriteByte('\t')
			col += width
		} else {
			out.WriteByte(' ')
			col++
		}
	}
}
func (e *Editor) indentationExplicit() bool {
	c := indentation.Overlay(indentation.Overlay(e.buf.indentSettings, e.buf.indentProject), e.buf.indentFile)
	return c.Style != "" || c.Size != 0 || c.SizeIsTab
}
func (e *Editor) Indentation() indentation.Choices {
	detected := e.detectedIndentStyle()
	style := indentation.Spaces
	if detected.tab {
		style = indentation.Tabs
	}
	return indentation.Resolve(indentation.Choices{Style: style, Size: detected.size, TabWidth: e.TabWidth()}, e.buf.indentSettings, e.buf.indentProject, e.buf.indentFile)
}
func (e *Editor) IndentationChoices() indentation.Choices { return e.buf.indentFile }
func (e *Editor) IndentationGeneration() uint64           { return e.buf.indentGeneration }
func (e *Editor) SetIndentationChoices(c indentation.Choices) error {
	if err := c.Validate(); err != nil {
		return err
	}
	e.buf.indentChoiceGeneration++ // even explicit Reset wins over a late restore
	if e.buf.indentFile == c {
		return nil
	}
	e.buf.indentFile = c
	e.applyIndentationWidth()
	return nil
}
func (e *Editor) SetProjectIndentation(c indentation.Choices) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if e.buf.indentProject == c {
		return nil
	}
	e.buf.indentProject = c
	e.applyIndentationWidth()
	return nil
}
func (e *Editor) applyIndentationWidth() {
	c := indentation.Overlay(indentation.Overlay(e.buf.indentSettings, e.buf.indentProject), e.buf.indentFile)
	width := c.TabWidth
	if width == 0 {
		width = DefaultTabWidth
	}
	e.setTabWidth(width)
	e.buf.indentGeneration++
}
func (e *Editor) IndentationSource() string {
	switch {
	case e.buf.indentFile != (indentation.Choices{}):
		return "This file"
	case e.buf.indentProject != (indentation.Choices{}):
		return "EditorConfig"
	case e.buf.indentSettings.Style != "" || e.buf.indentSettings.Size != 0:
		return "Settings"
	default:
		return "Detected from text"
	}
}

// convertIndentation rewrites only leading whitespace, preserving its rendered
// columns and every content/terminator byte. One vector uses canonical Undo.
func (e *Editor) convertIndentation(tabs bool) {
	style := e.indentStyle()
	style.tab = tabs
	policy := e.IndentationChoices()
	policy.Style = indentation.Spaces
	if tabs {
		policy.Style = indentation.Tabs
	}
	_ = e.SetIndentationChoices(policy)
	e.rewriteIndentation(0, e.LineCount()-1, 0, style)
}

// Indent, outdent and conversion replace each leading run as one exact vector.
// Inserting spaces before an existing tab does not necessarily move source ink;
// deriving the complete new run prevents cursor and indentation from disagreeing.
func (e *Editor) rewriteIndentation(first, last, delta int, style indentStyle) {
	_ = e.applyIndentation(first, last, delta, style, nil)
}
func (e *Editor) applyIndentation(first, last, delta int, style indentStyle, only map[int]bool) error {
	spans := e.selectionSpans()
	positions := make(map[int]Position, len(spans)*2)
	for _, s := range spans {
		positions[s.Anchor] = e.buf.positionAtOffset(s.Anchor)
		positions[s.Cursor] = e.buf.positionAtOffset(s.Cursor)
	}
	edits, shifts := e.indentationEdits(first, last, delta, style, only)
	return e.applyMappedCaretEdits(edits, spans, func(s selectionSpan) SourceSelection {
		adjust := func(off int) int {
			p := positions[off]
			if shift, ok := shifts[p.Line]; ok && p.Col > 0 && p.Col < shift[0] {
				p.Col = max(0, p.Col+shift[1])
				return e.buf.positionToOffset(e.buf.snapPosition(p))
			}
			return remapLinkedOffset(off, edits)
		}
		return SourceSelection{adjust(s.Anchor), adjust(s.Cursor)}
	})
}
func (e *Editor) useDetectedIndentation() {
	e.buf.indentationRevision = 0
	style := e.detectedIndentStyle()
	choice := indentation.Choices{Style: indentation.Spaces, Size: style.size}
	if style.tab {
		choice.Style = indentation.Tabs
	}
	_ = e.SetIndentationChoices(choice)
}

// RestoreIndentationChoices never overwrites a newer choice in an already-open
// linked document while a background pane restoration is still arriving.
func (e *Editor) RestoreIndentationChoices(c indentation.Choices) {
	if e.buf.indentChoiceGeneration == 0 {
		_ = e.SetIndentationChoices(c)
	}
}

func (e *Editor) indentationEdits(first, last, delta int, style indentStyle, only map[int]bool) ([]document.Edit, map[int][2]int) {
	var edits []document.Edit
	shifts := make(map[int][2]int)
	for line := first; line <= last; line++ {
		if only != nil && !only[line] {
			continue
		}
		raw := e.LineText(line)
		if raw == "" {
			continue
		}
		prefix := leadingWhitespace(raw)
		old := LineVisualWidth(prefix, e.TabWidth())
		width := max(0, old+delta)
		replacement := style.padding(0, width)
		if replacement != prefix {
			start := e.buf.positionToOffset(Position{Line: line})
			edits = append(edits, document.Edit{Start: start, End: start + len(prefix), Text: replacement})
			shifts[line] = [2]int{old, width - old}
		}
	}

	return edits, shifts
}
