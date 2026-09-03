package editor

import (
	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/rendering"
	"crude/tui/ui"
)

type EditorCloseMsg struct {
	FilePath string
}

func (e *Editor) HandleEvent(msg tea.Msg) tea.Cmd {
	if !e.open {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		e.cursorBlink.MarkActivity()
		return e.handleKey(msg)
	case tea.PasteMsg:
		e.cursorBlink.MarkActivity()
		e.insertText(msg.Content)
	case tea.MouseWheelMsg:
		e.cursorBlink.MarkActivity()
		e.handleMouseWheel(msg)
	case tea.MouseClickMsg:
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
		// Only extend selection during an active mouse-button drag.
		// Keyboard Shift+Arrow sets selecting without mouseDragging, so bare
		// motion must not treat that as a drag (phantom-drag bug).
		if e.mouseDragging {
			pos, ok := e.mouseToPosition(msg.Mouse().X, msg.Mouse().Y)
			if ok {
				e.selEnd = pos
			}
		}
		e.refreshMouseHover(msg.Mouse().X, msg.Mouse().Y)
	case tea.MouseReleaseMsg:
		if e.IsHorizontalScrollbarDragging() && e.handleHorizontalScrollbarEvent(msg, msg.Mouse().X-e.gutterWidth()) {
			return nil
		}
		if e.handleScrollbarEvent(msg, msg.Mouse().Y) {
			return nil
		}
		if e.mouseDragging {
			e.mouseDragging = false
			if e.HasSelection() {
				text := e.SelectedText()
				e.ClearSelection()
				return rendering.CopyToClipboardCmd(text)
			}
			e.ClearSelection()
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
	return Position{Line: lineIdx, Col: col}, true
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
	e.cursor = pos
	e.selAnchor = pos
	e.selEnd = pos
	e.selecting = true
	e.mouseDragging = true
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
		e.insertNewline()
	case tea.KeyTab:
		if extend {
			e.outdentLines()
		} else if e.selectionSpansLines() {
			e.indentLines()
		} else {
			e.insertTab()
		}
	case tea.KeyEsc:
		e.ClearSelection()
		return e.closeCmd()
	default:
		switch key.String() {
		case "ctrl+a":
			e.selectAll()
		case "ctrl+x":
			if e.HasSelection() {
				text := e.SelectedText()
				e.DeleteSelection()
				return rendering.CopyToClipboardCmd(text)
			}
		case "ctrl+v":
			// Paste: read from the system clipboard and insert.
			// Bubble Tea's bracketed-paste mode fires a
			// tea.PasteMsg that gets routed here via HandleEvent
			// (see events.go line ~43), but terminals without
			// bracketed paste send Ctrl+V as a KeyPressMsg —
			// handle both paths uniformly.
			//
			// We use a closure (not rendering.ReadFromClipboardCmd
			// directly) so the editor can re-insert the read text
			// via its own HandleEvent(tea.PasteMsg) path — this
			// keeps the insert/selection-deletion logic in a
			// single place.
			return func() tea.Msg {
				data := clipboard.Read(clipboard.FmtText)
				if len(data) == 0 {
					return rendering.ClipboardErrorMsg("clipboard is empty or unavailable")
				}
				return tea.PasteMsg{Content: string(data)}
			}
		default:
			if len(key.Text) > 0 {
				for _, r := range key.Text {
					if r >= 32 {
						e.typeRune(r)
					}
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
