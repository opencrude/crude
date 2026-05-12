package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/theme"
)

type Button struct {
	Label string
	Key   string
}

type ButtonGroup struct {
	theme   *theme.Theme
	buttons []Button
	focused int
}

func NewButtonGroup(t *theme.Theme, buttons ...Button) ButtonGroup {
	return ButtonGroup{theme: t, buttons: buttons}
}

func (m ButtonGroup) Update(msg tea.Msg) (ButtonGroup, int, bool) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, 0, false
	}

	switch keyMsg.Type {
	case tea.KeyLeft:
		if m.focused > 0 {
			m.focused--
		}
		return m, 0, false
	case tea.KeyRight, tea.KeyTab:
		if m.focused < len(m.buttons)-1 {
			m.focused++
		}
		return m, 0, false
	case tea.KeyShiftTab:
		if m.focused > 0 {
			m.focused--
		}
		return m, 0, false
	case tea.KeyEnter:
		return m, m.focused, true
	case tea.KeyEsc:
		return m, len(m.buttons) - 1, true
	default:
		s := keyMsg.String()
		for i, btn := range m.buttons {
			if s == btn.Key {
				return m, i, true
			}
		}
	}

	return m, 0, false
}

func (m ButtonGroup) View() string {
	var btnStrs []string
	dim := m.theme.Styles.ButtonDim
	box := m.theme.Styles.ButtonFocus
	normal := m.theme.Styles.Button

	for _, btn := range m.buttons {
		if m.focused == indexOf(m.buttons, btn) {
			btnStrs = append(btnStrs, box.Render(fmt.Sprintf("❯ [ %s ]", btn.Label))+"  "+dim.Render("("+btn.Key+")"))
		} else {
			btnStrs = append(btnStrs, normal.Render(fmt.Sprintf("  [ %s ]", btn.Label))+"  "+dim.Render("("+btn.Key+")"))
		}
	}
	return strings.Join(btnStrs, "  ")
}

func (m ButtonGroup) FocusedIndex() int {
	return m.focused
}

func (m *ButtonGroup) FocusIndex(idx int) {
	if idx >= 0 && idx < len(m.buttons) {
		m.focused = idx
	}
}

func indexOf(buttons []Button, btn Button) int {
	for i, b := range buttons {
		if b.Label == btn.Label && b.Key == btn.Key {
			return i
		}
	}
	return -1
}
