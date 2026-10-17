package editor

import (
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/rendering"
	"crude/tui/ui"
)

type EditorCloseMsg struct {
	FilePath string
}

type InspectChangeMsg struct {
	Editor   *Editor
	Line     int
	Revision uint64
}

func (e *Editor) HandleEvent(msg tea.Msg) tea.Cmd {
	if !e.open {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		e.landing = nil
		if e.readOnly {
			if ui.IsCopyChord(msg) {
				return rendering.CopyToClipboardCmd(e.SelectedText())
			}
			if msg.Mod.Contains(tea.ModAlt) {
				return nil // Alt+Up/Down are mutating line moves in an editable document.
			}
			if msg.String() == "ctrl+a" {
				line := max(0, e.LineCount()-1)
				e.SetSelection(0, 0, line, e.lineVisualWidth(line))
				return nil
			}
			switch msg.Code {
			case tea.KeyLeft, tea.KeyRight, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown:
			default:
				return nil
			}
		}
		e.cursorBlink.MarkActivity()
		return e.handleKey(msg)
	case tea.PasteMsg:
		if e.readOnly {
			return nil
		}
		e.cursorBlink.MarkActivity()
		e.replacingSelection(func() { e.insertText(msg.Content) })
	case tea.MouseWheelMsg:
		e.cursorBlink.MarkActivity()
		e.handleMouseWheel(msg)
	case tea.MouseClickMsg:
		e.landing = nil
		if msg.Button == tea.MouseLeft && msg.Y >= 0 && msg.Y < e.bodyHeight() {
			gutter := e.gutterGeometry()
			line := e.scrollY + msg.Y
			if msg.X == gutter.railX || msg.X == gutter.gapX {
				if _, _, ok := e.GitHunkAtLine(line); ok {
					revision := e.Revision()
					return func() tea.Msg { return InspectChangeMsg{Editor: e, Line: line, Revision: revision} }
				}
				return nil
			}
			if msg.X == gutter.diagX {
				e.refreshMouseHover(msg.X, msg.Y)
				return nil
			}
			if msg.X >= gutter.numberX && msg.X < gutter.numberX+gutter.numberW && line < e.LineCount() {
				if !msg.Mod.Contains(tea.ModShift) || !e.HasSelection() {
					e.gutterAnchor = line
				}
				e.gutterDrag, e.mouseDragging = true, true
				e.selectGutterLines(line)
				return nil
			}
		}
		e.cursorBlink.MarkActivity()
		e.refreshScrollbarHover(msg.Mouse().X, msg.Mouse().Y)
		e.handleMouseClick(msg)
		e.refreshMouseHover(msg.Mouse().X, msg.Mouse().Y)
	case tea.MouseMotionMsg:
		if e.gutterDrag && e.mouseDragging {
			if msg.Y < 0 {
				e.scrollY = max(0, e.scrollY-1)
			}
			if msg.Y >= e.bodyHeight() {
				e.scrollY = min(max(0, e.LineCount()-e.bodyHeight()), e.scrollY+1)
			}
			e.selectGutterLines(min(e.LineCount()-1, max(0, e.scrollY+msg.Y)))
			return nil
		}
		e.refreshScrollbarHover(msg.Mouse().X, msg.Mouse().Y)
		if e.IsScrollbarDragging() {
			if e.IsHorizontalScrollbarDragging() {
				e.handleHorizontalScrollbarEvent(msg, msg.Mouse().X-e.gutterWidth())
			} else {
				e.handleScrollbarEvent(msg, msg.Mouse().Y)
			}
			e.refreshMouseHover(msg.Mouse().X, msg.Mouse().Y)
			return nil
		}
		// Only extend selection during an active mouse-button drag.
		// Keyboard Shift+Arrow sets selecting without mouseDragging, so bare
		// motion must not treat that as a drag (phantom-drag bug).
		if e.mouseDragging {
			pos, ok := e.mouseToPosition(msg.Mouse().X, msg.Mouse().Y)
			if ok {
				// The caret follows the drag end, so the kept selection
				// continues with Shift+arrows from where the pointer stopped.
				e.selEnd = pos
				e.cursor = pos
			}
		}
		e.refreshMouseHover(msg.Mouse().X, msg.Mouse().Y)
	case tea.MouseReleaseMsg:
		e.gutterDrag = false
		if e.IsHorizontalScrollbarDragging() && e.handleHorizontalScrollbarEvent(msg, msg.Mouse().X-e.gutterWidth()) {
			return nil
		}
		if e.handleScrollbarEvent(msg, msg.Mouse().Y) {
			return nil
		}
		if e.mouseDragging {
			e.mouseDragging = false
			// An editable document keeps the dragged selection for typing,
			// indenting, cutting or Ctrl+C. Only a read-only document is a
			// reading surface that copies on release like the chat transcript.
			if !e.HasSelection() {
				e.ClearSelection()
			} else if e.readOnly {
				text := e.SelectedText()
				e.ClearSelection()
				return rendering.CopyToClipboardCmd(text)
			}
		}
	}
	return nil
}

