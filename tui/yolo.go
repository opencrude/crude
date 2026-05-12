package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"crude/tui/widget"
)

type YoloDialog struct {
	dialog widget.Dialog
}

func NewYoloDialog(theme *widget.Theme) YoloDialog {
	buttons := widget.NewButtonGroup(theme,
		widget.Button{Label: "Enable YOLO", Key: "y"},
		widget.Button{Label: "Cancel", Key: "n"},
	)
	return YoloDialog{
		dialog: widget.NewDialog(theme, "YOLO MODE", "☠", buttons),
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
	dim := lipgloss.NewStyle().Foreground(appTheme.Colors.Muted)
	danger := appTheme.Styles.DangerText

	var b strings.Builder

	b.WriteString(danger.Render("  ☠  YOLO MODE"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  " + strings.Repeat("─", 54)))
	b.WriteString("\n")
	b.WriteString(dim.Render("  This is DANGEROUS. Safety checks protect"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  your system from accidental damage and"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  prompt injection attacks."))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(danger.Render("  Are you absolutely sure?"))
	b.WriteString("\n\n")

	b.WriteString("  " + m.dialog.View())
	b.WriteString("\n")

	return b.String()
}

func (m *YoloDialog) Reset() {
	m.dialog.Reset("YOLO MODE", "☠")
}

func (m *YoloDialog) Dismiss() {
}
