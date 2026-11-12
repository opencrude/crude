package editor

import (
	"crude/document"
	"crude/tui/ui"
	"slices"
	"strings"
)

type selectedLines struct {
	first, last       int
	selected, primary bool
}

func (e *Editor) selectedLineGroups(join, adjacent bool) []selectedLines {
	var groups []selectedLines
	for _, s := range e.selectionSpans() {
		a, b := s.bounds()
		first, last := e.buf.positionAtOffset(a), e.buf.positionAtOffset(b)
		if first.Line < last.Line && last.Col == 0 {
			last.Line--
		}
		if join && first.Line == last.Line {
			last.Line = min(last.Line+1, e.LineCount()-1)
		}
		g := selectedLines{first.Line, last.Line, a != b, s.primary}
		extra := 0
		if adjacent {
			extra = 1
		}
		if n := len(groups); n > 0 && g.first <= groups[n-1].last+extra {
			prior := &groups[n-1]
			prior.last = max(prior.last, g.last)
			prior.selected = prior.selected || g.selected
			prior.primary = prior.primary || g.primary
		} else {
			groups = append(groups, g)
		}
	}
	return groups
}
func linePlannedAction(id ui.ActionID) bool {
	switch id {
	case "edit.moveLineUp", "edit.moveLineDown", "edit.deleteLine", "edit.joinLines", "edit.sortLines", "edit.reverseLines", "edit.toggleComment":
		return true
	}
	return false
}