func (e *Editor) selectGutterLines(line int) {
	first, last := min(line, e.gutterAnchor), max(line, e.gutterAnchor)
	e.selAnchor = Position{Line: first}
	if last+1 < e.LineCount() {
		e.selEnd = Position{Line: last + 1}
	} else {
		e.selEnd = Position{Line: last, Col: e.lineVisualWidth(last)}
	}
	e.cursor = Position{Line: line}
	e.selecting = true
}

func (e *Editor) handleMouseWheel(msg tea.MouseWheelMsg) {
	scrollLines := 3
	bodyH := e.bodyHeight()
	horizontal := msg.Button == tea.MouseWheelLeft || msg.Button == tea.MouseWheelRight || msg.Mod.Contains(tea.ModShift)
	if horizontal {
		e.syncHorizontalScrollChrome(max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth()), bodyH)
		switch msg.Button {
		case tea.MouseWheelLeft, tea.MouseWheelUp:
			e.scrollX -= scrollLines
		case tea.MouseWheelRight, tea.MouseWheelDown:
			e.scrollX += scrollLines
		}
		if e.scrollX < 0 {
			e.scrollX = 0
		}
		if e.hScrollModel != nil && e.scrollX > e.hScrollModel.MaxOffset() {
			e.scrollX = e.hScrollModel.MaxOffset()
		}
		e.hScrollModel.ScrollTo(e.scrollX)
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		e.scrollY -= scrollLines
		if e.scrollY < 0 {
			e.scrollY = 0
		}
	case tea.MouseWheelDown:
		e.scrollY += scrollLines
		maxScroll := e.buf.LineCount() - bodyH
		if maxScroll < 0 {
			maxScroll = 0
		}
		if e.scrollY > maxScroll {
			e.scrollY = maxScroll
		}
	}
	e.syncScrollChrome(bodyH)
}

func (e *Editor) mouseToPosition(mouseX, mouseY int) (Position, bool) {
	sw := e.gutterWidth()
	sbW := e.scrollbarColWidth()
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}
	sbX := sw + textW
	if e.horizontalScrollbarHit(mouseX, mouseY) {
		return Position{}, false
	}
	if e.verticalScrollbarHit(mouseX, mouseY, sbX) {
		return Position{}, false
	}
	lineIdx := e.scrollY + mouseY
	if lineIdx >= e.buf.LineCount() {
		lineIdx = e.buf.LineCount() - 1
	}
	if lineIdx < 0 {
		lineIdx = 0
	}
	col := e.scrollX + mouseX - sw
	if col < 0 {
		col = 0
	}
	lineVisW := e.lineVisualWidth(lineIdx)
	if col > lineVisW {
		col = lineVisW
	}
	return Position{Line: lineIdx, Col: col}, true
}

func (e *Editor) PositionAt(mouseX, mouseY int) (Position, bool) {
	return e.mouseToPosition(mouseX, mouseY)
}

func (e *Editor) handleMouseClick(msg tea.MouseClickMsg) {
	if msg.Button != tea.MouseLeft {
		return
	}
	e.clearGoal()
	mouse := msg.Mouse()
	sw := e.gutterWidth()
	sbW := e.scrollbarColWidth()
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}

	sbX := sw + textW
	if e.horizontalScrollbarHit(mouse.X, mouse.Y) {
		e.handleHorizontalScrollbarEvent(msg, mouse.X-sw)
		return
	}
	if e.verticalScrollbarHit(mouse.X, mouse.Y, sbX) {
		e.handleScrollbarEvent(msg, mouse.Y)
		return
	}

	pos, ok := e.mouseToPosition(mouse.X, mouse.Y)
	if !ok {
		return
	}
	now := time.Now()
	if now.Sub(e.lastClickAt) <= multiClickWindow && pos.Line == e.lastClickPos.Line &&
		max(pos.Col-e.lastClickPos.Col, e.lastClickPos.Col-pos.Col) <= 1 {
		e.clickCount++
	} else {
		e.clickCount = 1
	}
	e.lastClickAt, e.lastClickPos = now, pos
	switch {
	case e.clickCount >= 3:
		e.selectLineAt(pos.Line)
		e.clickCount = 0
		return
	case e.clickCount == 2:
		e.selectWordAt(pos)
		return
	case msg.Mod.Contains(tea.ModShift):
		// Shift+click extends the existing selection (or one anchored at
		// the caret) to the pressed position; dragging keeps extending it.
		if !e.HasSelection() {
			e.selAnchor = e.cursor
		}
	default:
		e.selAnchor = pos
	}
	e.cursor = pos
	e.selEnd = pos
	e.selecting = true
	e.mouseDragging = true
	e.ensureCursorVisible()
}

