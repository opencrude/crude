package components

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

type Dialog struct {
	theme   *theme.Theme
	title   string
	icon    string
	body    string
	buttons ButtonGroup
	width   int
}

func NewDialog(t *theme.Theme, title, icon string, buttons ButtonGroup) Dialog {
	return Dialog{theme: t, title: title, icon: icon, buttons: buttons}
}

func (m Dialog) Update(msg tea.Msg) (Dialog, int, bool) {
	// Forward all messages to the ButtonGroup. The ButtonGroup
	// knows how to handle mouse motion (hover update) and mouse
	// click (activate button), in addition to keyboard navigation.
	// This is the "single source of truth" approach: the dialog
	// doesn't need to know about mouse vs keyboard — it just
	// forwards to the only interactive element (the buttons).
	//
	// Coordinate translation: the ButtonGroup's X axis starts at
	// the dialog's left padding, not at the screen origin. The
	// dialog is responsible for offsetting the X coordinate before
	// passing it on. But because the dialog has no real way to
	// know the screen X of its own body (it's rendered inline in
	// the agent's overlay, with whatever padding the parent uses),
	// we forward the raw X and rely on the caller (the overlay
	// tab dispatcher) to translate. The dialog's only X-dependent
	// operation is the ButtonGroup hover, which uses the raw X
	// relative to the button row's own start — that offset is
	// already applied by the time the dialog sees the message
	// in practice, because the parent translates screen X to
	// dialog-local X before calling Update.
	updated, btnIdx, done := m.buttons.Update(msg)
	m.buttons = updated
	return m, btnIdx, done
}

func (m Dialog) View() string {
	var b strings.Builder
	dim := m.theme.Styles.DimLabel
	warn := m.theme.Styles.ErrorText

	// Title line + short separator
	b.WriteString("\n")
	b.WriteString(warn.Render(fmt.Sprintf("%s  %s", m.icon, m.title)))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("─", len(m.title)+4)))
	b.WriteString("\n")

	if m.body != "" {
		for _, line := range strings.Split(m.body, "\n") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(m.buttons.View())
	b.WriteString("\n")

	return b.String()
}

func (m *Dialog) SetWidth(w int) {
	m.width = w
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
