package chatprojection

import (
	"strings"

	"crude/tui/bidi"
	"crude/tui/rendering"
)

// CopySelection reads semantic source ranges from native rows. A soft wrap has
// no source newline; decoration has no source range. Non-Markdown rows retain
// their established visible-text copy behavior. Only selected rows are visited.
func CopySelection(rows []MessageRow, startLine, startCol, endLine, endCol int) string {
	var out strings.Builder
	var prior rendering.CopyRange
	lastRow := -1
	lineIndex := 0
	emit := func(text string, source rendering.CopyRange, line int) {
		if text == "" && !source.Valid() {
			return
		}
		if source.Valid() {
			if prior.Text == source.Text && prior.Valid() && source.Start >= prior.Start && source.End <= prior.End {
				return
			}
			if prior.Text == source.Text && prior.Valid() && source.Start >= prior.End {
				out.WriteString(source.Text.Value[prior.End:source.End])
			} else {
				if out.Len() > 0 {
					if prior.Valid() && source.Text.Separator != "" {
						out.WriteString(source.Text.Separator)
					} else if line > lastRow {
						out.WriteString("\n")
					}
				}
				out.WriteString(source.String())
			}
			prior = source
		} else {
			if out.Len() > 0 && line > lastRow {
				out.WriteString(strings.Repeat("\n", line-lastRow))
			}
			out.WriteString(text)
			prior = rendering.CopyRange{}
		}
		lastRow = line
	}
	var table rendering.CopyRange
	tableLine := 0
	flushTable := func() {
		if table.Valid() {
			emit("", table, tableLine)
			table = rendering.CopyRange{}
		}
	}
	collect := func(text string, source rendering.CopyRange, line int) {
		if source.Valid() && source.Text.Table {
			if table.Text != source.Text {
				flushTable()
				table, tableLine = source, line
			} else {
				table.Start, table.End = min(table.Start, source.Start), max(table.End, source.End)
			}
			return
		}
		if text != "" || source.Valid() {
			flushTable()
			emit(text, source, line)
		}
	}
	for _, row := range rows {
		for index, plain := range row.Lines {
			line := lineIndex
			lineIndex++
			if line < startLine {
				continue
			}
			if line > endLine {
				flushTable()
				return out.String()
			}
			start, end := 0, 1<<30
			if line == startLine {
				start = startCol
			}
			if line == endLine {
				end = endCol
			}
			if index >= len(row.Native) {
				collect(rendering.ExtractVisibleRange(rendering.StripAnsi(plain), start, end), rendering.CopyRange{}, line)
				continue
			}
			native := row.Native[index]
			if native.prose != nil {
				for _, box := range native.prose.boxes {
					if box.Cell.Source.Start == box.Cell.Source.End && start <= box.Start+native.prose.shift && end >= box.End+native.prose.shift {
						collect("", box.Cell.Source, line)
					}
				}
			}
			col := 0
			for _, span := range native.Spans {
				width := rendering.VisibleWidth(span.Text)
				from, to := max(start-col, 0), min(end-col, width)
				if to > from && span.Copy.Valid() {
					// ExtractVisibleRange uses the shared grapheme profile and
					// never selects half of a wide or combining source unit.
					prefix := rendering.ExtractVisibleRange(span.Text, 0, from)
					selected := rendering.ExtractVisibleRange(span.Text, from, to)
					if selected != "" {
						part := span.Slice(len(prefix), len(prefix)+len(selected))
						collect("", part.Copy, line)
					}
				}
				if to > from && !span.Copy.Valid() && !native.SourceAware {
					collect(rendering.ExtractVisibleRange(span.Text, from, to), rendering.CopyRange{}, line)
				}
				col += width
			}
		}
	}
	flushTable()
	return out.String()
}

// CopyAnchor retains source identity while a selected message reflows. It is a
// derivative of the immutable render source, not another text or selection owner.
type CopyAnchor struct {
	MessageID string
	Source    *rendering.CopyText
	Offset    int
}