// multiClickWindow is the press interval that counts as a double/triple click,
// matching the shared text controls.
const multiClickWindow = 500 * time.Millisecond

// selectWordAt selects the identifier, whitespace run or punctuation run
// under pos, leaving the caret at its end.
func (e *Editor) selectWordAt(pos Position) {
	line := strings.TrimRight(e.LineText(pos.Line), "\r\n")
	off := VisualColToByteOffset(line, pos.Col, tabWidth)
	runes := []rune(line)
	idx := len([]rune(line[:min(off, len(line))]))
	if idx >= len(runes) && idx > 0 {
		idx--
	}
	class := func(r rune) int {
		switch {
		case unicode.IsSpace(r):
			return 0
		case isWordChar(r):
			return 1
		}
		return 2
	}
	start, end := idx, idx
	if idx < len(runes) {
		kind := class(runes[idx])
		for start > 0 && class(runes[start-1]) == kind {
			start--
		}
		end = idx + 1
		for end < len(runes) && class(runes[end]) == kind {
			end++
		}
	}
	startCol := ByteOffsetToVisualCol(line, len(string(runes[:start])), tabWidth)
	endCol := ByteOffsetToVisualCol(line, len(string(runes[:end])), tabWidth)
	e.selAnchor = Position{Line: pos.Line, Col: startCol}
	e.selEnd = Position{Line: pos.Line, Col: endCol}
	e.cursor = e.selEnd
	e.selecting = true
	e.mouseDragging = false
	e.ensureCursorVisible()
}

// selectLineAt selects a whole line including its line break.
func (e *Editor) selectLineAt(line int) {
	e.selAnchor = Position{Line: line}
	if line+1 < e.LineCount() {
		e.selEnd = Position{Line: line + 1}
	} else {
		e.selEnd = Position{Line: line, Col: e.lineVisualWidth(line)}
	}
	e.cursor = e.selEnd
	e.selecting = true
	e.mouseDragging = false
	e.ensureCursorVisible()
}

func (e *Editor) verticalScrollbarHit(mouseX, mouseY, sbX int) bool {
	if e.sbCtrl == nil || e.scrollModel == nil || e.scrollModel.MaxOffset() <= 0 {
		return false
	}
	e.sbCtrl.Layout(ui.LayoutContext{}, ui.Rect{X: sbX, W: e.scrollbarColWidth(), H: e.bodyHeight()})
	return e.sbCtrl.PointerHitBounds(e.sbCtrl.Bounds()).Contains(mouseX, mouseY)
}

func (e *Editor) horizontalScrollbarHit(mouseX, mouseY int) bool {
	if !e.hScrollbarVisible || e.hSbCtrl == nil || e.hScrollModel == nil || e.hScrollModel.MaxOffset() <= 0 {
		return false
	}
	return e.hSbCtrl.PointerHitBounds(e.hSbCtrl.Bounds()).Contains(mouseX, mouseY)
}