// applyMappedCaretEdits retains all input ranges while the command's source
// transform runs once. Mapping reads the committed bytes, never a second table.
func (e *Editor) applyMappedCaretEdits(edits []document.Edit, spans []selectionSpan, mapRange func(selectionSpan) SourceSelection) error {
	if len(edits) == 0 {
		return nil
	}
	if err := e.buf.checkOffsets(edits); err != nil {
		return err
	}
	e.beginUndoGroup()
	defer e.endUndoGroup()
	e.multiApplying = true
	err := e.spliceRecordedBatch(edits, e.cursor)
	e.multiApplying = false
	if err != nil {
		return err
	}
	for i, s := range spans {
		spans[i].SourceSelection = mapRange(s)
		spans[i].goal = caretGoal{}
	}
	if err := e.setSelections(spans); err != nil {
		return err
	}
	e.finishSimpleEdit()
	return nil
}
func (e *Editor) applyNativeLineAction(id ui.ActionID) error {
	e.buf.ensureCache()
	spans := e.selectionSpans()
	groups := e.selectedLineGroups(id == "edit.joinLines", id == "edit.moveLineUp" || id == "edit.moveLineDown")
	if id == "edit.toggleComment" {
		return e.applyCommentLines(groups, spans)
	}
	if id == "edit.moveLineUp" || id == "edit.moveLineDown" {
		return e.applyMovedLines(groups, spans, id == "edit.moveLineDown")
	}
	if id == "edit.deleteLine" {
		var edits []document.Edit
		for _, g := range groups {
			a := e.buf.positionToOffset(Position{Line: g.first})
			b := e.buf.positionToOffset(Position{Line: g.last, Col: e.lineVisualWidth(g.last)})
			if g.last+1 < e.LineCount() {
				b = e.buf.positionToOffset(Position{Line: g.last + 1})
			} else if g.first > 0 {
				a = e.buf.positionToOffset(Position{Line: g.first - 1, Col: e.lineVisualWidth(g.first - 1)})
			}
			edits = append(edits, document.Edit{Start: a, End: b})
		}
		// Neighboring deletions can share the last terminator; union once.
		merged := edits[:0]
		for _, edit := range edits {
			if len(merged) > 0 && edit.Start <= merged[len(merged)-1].End {
				merged[len(merged)-1].End = max(merged[len(merged)-1].End, edit.End)
			} else {
				merged = append(merged, edit)
			}
		}
		edits = merged
		columns := make(map[int]int, len(spans))
		for _, s := range spans {
			columns[s.Cursor] = e.buf.positionAtOffset(s.Cursor).Col
		}
		return e.applyMappedCaretEdits(edits, spans, func(s selectionSpan) SourceSelection {
			pos := e.buf.positionAtOffset(remapLinkedOffset(s.Cursor, edits))
			pos.Col = columns[s.Cursor]
			off := e.buf.positionToOffset(e.buf.snapPosition(pos))
			return SourceSelection{off, off}
		})
	}
	if id != "edit.joinLines" && !e.HasSelection() {
		last := e.LineCount() - 1
		if last > 0 && e.LineText(last) == "" {
			last--
		}
		groups = []selectedLines{{0, last, true, true}}
	}
	var plans []caretEdit
	for _, g := range groups {
		a := e.buf.positionToOffset(Position{Line: g.first})
		b := e.buf.positionToOffset(Position{Line: g.last, Col: e.lineVisualWidth(g.last)})
		text := string(e.buf.cachedBytes[a:b])
		p := replacement(a, b, text)
		p.anchor = 0
		p.primary = g.primary
		if g.first == g.last {
			if id == "edit.joinLines" {
				p.anchor, p.cursor = 0, 0
			}
			plans = append(plans, p)
			continue
		}
		if id == "edit.joinLines" {
			var joined strings.Builder
			joined.WriteString(strings.TrimRight(e.LineText(g.first), " \t"))
			seam := joined.Len()
			for line := g.first + 1; line <= g.last; line++ {
				next := strings.Trim(e.LineText(line), " \t")
				if joined.Len() > 0 && next != "" {
					joined.WriteByte(' ')
				}
				joined.WriteString(next)
			}
			p.Text = joined.String()
			p.cursor = len(p.Text)
			if !g.selected {
				p.anchor, p.cursor = seam, seam
			}
		} else {
			lines := make([]string, 0, g.last-g.first+1)
			for line := g.first; line <= g.last; line++ {
				lines = append(lines, e.LineText(line))
			}
			if id == "edit.reverseLines" {
				slices.Reverse(lines)
			} else {
				slices.Sort(lines)
			}
			p.Text = strings.Join(lines, e.lineEnding())
			p.cursor = len(p.Text)
		}
		plans = append(plans, p)
	}
	return e.applyCaretEdits(plans)
}
func (e *Editor) applyMovedLines(groups []selectedLines, spans []selectionSpan, down bool) error {
	direction := -1
	if down {
		direction = 1
	}
	var edits []document.Edit
	positions := make(map[int]Position, len(spans)*2)
	for _, s := range spans {
		positions[s.Anchor] = e.buf.positionAtOffset(s.Anchor)
		positions[s.Cursor] = e.buf.positionAtOffset(s.Cursor)
	}
	for _, g := range groups {
		if !down && g.first == 0 || down && g.last == e.LineCount()-1 {
			continue
		}
		first, last := g.first, g.last
		neighbor := first - 1
		if down {
			neighbor = last + 1
			last++
		} else {
			first--
		}
		var lines []string
		if down {
			lines = append(lines, e.LineText(neighbor))
		}
		for line := g.first; line <= g.last; line++ {
			lines = append(lines, e.LineText(line))
		}
		if !down {
			lines = append(lines, e.LineText(neighbor))
		}
		edits = append(edits, document.Edit{Start: e.buf.positionToOffset(Position{Line: first}), End: e.buf.positionToOffset(Position{Line: last, Col: e.lineVisualWidth(last)}), Text: strings.Join(lines, e.lineEnding())})
	}
	return e.applyMappedCaretEdits(edits, spans, func(s selectionSpan) SourceSelection {
		start, end := positions[s.Anchor], positions[s.Cursor]
		a, _ := s.bounds()
		line := positions[a].Line
		for _, g := range groups {
			if line >= g.first && line <= g.last && (g.first != 0 || down) && (g.last != e.LineCount()-1 || !down) {
				start.Line += direction
				end.Line += direction
				break
			}
		}
		return SourceSelection{e.buf.positionToOffset(e.buf.snapPosition(start)), e.buf.positionToOffset(e.buf.snapPosition(end))}
	})
}
func (e *Editor) applyCommentLines(groups []selectedLines, selections []selectionSpan) error {
	prefix, start, end := e.commentTokens()
	if prefix == "" && (start == "" || end == "") {
		return nil
	}
	type commentLine struct {
		at, tail int
		body     string
		wrapped  bool
	}
	var lines []commentLine
	all := true
	for _, g := range groups {
		for line := g.first; line <= g.last; line++ {
			raw := e.LineText(line)
			head := len(leadingWhitespace(raw))
			tail := len(strings.TrimRight(raw, " \t"))
			if tail <= head {
				continue
			}
			off := e.buf.positionToOffset(Position{Line: line})
			body := raw[head:tail]
			wrapped := strings.HasPrefix(body, prefix)
			if prefix == "" {
				wrapped = len(body) >= len(start)+len(end) && strings.HasPrefix(body, start) && strings.HasSuffix(body, end)
				if !wrapped && (strings.Contains(body, start) || strings.Contains(body, end)) {
					return nil
				}
			}
			lines = append(lines, commentLine{off + head, off + tail, body, wrapped})
			all = all && wrapped
		}
	}
	var edits []document.Edit
	heads := map[int]int{}
	for _, line := range lines {
		if prefix != "" {
			if all {
				remove := prefix
				if strings.HasPrefix(line.body, prefix+" ") {
					remove += " "
				}
				edits = append(edits, document.Edit{Start: line.at, End: line.at + len(remove)})
			} else {
				ins := prefix + " "
				edits = append(edits, document.Edit{Start: line.at, End: line.at, Text: ins})
				heads[line.at] = len(ins)
			}
		} else if all {
			head, tail := start, end
			inner := line.body[len(start) : len(line.body)-len(end)]
			if strings.HasPrefix(inner, " ") {
				head += " "
			}
			if len(inner) > 1 && strings.HasSuffix(inner, " ") {
				tail = " " + tail
			}
			edits = append(edits, document.Edit{Start: line.at, End: line.at + len(head)}, document.Edit{Start: line.tail - len(tail), End: line.tail})
		} else if !line.wrapped {
			edits = append(edits, document.Edit{Start: line.at, End: line.at, Text: start + " "}, document.Edit{Start: line.tail, End: line.tail, Text: " " + end})
			heads[line.at] = len(start) + 1
		}
	}
	return e.applyMappedCaretEdits(edits, selections, func(s selectionSpan) SourceSelection {
		shift := func(off int) int { return remapLinkedOffset(off, edits) + heads[off] }
		return SourceSelection{shift(s.Anchor), shift(s.Cursor)}
	})
}
