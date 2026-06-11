package editor

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/scrollbar"
)

type CursorBlinkMsg time.Time

type EditorCloseMsg struct {
	FilePath string
}

func (e *Editor) HandleEvent(msg tea.Msg) tea.Cmd {
	if !e.open {
		return nil
	}

	switch msg := msg.(type) {
	case CursorBlinkMsg:
		idle := time.Since(e.lastActivity)
		if idle < cursorBlinkInterval {
			e.blinkActive = false
			e.cursorVisible = true
		} else if !e.blinkActive {
			e.blinkActive = true
			e.cursorVisible = false
			e.lastToggle = time.Now()
		} else if time.Since(e.lastToggle) >= cursorBlinkInterval {
			e.cursorVisible = !e.cursorVisible
			e.lastToggle = time.Now()
		}
		return e.blinkCmd()
	case tea.KeyPressMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		return e.handleKey(msg)
	case tea.PasteMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		e.insertText(msg.Content)
	case tea.MouseWheelMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		e.handleMouseWheel(msg)
	case tea.MouseClickMsg:
		e.cursorVisible = true
		e.blinkActive = false
		e.lastActivity = time.Now()
		e.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		if e.selecting {
			pos, ok := e.mouseToPosition(msg.Mouse().X, msg.Mouse().Y)
			if ok {
				e.selEnd = pos
			}
		}
		sb, cmd := e.scrollbar.Update(msg)
		e.scrollbar = sb
		if cmd != nil && sb.GetScrollY() != e.scrollY {
			e.scrollY = sb.GetScrollY()
		}
	case tea.MouseReleaseMsg:
		sb, cmd := e.scrollbar.Update(msg)
		e.scrollbar = sb
		if sb.GetScrollY() != e.scrollY {
			e.scrollY = sb.GetScrollY()
		}
		if cmd != nil {
			return cmd
		}
		if e.selecting {
			e.selecting = false
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
	bodyH := e.viewH
	if bodyH < 1 {
		bodyH = 1
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
	lc := e.buf.LineCount()
	e.scrollbar.SetTrackHeight(bodyH)
	e.scrollbar.SetScrollInfo(lc, bodyH, e.scrollY)
}

func (e *Editor) mouseToPosition(mouseX, mouseY int) (Position, bool) {
	sw := e.gutterWidth()
	sbW := scrollbar.ScrollbarWidth
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}
	sbX := sw + textW
	if mouseX >= sbX {
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
	sbW := scrollbar.ScrollbarWidth
	textW := e.viewW - sw - sbW
	if textW < 1 {
		textW = 1
	}

	sbX := sw + textW
	if mouse.X >= sbX {
		sb, cmd := e.scrollbar.Update(msg)
		e.scrollbar = sb
		if cmd != nil {
			if sb.GetScrollY() != e.scrollY {
				e.scrollY = sb.GetScrollY()
			}
		}
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
	e.ensureCursorVisible()
}

func (e *Editor) handleKey(key tea.KeyPressMsg) tea.Cmd {
	extend := key.Mod&tea.ModShift != 0
	ctrl := key.Mod&tea.ModCtrl != 0

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
		e.deleteBackward()
	case tea.KeyDelete:
		e.deleteForward()
	case tea.KeyEnter:
		e.insertNewline()
	case tea.KeyTab:
		e.insertTab()
	case tea.KeyEsc:
		e.ClearSelection()
		return e.closeCmd()
	default:
		switch key.String() {
		case "ctrl+s":
			e.save()
		case "ctrl+z":
			e.undo()
		case "ctrl+y":
			e.redo()
		case "ctrl+a":
			e.selectAll()
		default:
			if len(key.Text) > 0 {
				for _, r := range key.Text {
					if r >= 32 {
						e.insertRune(r)
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

func (e *Editor) blinkCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return CursorBlinkMsg(t)
	})
}

func (e *Editor) BlinkCmd() tea.Cmd {
	return e.blinkCmd()
}
