package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/tui/ui"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// caretEdit is one original-source edit plus relative post-edit selection.
// Every range is planned against the same source before the sole batch writer.
type caretEdit struct {
	document.Edit
	anchor, cursor int
	primary        bool
	// stop is the plan's VS Code undo stop (undo_stops.go); typing,
	// deletion and Enter planners set it.
	stop undoStop
}

func replacement(a, b int, text string) caretEdit {
	return caretEdit{Edit: document.Edit{Start: a, End: b, Text: text}, anchor: len(text), cursor: len(text)}
}
func selectionNotice(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return func() tea.Msg { return SelectionNoticeMsg{Message: err.Error()} }
}

func (e *Editor) applyCaretEdits(plans []caretEdit) error {
	slices.SortFunc(plans, func(a, b caretEdit) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return a.End - b.End
	})
	var unique []document.Edit
	for _, p := range plans {
		if len(unique) > 0 {
			prior := &unique[len(unique)-1]
			if p.Edit == *prior {
				continue
			}
			if p.Start < prior.End || p.Start == prior.Start {
				if p.Text == "" && prior.Text == "" {
					prior.End = max(prior.End, p.End)
					continue
				}
				return fmt.Errorf("these edits overlap; press Esc to use one caret")
			}
		}
		unique = append(unique, p.Edit)
	}
	e.buf.ensureCache()
	size := len(e.buf.cachedBytes)
	edits := unique
	for _, p := range edits {
		size += len(p.Text) - (p.End - p.Start)
	}
	if size > document.MaxBytes || size < 0 {
		return document.ErrLimit
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
	next := make([]selectionSpan, 0, len(plans))
	for _, p := range plans {
		start := remapLinkedOffset(p.Start, edits)
		a, b := start+p.anchor, start+p.cursor
		snap := func(off int) int {
			pos := e.buf.positionAtOffset(off)
			if e.buf.positionToOffset(pos) < off {
				pos = e.buf.nextPosition(pos)
			}
			return e.buf.positionToOffset(pos)
		}
		next = append(next, selectionSpan{SourceSelection: SourceSelection{snap(a), snap(b)}, primary: p.primary})
	}
	if err := e.setSelections(next); err != nil {
		return err
	}
	e.finishSimpleEdit()
	return nil
}
func (e *Editor) multipleReplace(text string) error {
	var plans []caretEdit
	for _, s := range e.selectionSpans() {
		a, b := s.bounds()
		p := replacement(a, b, text)
		p.primary = s.primary
		plans = append(plans, p)
	}
	return e.applyCaretEdits(plans)
}
func (e *Editor) multipleKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	if !e.HasMultipleCarets() {
		return nil, false
	}
	if key.Code == tea.KeyEsc {
		e.clearSecondary()
		return nil, true
	}
	if isHistoryKey(key) {
		return nil, false
	}
	if ui.IsCopyChord(key) {
		return e.copyCmd(), true
	}
	if ui.ClipboardChordFor(key) == ui.ClipboardChordCut {
		if e.readOnly {
			return nil, true
		}
		return e.cutCmd(), true
	}
	if ui.IsPasteChord(key) {
		return nil, false
	}
	switch key.Code {
	case tea.KeyLeft, tea.KeyRight, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown:
		return e.multipleMotion(func() { _ = e.handleKey(key) }), true
	}
	if key.String() == "ctrl+a" {
		e.clearSecondary()
		return nil, false
	}
	if e.readOnly {
		return nil, true
	}
	if key.Code == tea.KeyTab && (key.Mod == 0 || key.Mod == tea.ModShift) && (e.HasSelection() || key.Mod == tea.ModShift) {
		delta := e.indentStyle().size
		if key.Mod == tea.ModShift {
			delta = -delta
		}
		return selectionNotice(e.multipleIndent(delta)), true
	}
	ctrl := key.Mod == tea.ModCtrl
	printable := key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0
	if !printable && key.Code != tea.KeyBackspace && key.Code != tea.KeyDelete && key.Code != tea.KeyEnter && key.Code != tea.KeyTab {
		return nil, true
	}
	var plans []caretEdit
	e.buf.ensureCache()
	for _, s := range e.selectionSpans() {
		a, b := s.bounds()
		pos := e.buf.positionAtOffset(a)
		p := replacement(a, b, "")
		switch {
		case printable:
			text := strings.Map(func(r rune) rune {
				if r < 32 {
					return -1
				}
				return r
			}, key.Text)
			p = e.typedCaretEdit(s.SourceSelection, text)
		case key.Code == tea.KeyEnter:
			p = e.newlineCaretEdit(a, b)
		case key.Code == tea.KeyTab:
			style := e.indentStyle()
			p = replacement(a, b, style.padding(pos.Col, pos.Col+style.size-pos.Col%style.size))
		case a != b: // ordinary selection deletion
		case ctrl:
			old := e.captureViewState()
			e.cursor = pos
			if key.Code == tea.KeyBackspace {
				e.moveWordLeft()
				p.Start = e.buf.positionToOffset(e.cursor)
			} else {
				e.moveWordRight()
				p.End = e.buf.positionToOffset(e.cursor)
			}
			e.restoreViewState(old)
		case key.Code == tea.KeyBackspace:
			p = e.backwardCaretEdit(pos, true)
		case key.Code == tea.KeyDelete:
			p.End = e.buf.positionToOffset(e.buf.nextPosition(pos))
		}
		p.primary = s.primary
		plans = append(plans, p)
	}
	return selectionNotice(e.applyCaretEdits(plans)), true
}

