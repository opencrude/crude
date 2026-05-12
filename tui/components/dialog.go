package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/theme"
)

type Dialog struct {
	theme   *theme.Theme
	title   string
	icon    string
	body    string
	buttons ButtonGroup
}

func NewDialog(t *theme.Theme, title, icon string, buttons ButtonGroup) Dialog {
	return Dialog{theme: t, title: title, icon: icon, buttons: buttons}
}

func (m Dialog) Update(msg tea.Msg) (Dialog, int, bool) {
	updated, btnIdx, done := m.buttons.Update(msg)
	m.buttons = updated
	return m, btnIdx, done
}

func (m Dialog) View() string {
	var b strings.Builder
	dim := m.theme.Styles.DimLabel
	warn := m.theme.Styles.ErrorText

	b.WriteString(warn.Render(fmt.Sprintf("  %s  %s", m.icon, m.title)))
	b.WriteString("\n")
	b.WriteString(dim.Render("  " + strings.Repeat("─", 54)))
	b.WriteString("\n")

	if m.body != "" {
		for _, line := range strings.Split(m.body, "\n") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString("  " + m.buttons.View())
	b.WriteString("\n")
	b.WriteString(dim.Render("  ← → navigate · enter select · esc cancel"))
	b.WriteString("\n")

	return b.String()
}

func (m *Dialog) SetBody(s string) {
	m.body = s
}

func (m *Dialog) SetButtons(buttons ButtonGroup) {
	m.buttons = buttons
}

func (m *Dialog) Reset(title, icon string) {
	m.title = title
	m.icon = icon
	m.body = ""
}
