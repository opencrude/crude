package shellterm

import (
	tea "charm.land/bubbletea/v2"
	"crude/terminalvt"
	"crude/tui/ui"
)

type CommandKind uint8

const (
	CommandKey CommandKind = iota
	CommandMouse
	CommandPaste
	CommandFocus
	CommandResize
	CommandReadRows
	CommandTakeControl
)

// Command is an intent, never pre-encoded PTY bytes. The host binds the exact
// service lease and executes it off the UI thread. Repeat defaults to one.
type Command struct {
	ID                   string
	Kind                 CommandKind
	Key                  terminalvt.Key
	Mouse                terminalvt.Mouse
	Paste                []byte
	Focused              bool
	Cols, Rows           int
	Start, Count         int
	Sequence, Generation uint64
	Repeat               int
}

func mods(m tea.KeyMod) terminalvt.Modifiers {
	var out terminalvt.Modifiers
	if m.Contains(tea.ModShift) {
		out |= terminalvt.ModShift
	}
	if m.Contains(tea.ModCtrl) {
		out |= terminalvt.ModCtrl
	}
	if m.Contains(tea.ModAlt) {
		out |= terminalvt.ModAlt
	}
	if m.Contains(tea.ModSuper) {
		out |= terminalvt.ModSuper
	}
	return out
}
func neutralKey(k tea.KeyPressMsg) terminalvt.Key {
	code := k.Code
	from := []rune{tea.KeyEnter, tea.KeyTab, tea.KeyBackspace, tea.KeyEscape, tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown, tea.KeyInsert, tea.KeyDelete, tea.KeySpace, tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6, tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12}
	for i, c := range from {
		if code == c {
			code = terminalvt.KeyEnter + rune(i)
			break
		}
	}
	return terminalvt.Key{Code: code, Text: k.Text, Mod: mods(k.Mod)}
}
func neutralMouse(e ui.Event, x, y int) terminalvt.Mouse {
	out := terminalvt.Mouse{X: x, Y: y}
	var button tea.MouseButton
	var mod tea.KeyMod
	switch e.Kind {
	case ui.EventMouseClick:
		button, mod = e.MouseClick.Button, e.MouseClick.Mod
	case ui.EventMouseRelease:
		out.Action = terminalvt.MouseRelease
		button, mod = e.MouseRelease.Button, e.MouseRelease.Mod
	case ui.EventMouseMotion:
		out.Action = terminalvt.MouseMotion
		button, mod = e.MouseMotion.Button, e.MouseMotion.Mod
	case ui.EventMouseWheel:
		button, mod = e.MouseWheel.Button, e.MouseWheel.Mod
	}
	switch button {
	case tea.MouseLeft:
		out.Button = terminalvt.MouseLeft
	case tea.MouseMiddle:
		out.Button = terminalvt.MouseMiddle
	case tea.MouseRight:
		out.Button = terminalvt.MouseRight
	case tea.MouseWheelUp:
		out.Button = terminalvt.MouseWheelUp
	case tea.MouseWheelDown:
		out.Button = terminalvt.MouseWheelDown
	}
	out.Mod = mods(mod)
	return out
}