// Motion reuses the scalar geometry owner, including wraps, inlays and goals.
// No document mutation is possible while this dispatcher visits private ranges.
func (e *Editor) multipleMotion(move func()) tea.Cmd {
	spans := e.selectionSpans()
	saved := e.captureViewState()

	e.multiDispatch = true
	for i, s := range spans {
		e.cursor = e.buf.positionAtOffset(s.Cursor)
		e.selAnchor = e.buf.positionAtOffset(s.Anchor)
		e.selEnd = e.cursor
		e.selecting = s.Anchor != s.Cursor
		e.goalCol, e.goalSet, e.wrapBefore = s.goal.col, s.goal.set, s.goal.before
		move()
		spans[i].SourceSelection = e.primaryRange()
		spans[i].goal = caretGoal{e.goalCol, e.goalSet, e.wrapBefore}
	}
	e.multiDispatch = false
	e.restoreViewState(saved)
	err := e.setSelections(spans)
	e.ensureCursorVisible()
	return selectionNotice(err)
}
func (e *Editor) multipleIndent(delta int) error {
	spans := e.selectionSpans()
	lines := map[int]bool{}
	for _, s := range spans {
		a, b := s.bounds()
		first, last := e.buf.positionAtOffset(a), e.buf.positionAtOffset(b)
		if last.Line > first.Line && last.Col == 0 {
			last.Line--
		}
		for line := first.Line; line <= last.Line; line++ {
			lines[line] = true
		}
	}
	first, last := e.LineCount(), 0
	for line := range lines {
		first = min(first, line)
		last = max(last, line)
	}
	return e.applyIndentation(first, last, delta, e.indentStyle(), lines)
}

// These recipes are shared by scalar and multiple-caret editing. They compute
// bytes and the post-edit caret without moving or mutating the live document.
func (e *Editor) newlineCaretEdit(start, end int) caretEdit {
	return e.smartNewline(start, end)
}
func (e *Editor) backwardCaretEdit(pos Position, smart bool) caretEdit {
	end := e.buf.positionToOffset(pos)
	start := e.buf.positionToOffset(e.buf.previousPosition(pos))
	p := replacement(start, end, "")
	old := e.cursor
	e.cursor = pos
	defer func() { e.cursor = old }()
	if smart {
		if closer, ok := autoClosePairs[e.runeBeforeCursor()]; ok && e.runeAtCursor() == closer {
			p.End = e.buf.positionToOffset(e.buf.nextPosition(pos))
			p.stop = undoStop{op: editDeletingLeft, before: true} // _runAutoClosingPairDelete
			return p
		}
	}
	raw := e.LineText(pos.Line)
	off := VisualColToByteOffset(raw, pos.Col, e.TabWidth())
	if pos.Col > 0 && strings.Trim(raw[:off], " \t") == "" {
		style := e.indentStyle()
		target := ((pos.Col - 1) / style.size) * style.size
		prefix := leadingWhitespace(raw)
		begin := e.buf.positionToOffset(Position{Line: pos.Line})
		if strings.Contains(prefix, "\t") {
			width := LineVisualWidth(prefix, e.TabWidth()) - (pos.Col - target)
			left := style.padding(0, target)
			p = replacement(begin, begin+len(prefix), left+style.padding(target, width))
			p.anchor, p.cursor = len(left), len(left)
		} else {
			p.Start = e.buf.positionToOffset(Position{Line: pos.Line, Col: target})
		}
	}
	p.stop = e.deleteUndoStop(editDeletingLeft, p.Start, p.End)
	return p
}
func (e *Editor) applySingleCaretEdit(p caretEdit) {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	if err := e.spliceRecordedBatch([]document.Edit{p.Edit}, e.cursor); err != nil {
		return
	}
	e.cursor = e.buf.positionAtOffset(p.Start + p.cursor)
	e.finishSimpleEdit()
}

func (e *Editor) typedCaretEdit(s SourceSelection, text string) caretEdit {
	a, b := s.bounds()
	p := replacement(a, b, text)
	r, n := utf8.DecodeRuneInString(text)
	if n != len(text) || n == 0 {
		return p
	}
	p.stop = e.simpleTypingStop(text) // also AutoClosingOvertypeOperation
	closer, opener := autoClosePairs[r]
	if opener && a != b {
		p.Text = string(r) + string(e.buf.cachedBytes[a:b]) + string(closer)
		p.anchor = len(string(r))
		p.cursor = len(p.Text) - len(string(closer))
		p.stop = atomicUndoStop // SurroundSelectionOperation
		return p
	}
	if a == b {
		old := e.cursor
		e.cursor = e.buf.positionAtOffset(a)
		if isPairCloser(r) && e.runeAtCursor() == r {
			p = replacement(a, a+len(string(r)), string(r))
			p.stop = e.simpleTypingStop(text)
		} else if opener && e.shouldAutoClose(r) {
			p.Text = string(r) + string(closer)
			p.anchor, p.cursor = len(string(r)), len(string(r))
			p.stop = undoStop{op: editTypingOther, before: true} // AutoClosingOpenCharTypeOperation
		}
		e.cursor = old
	}
	if p.Text == text {
		if dedented := e.dedentTyped(p, text); dedented.Edit != p.Edit {
			p = dedented
			p.stop = undoStop{op: editTypingOther, before: true} // AutoIndentOperation
		}
	}
	return p
}
