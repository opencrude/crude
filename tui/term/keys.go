package term

import (
	tea "charm.land/bubbletea/v2"
)

// EncodeKey converts a Bubble Tea key press into PTY bytes (xterm / Ghostty).
func EncodeKey(msg tea.KeyPressMsg) []byte {
	if msg.Text != "" && msg.Mod == 0 {
		return []byte(msg.Text)
	}
	if msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModAlt) && !msg.Mod.Contains(tea.ModShift) {
		if b := ctrlByte(msg); len(b) > 0 {
			return b
		}
	}
	switch msg.Code {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		if msg.Mod.Contains(tea.ModShift) {
			return []byte{0x1b, '[', 'Z'}
		}
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeyUp:
		return csiArrow('A', msg.Mod)
	case tea.KeyDown:
		return csiArrow('B', msg.Mod)
	case tea.KeyRight:
		return csiArrow('C', msg.Mod)
	case tea.KeyLeft:
		return csiArrow('D', msg.Mod)
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	case tea.KeyPgUp:
		return []byte{0x1b, '[', '5', '~'}
	case tea.KeyPgDown:
		return []byte{0x1b, '[', '6', '~'}
	case tea.KeyInsert:
		return []byte{0x1b, '[', '2', '~'}
	case tea.KeyDelete:
		return []byte{0x1b, '[', '3', '~'}
	case tea.KeyF1:
		return []byte{0x1b, 'O', 'P'}
	case tea.KeyF2:
		return []byte{0x1b, 'O', 'Q'}
	case tea.KeyF3:
		return []byte{0x1b, 'O', 'R'}
	case tea.KeyF4:
		return []byte{0x1b, 'O', 'S'}
	case tea.KeyF5:
		return []byte{0x1b, '[', '1', '5', '~'}
	case tea.KeyF6:
		return []byte{0x1b, '[', '1', '7', '~'}
	case tea.KeyF7:
		return []byte{0x1b, '[', '1', '8', '~'}
	case tea.KeyF8:
		return []byte{0x1b, '[', '1', '9', '~'}
	case tea.KeyF9:
		return []byte{0x1b, '[', '2', '0', '~'}
	case tea.KeyF10:
		return []byte{0x1b, '[', '2', '1', '~'}
	case tea.KeyF11:
		return []byte{0x1b, '[', '2', '3', '~'}
	case tea.KeyF12:
		return []byte{0x1b, '[', '2', '4', '~'}
	}
	if msg.Text != "" {
		if msg.Mod.Contains(tea.ModAlt) {
			out := make([]byte, 0, 1+len(msg.Text))
			out = append(out, 0x1b)
			out = append(out, msg.Text...)
			return out
		}
		return []byte(msg.Text)
	}
	if msg.Code >= 32 && msg.Code < 127 && msg.Mod.Contains(tea.ModAlt) {
		return []byte{0x1b, byte(msg.Code)}
	}
	return nil
}

func ctrlByte(msg tea.KeyPressMsg) []byte {
	c := msg.Code
	if c >= 'a' && c <= 'z' {
		return []byte{byte(c - 'a' + 1)}
	}
	if c >= 'A' && c <= 'Z' {
		return []byte{byte(c - 'A' + 1)}
	}
	switch c {
	case '@':
		return []byte{0x00}
	case '[':
		return []byte{0x1b}
	case '\\':
		return []byte{0x1c}
	case ']':
		return []byte{0x1d}
	case '^':
		return []byte{0x1e}
	case '_':
		return []byte{0x1f}
	case ' ':
		return []byte{0x00}
	}
	return nil
}

func csiArrow(final byte, mod tea.KeyMod) []byte {
	// xterm modifier: 1 + shift=1 + alt=2 + ctrl=4
	m := 1
	if mod.Contains(tea.ModShift) {
		m += 1
	}
	if mod.Contains(tea.ModAlt) {
		m += 2
	}
	if mod.Contains(tea.ModCtrl) {
		m += 4
	}
	if m == 1 {
		return []byte{0x1b, '[', final}
	}
	return []byte{0x1b, '[', '1', ';', byte('0' + m), final}
}
