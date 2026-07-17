package ui

import (
	tea "charm.land/bubbletea/v2"
)

// HandleTextEditKey applies navigation and editing keys to ed.
// Enter and Esc are not handled — callers use those for submit/cancel.
// Returns true if the key was consumed as an edit/navigation action.
func HandleTextEditKey(ed *TextEdit, msg tea.KeyPressMsg) bool {
	if ed == nil {
		return false
	}
	extend := msg.Mod.Contains(tea.ModShift)
	ctrl := msg.Mod.Contains(tea.ModCtrl)

	switch msg.Code {
	case tea.KeyLeft:
		if ctrl {
			ed.MoveWordLeft(extend)
		} else {
			ed.MoveLeft(extend)
		}
		return true
	case tea.KeyRight:
		if ctrl {
			ed.MoveWordRight(extend)
		} else {
			ed.MoveRight(extend)
		}
		return true
	case tea.KeyHome:
		ed.MoveHome(extend)
		return true
	case tea.KeyEnd:
		ed.MoveEnd(extend)
		return true
	case tea.KeyBackspace:
		if ctrl {
			ed.DeleteWordBack()
		} else {
			ed.Backspace()
		}
		return true
	case tea.KeyDelete:
		ed.Delete()
		return true
	default:
		if ctrl {
			switch msg.Code {
			case 'a':
				ed.SelectAll()
				return true
			case 'u':
				ed.DeleteToLineStart()
				return true
			case 'k':
				ed.DeleteToLineEnd()
				return true
			case 'w':
				ed.DeleteWordBack()
				return true
			default:
				return false
			}
		}
		if msg.Text != "" {
			ed.InsertRunes(msg.Text)
			return true
		}
		return false
	}
}

// HandleFilterKey is a subset for filter fields that only need
// insert/backspace/delete/word-delete (no selection extend required).
// Arrow keys are left unhandled so list navigation can own them.
func HandleFilterKey(ed *TextEdit, msg tea.KeyPressMsg) bool {
	if ed == nil {
		return false
	}
	ctrl := msg.Mod.Contains(tea.ModCtrl)
	switch msg.Code {
	case tea.KeyBackspace:
		if ctrl {
			ed.DeleteWordBack()
		} else {
			ed.Backspace()
		}
		return true
	case tea.KeyDelete:
		ed.Delete()
		return true
	case tea.KeyHome:
		ed.MoveHome(false)
		return true
	case tea.KeyEnd:
		ed.MoveEnd(false)
		return true
	default:
		if ctrl {
			switch msg.Code {
			case 'a':
				ed.SelectAll()
				return true
			case 'u':
				ed.DeleteToLineStart()
				return true
			case 'w':
				ed.DeleteWordBack()
				return true
			default:
				return false
			}
		}
		// Leave navigation keys for the surrounding list.
		switch msg.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
			tea.KeyPgUp, tea.KeyPgDown, tea.KeyEnter, tea.KeyEsc, tea.KeyTab:
			return false
		}
		if msg.Text != "" {
			ed.InsertRunes(msg.Text)
			return true
		}
		return false
	}
}
