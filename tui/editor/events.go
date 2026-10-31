package editor

import (
	"time"

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
	defer e.reconcileSnippetSelection()

	switch msg := msg.(type) {
	case PointerScrollMsg:
		return e.handlePointerScroll(msg)
	case tea.KeyPressMsg:
		e.stopPointerSelection()
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
		if isHistoryKey(msg) {
			e.CancelSnippet() // retire on intent, before connected history queues
		}
		if !e.readOnly && !isHistoryKey(msg) && msg.Code != tea.KeyEsc && !ui.IsCopyChord(msg) && !ui.IsPasteChord(msg) {
			if e.DeferDocumentEvent(msg) {
				return nil
			}
			typing := msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 && !e.HasSelection()
			for _, r := range msg.Text {
				if _, pair := autoClosePairs[r]; pair || isPairCloser(r) || r < 32 {
					typing = false
				}
			}
			if typing && e.typingGroup != 0 && time.Since(e.typingAt) < 500*time.Millisecond && e.typingCursor == e.cursor {
				e.undoGroupDepth = 1
				e.undoGroupID = e.typingGroup
				e.startUndoGroup()
			} else {
				e.typingGroup = 0
				e.beginUndoGroup()
			}
			defer func() {
				group := e.undoGroupID
				e.endUndoGroup()
				if typing {
					e.typingGroup, e.typingAt, e.typingCursor = group, time.Now(), e.cursor
				}
			}()
		}
		return e.handleKey(msg)
	case tea.PasteMsg:
		e.stopPointerSelection()
		if e.readOnly {
			return nil
		}
		if e.DeferDocumentEvent(msg) {
			return nil
		}
		e.typingGroup = 0
		e.cursorBlink.MarkActivity()
		e.replacingSelection(func() { e.insertText(msg.Content) })
	case tea.MouseWheelMsg:
		e.cursorBlink.MarkActivity()
		e.handleMouseWheel(msg)
		if e.IsMouseDragging() {
			return e.movePointerSelection(msg.X, msg.Y)
		}
	case tea.MouseClickMsg:
		e.typingGroup = 0
		e.landing = nil
		if msg.Button == tea.MouseLeft {
			e.stopPointerSelection()
			e.sbCtrl.CancelPointerCapture()
			e.hSbCtrl.CancelPointerCapture()
		}
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
				anchor := Position{Line: line}
				if msg.Mod.Contains(tea.ModShift) {
					anchor = e.cursor
					if e.HasSelection() {
						anchor = e.selAnchor
					}
				}
				start, end := e.pointerUnitRange(pointerLine, anchor)
				e.beginPointerSelection(pointerLine, start, end, msg.X, msg.Y)
				return nil
			}
		}
		e.cursorBlink.MarkActivity()
		e.refreshScrollbarHover(msg.Mouse().X, msg.Mouse().Y)
		e.handleMouseClick(msg)
		e.refreshMouseHover(msg.Mouse().X, msg.Mouse().Y)
	case tea.MouseMotionMsg:
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
		if e.IsMouseDragging() {
			e.ClearHover()
			return e.movePointerSelection(msg.X, msg.Y)
		}
		e.refreshMouseHover(msg.Mouse().X, msg.Mouse().Y)
	case tea.MouseReleaseMsg:
		if msg.Button != tea.MouseLeft {
			return nil
		}
		if e.IsHorizontalScrollbarDragging() && e.handleHorizontalScrollbarEvent(msg, msg.Mouse().X-e.gutterWidth()) {
			return nil
		}
		if e.handleScrollbarEvent(msg, msg.Mouse().Y) {
			return nil
		}
		if e.IsMouseDragging() {
			e.drag.x, e.drag.y = msg.X, msg.Y
			e.projectPointerSelection()
			e.stopPointerSelection()
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
	return e.buf.snapPosition(Position{Line: lineIdx, Col: col}), true
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
	kind := pointerCharacter
	start, end := pos, pos
	switch {
	case e.clickCount >= 3:
		kind = pointerLine
		e.clickCount = 0
	case e.clickCount == 2:
		kind = pointerWord
	case msg.Mod.Contains(tea.ModShift):
		// Shift+click extends the existing selection (or one anchored at
		// the caret) to the pressed position; dragging keeps extending it.
		if !e.HasSelection() {
			e.selAnchor = e.cursor
		}
		start = e.selAnchor
	}
	if kind != pointerCharacter {
		start, end = e.pointerUnitRange(kind, pos)
	} else if msg.Mod.Contains(tea.ModShift) {
		end = start // Extending a caret/selection is not a new source-unit hit.
	} else if pos.Col < e.lineVisualWidth(pos.Line) {
		end = e.buf.nextPosition(pos)
	}
	e.beginPointerSelection(kind, start, end, mouse.X, mouse.Y)
}

// multiClickWindow is the press interval that counts as a double/triple click,
// matching the shared text controls.
const multiClickWindow = 500 * time.Millisecond

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
	case "ctrl+z", "super+z":
		e.Undo()
		return nil
	case "ctrl+y", "ctrl+shift+z", "ctrl+shift+Z", "super+shift+z", "super+shift+Z":
		e.Redo()
		return nil
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
		if !extend && !ctrl && e.HasSelection() {
			line, col, _, _ := e.SelectionRange()
			e.cursor = Position{Line: line, Col: col}
			e.ClearSelection()
			e.clearGoal()
			e.ensureCursorVisible()
			break
		}
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
		if !extend && !ctrl && e.HasSelection() {
			_, _, line, col := e.SelectionRange()
			e.cursor = Position{Line: line, Col: col}
			e.ClearSelection()
			e.clearGoal()
			e.ensureCursorVisible()
			break
		}
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
		if ctrl && !e.HasSelection() {
			end := e.cursor
			e.moveWordLeft()
			start := e.cursor
			e.deleteRecorded(start, end, end)
			e.finishEdit()
		} else {
			e.deleteBackwardSmart()
		}
	case tea.KeyDelete:
		if e.HasSelection() {
			e.DeleteSelection()
		} else if ctrl {
			start := e.cursor
			e.moveWordRight()
			end := e.cursor
			e.deleteRecorded(start, end, start)
			e.cursor = start
			e.finishEdit()
		} else {
			e.deleteForward()
		}
	case tea.KeyEnter:
		e.replacingSelection(e.insertNewline)
	case tea.KeyTab:
		if e.navigateSnippet(extend) {
			return nil
		} else if extend {
			e.outdentLines()
		} else if e.HasSelection() {
			e.indentLines()
		} else {
			e.insertTab()
		}
	case tea.KeyEsc:
		if e.SnippetActive() {
			e.CancelSnippet()
			e.ClearSelection()
			return nil
		}
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
					if _, opener := autoClosePairs[r]; opener && e.HasSelection() && !e.SnippetActive() {
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

func isHistoryKey(key tea.KeyPressMsg) bool {
	switch key.String() {
	case "ctrl+z", "super+z", "ctrl+y", "ctrl+shift+z", "ctrl+shift+Z", "super+shift+z", "super+shift+Z":
		return true
	}
	return false
}

func (e *Editor) closeCmd() tea.Cmd {
	return func() tea.Msg {
		return EditorCloseMsg{FilePath: e.filePath}
	}
}