func AnchorAt(rows []MessageRow, line, col int) (CopyAnchor, bool) {
	global := 0
	for _, row := range rows {
		if line >= global+len(row.Lines) {
			global += len(row.Lines)
			continue
		}
		at := line - global
		if at < 0 || at >= len(row.Native) || !row.Native[at].SourceAware {
			return CopyAnchor{}, false
		}
		native := row.Native[at]
		var domain rendering.CopyRange
		if native.prose != nil {
			shift := native.prose.shift
			for _, box := range native.prose.boxes {
				if col >= box.Start+shift && col < box.End+shift {
					domain = box.Cell.Source
					if box.Cell.RTL && bidi.Current().Arrange {
						switch {
						case col < box.ContentStart+shift:
							col = box.ContentEnd + shift
						case col >= box.ContentEnd+shift:
							col = box.ContentStart + shift
						}
					}
					break
				}
			}
		}
		x := 0
		var last CopyAnchor
		for _, span := range native.Spans {
			width := rendering.VisibleWidth(span.Text)
			if span.Copy.Valid() && (!domain.Valid() || span.Copy.Text == domain.Text && span.Copy.Start >= domain.Start && span.Copy.End <= domain.End) {
				last = CopyAnchor{MessageID: row.ID, Source: span.Copy.Text, Offset: span.Copy.End}
				if col <= x+width {
					n := len(rendering.ExtractVisibleRange(span.Text, 0, max(0, col-x)))
					offset := span.Copy.Start
					if span.Copy.End-span.Copy.Start == len(span.Text) {
						offset += n
					} else if col >= x+width {
						offset = span.Copy.End
					}
					last.Offset = offset
					return last, true
				}
			}
			x += width
		}
		if last.Source == nil && domain.Valid() {
			last = CopyAnchor{MessageID: row.ID, Source: domain.Text, Offset: domain.End}
		}
		return last, last.Source != nil
	}
	return CopyAnchor{}, false
}
func LocateAnchor(rows []MessageRow, anchor CopyAnchor) (line, col int, ok bool) {
	if anchor.Source == nil {
		return 0, 0, false
	}
	global := 0
	for _, row := range rows {
		if row.ID != anchor.MessageID {
			global += len(row.Lines)
			continue
		}
		for y, native := range row.Native {
			x := 0
			for _, span := range native.Spans {
				r := span.Copy
				if r.Valid() && r.Text.Block == anchor.Source.Block && r.Text.Value == anchor.Source.Value && anchor.Offset >= r.Start && anchor.Offset <= r.End {
					col := 0
					if r.End-r.Start == len(span.Text) {
						col = rendering.VisibleWidth(span.Text[:anchor.Offset-r.Start])
					} else if anchor.Offset == r.End {
						col = rendering.VisibleWidth(span.Text)
					}
					return global + y, x + col, true
				}
				x += rendering.VisibleWidth(span.Text)
			}
		}
		return 0, 0, false
	}
	return 0, 0, false
}

// SourceSelectionRanges projects one logical source selection into a painted row.
// Only spans from that source are highlighted, not adjacent table cells, rails
// or wrap padding. Expanded source units remain atomic in both copy and paint.
func SourceSelectionRanges(dst []bidi.ColumnRange, line PaintLine, selection rendering.CopyRange) []bidi.ColumnRange {
	col := 0
	for _, span := range line.Spans {
		width := rendering.VisibleWidth(span.Text)
		source := span.Copy
		if source.Valid() && source.Text == selection.Text {
			from, to := max(source.Start, selection.Start), min(source.End, selection.End)
			if to > from {
				a, b := col, col+width
				if source.End-source.Start == len(span.Text) {
					a += rendering.VisibleWidth(span.Text[:from-source.Start])
					b = col + rendering.VisibleWidth(span.Text[:to-source.Start])
				}
				if n := len(dst); n > 0 && dst[n-1].End == a {
					dst[n-1].End = b
				} else {
					dst = append(dst, bidi.ColumnRange{Start: a, End: b})
				}
			}
		}
		col += width
	}
	return dst
}
