package widgets

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"crude/tui/components"
	"crude/tui/theme"
)

type YoloDialog struct {
	dialog components.Dialog
	theme  *theme.Theme
}

func NewYoloDialog(th *theme.Theme) YoloDialog {
	buttons := components.NewButtonGroup(th,
		components.Button{Label: "Enable YOLO", Key: "y", Color: th.Colors.Error},
		components.Button{Label: "Cancel", Key: "n", Color: th.Colors.Muted, Cancel: true},
	)
	return YoloDialog{
		dialog: components.NewDialog(th, "YOLO MODE", "☠", buttons),
		theme:  th,
	}
}

func (m YoloDialog) Update(msg tea.Msg) (YoloDialog, bool, bool) {
	updated, btnIdx, done := m.dialog.Update(msg)
	m.dialog = updated

	if done {
		return m, btnIdx == 0, true
	}

	return m, false, false
}

func (m YoloDialog) View() string {
	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)
	warn := lipgloss.NewStyle().Foreground(m.theme.Colors.Warning)
	danger := lipgloss.NewStyle().Foreground(m.theme.Colors.Error).Bold(true)

	indent := strings.Repeat(" ", m.theme.Spacing.FormIndent)

	var body strings.Builder
	body.WriteString(warn.Render(indent + "This disables ALL safety checks."))
	body.WriteString("\n")
	body.WriteString(dim.Render(indent + "Your system is vulnerable to:"))
	body.WriteString("\n")
	body.WriteString(dim.Render(indent + "• accidental file deletion or damage"))
	body.WriteString("\n")
	body.WriteString(dim.Render(indent + "• prompt injection attacks"))
	body.WriteString("\n")
	body.WriteString(dim.Render(indent + "• unintended command execution"))
	body.WriteString("\n")
	body.WriteString("\n")
	body.WriteString(danger.Render(indent + "Are you absolutely sure?"))

	m.dialog.SetBody(body.String())
	return m.dialog.View()
}

func (m *YoloDialog) SetWidth(w int) {
	m.dialog.SetWidth(w)
}

func (m *YoloDialog) Reset() {
	m.dialog.Reset("YOLO MODE", "☠")
}

func (m *YoloDialog) Dismiss() {
	// no active state to clear; the App.mode tracks yolo activation
}
