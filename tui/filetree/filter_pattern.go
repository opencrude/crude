package filetree

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// editFilterPattern applies a key to the tree's in-model filter pattern: the
// type-to-filter text the tree derives rows from when no shell filter field
// owns the query. It is pattern state, not a text control; product text input
// goes through shelltree.FilterField and the picker filter (ui.TextField).
// Arrow keys are left unhandled so tree navigation owns them.
func editFilterPattern(ed *ui.TextEdit, msg tea.KeyPressMsg) bool {
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
