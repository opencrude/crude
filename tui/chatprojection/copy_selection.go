package chatprojection

import (
	"strings"

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
	for _, row := range rows {
		for index, plain := range row.Lines {
			line := lineIndex
			lineIndex++
			if line < startLine {
				continue
			}
			if line > endLine {
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
				emit(rendering.ExtractVisibleRange(rendering.StripAnsi(plain), start, end), rendering.CopyRange{}, line)
				continue
			}
			native := row.Native[index]
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
						emit("", part.Copy, line)
					}
				}
				if to > from && !span.Copy.Valid() && !native.SourceAware {
					emit(rendering.ExtractVisibleRange(span.Text, from, to), rendering.CopyRange{}, line)
				}
				col += width
			}
		}
	}
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
		x := 0
		var last CopyAnchor
		for _, span := range row.Native[at].Spans {
			width := rendering.VisibleWidth(span.Text)
			if span.Copy.Valid() {
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

// SourceSelectionCols projects one logical source selection into a painted row.
// Only spans from that source are highlighted, not adjacent table cells, rails
// or wrap padding. Expanded source units remain atomic in both copy and paint.
func SourceSelectionCols(line PaintLine, selection rendering.CopyRange) (start, end int) {
	found := false
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
				if !found {
					start = a
					found = true
				}
				end = b
			}
		}
		col += width
	}
	return
}