func (e *Editor) handleKey(key tea.KeyPressMsg) tea.Cmd {
	extend := key.Mod&tea.ModShift != 0
	ctrl := key.Mod&tea.ModCtrl != 0

	switch key.String() {
	case "alt+up":
		e.moveLinesUp()
		return nil
	case "alt+down":
		e.moveLinesDown()
		return nil
	case "ctrl+shift+d", "ctrl+shift+D":
		e.duplicateLineOrSelection()
		return nil
	case "ctrl+shift+k", "ctrl+shift+K":
		e.deleteLines()
		return nil
	case "ctrl+/", "ctrl+_":
		// Ctrl+/ arrives as ctrl+_ (0x1F) on legacy terminal encodings.
		e.toggleLineComment()
		return nil
	}

	switch key.Code {
	case tea.KeyUp:
		e.clearSelectionIfNotExtending(extend)
		if extend {
			e.startSelection()
		}
		e.moveUp()
		if extend && e.selecting {
			e.selEnd = e.cursor
		}
	case tea.KeyDown:
		e.clearSelectionIfNotExtending(extend)
		if extend {
			e.startSelection()
		}
		e.moveDown()
		if extend && e.selecting {
			e.selEnd = e.cursor
		}
	case tea.KeyLeft:
		if ctrl {
			e.clearSelectionIfNotExtending(extend)
			if extend {
				e.startSelection()
			}
			e.moveWordLeft()
			if extend && e.selecting {
				e.selEnd = e.cursor
			}
		} else {
			e.clearSelectionIfNotExtending(extend)
			if extend {
				e.startSelection()
			}
			e.moveLeft()
			if extend && e.selecting {
				e.selEnd = e.cursor
			}
		}
	case tea.KeyRight:
		if ctrl {
			e.clearSelectionIfNotExtending(extend)
			if extend {
				e.startSelection()
			}
			e.moveWordRight()
			if extend && e.selecting {
				e.selEnd = e.cursor
			}
		} else {
			e.clearSelectionIfNotExtending(extend)
			if extend {
				e.startSelection()
			}
			e.moveRight()
			if extend && e.selecting {
				e.selEnd = e.cursor
			}
		}
	case tea.KeyHome:
		e.clearSelectionIfNotExtending(extend)
		if extend {
			e.startSelection()
		}
		if ctrl {
			e.goToDocStart()
		} else {
			e.moveToLineStart()
		}
		if extend && e.selecting {
			e.selEnd = e.cursor
		}
	case tea.KeyEnd:
		e.clearSelectionIfNotExtending(extend)
		if extend {
			e.startSelection()
		}
		if ctrl {
			e.goToDocEnd()
		} else {
			e.moveToLineEnd()
		}
		if extend && e.selecting {
			e.selEnd = e.cursor
		}
	case tea.KeyPgUp:
		e.clearSelectionIfNotExtending(extend)
		if extend {
			e.startSelection()
		}
		e.pageUp()
		if extend && e.selecting {
			e.selEnd = e.cursor
		}
	case tea.KeyPgDown:
		e.clearSelectionIfNotExtending(extend)
		if extend {
			e.startSelection()
		}
		e.pageDown()
		if extend && e.selecting {
			e.selEnd = e.cursor
		}
	case tea.KeyBackspace:
		e.deleteBackwardSmart()
	case tea.KeyDelete:
		if e.HasSelection() {
			e.DeleteSelection()
		} else {
			e.deleteForward()
		}
	case tea.KeyEnter:
		e.replacingSelection(e.insertNewline)
	case tea.KeyTab:
		if extend {
			e.outdentLines()
		} else if e.selectionSpansLines() {
			e.indentLines()
		} else {
			e.insertTab()
		}
	case tea.KeyEsc:
		// Esc dismisses a selection before it means Back.
		if e.HasSelection() {
			e.ClearSelection()
			return nil
		}
		return e.closeCmd()
	default:
		if ui.IsPasteChord(key) {
			// Paste: read from the system clipboard and insert. Bubble Tea's
			// bracketed-paste mode fires a tea.PasteMsg that gets routed here
			// via HandleEvent; terminals that deliver the shortcut as a key
			// press use this owner-preserving fallback.
			return func() tea.Msg {
				data := clipboard.Read(clipboard.FmtText)
				if len(data) == 0 {
					return rendering.ClipboardErrorMsg(ui.ClipboardReadUnavailable())
				}
				return tea.PasteMsg{Content: string(data)}
			}
		}
		if ui.ClipboardChordFor(key) == ui.ClipboardChordCut {
			if e.HasSelection() {
				text := e.SelectedText()
				e.DeleteSelection()
				return rendering.CopyToClipboardCmd(text)
			}
			return nil
		}
		switch key.String() {
		case "ctrl+a":
			e.selectAll()
		default:
			if len(key.Text) > 0 {
				for _, r := range key.Text {
					if r < 32 {
						continue
					}
					if _, opener := autoClosePairs[r]; opener && e.HasSelection() {
						e.typeRune(r) // an opener wraps the selection
						continue
					}
					e.replacingSelection(func() { e.typeRune(r) })
				}
			}
		}
	}
	return nil
}

func (e *Editor) closeCmd() tea.Cmd {
	return func() tea.Msg {
		return EditorCloseMsg{FilePath: e.filePath}
	}
}
