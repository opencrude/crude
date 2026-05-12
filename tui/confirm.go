package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"crude/tui/types"
	"crude/tui/widget"
)

type ConfirmDialog struct {
	dialog     widget.Dialog
	question   string
	operation  string
	tool       string
	target     string
	risk       string
	reason     string
	suggestion string
}

func NewConfirmDialog(theme *widget.Theme) ConfirmDialog {
	buttons := widget.NewButtonGroup(theme,
		widget.Button{Label: "Allow", Key: "y"},
		widget.Button{Label: "Always Allow", Key: "a"},
		widget.Button{Label: "Deny", Key: "n"},
	)
	return ConfirmDialog{
		dialog: widget.NewDialog(theme, "confirmation required", "⚠", buttons),
	}
}

func (m ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, types.ConfirmResult, bool) {
	updated, btnIdx, done := m.dialog.Update(msg)
	m.dialog = updated

	if done {
		choice := types.ConfirmAllow
		switch btnIdx {
		case 0:
			choice = types.ConfirmAllow
		case 1:
			choice = types.ConfirmAlways
		case 2:
			choice = types.ConfirmDeny
		}
		return m, types.ConfirmResult{
			Choice:    choice,
			Operation: m.operation,
			Tool:      m.tool,
			Target:    m.target,
		}, true
	}

	return m, types.ConfirmResult{}, false
}

func (m ConfirmDialog) View(spinFrame int) string {
	var b strings.Builder
	dim := lipgloss.NewStyle().Foreground(appTheme.Colors.Muted)
	warn := lipgloss.NewStyle().Foreground(appTheme.Colors.Warning).Bold(true)

	b.WriteString(warn.Render("  ⚠  confirmation required"))
	b.WriteString("\n")
	b.WriteString(dim.Render("  " + strings.Repeat("─", 54)))
	b.WriteString("\n")

	if m.risk != "" {
		riskColor := appTheme.Colors.Warning
		riskLabel := "medium"
		switch m.risk {
		case "blocked":
			riskColor = lipgloss.Color("#ff0000")
			riskLabel = "BLOCKED"
		case "destructive":
			riskColor = appTheme.Colors.Warning
			riskLabel = "DANGEROUS"
		case "network":
			riskColor = appTheme.Colors.Accent
			riskLabel = "network"
		case "project-write":
			riskColor = lipgloss.Color("#d4a017")
			riskLabel = "write"
		case "unknown":
			riskColor = appTheme.Colors.Muted
			riskLabel = "unknown"
		default:
			riskColor = appTheme.Colors.Muted
			riskLabel = m.risk
		}
		riskStyle := lipgloss.NewStyle().Foreground(riskColor).Bold(true)
		b.WriteString(dim.Render("  risk:      ") + riskStyle.Render(strings.ToUpper(riskLabel)))
		b.WriteString("\n")
	}

	if m.operation != "" {
		opStyle := lipgloss.NewStyle().Foreground(appTheme.Colors.Accent2)
		b.WriteString(dim.Render("  operation: ") + opStyle.Render(m.operation))
		b.WriteString("\n")
	}
	if m.tool != "" {
		b.WriteString(dim.Render("  tool:      ") + appTheme.Styles.AccentLabel.Render(m.tool))
		b.WriteString("\n")
	}
	if m.target != "" {
		targetStyle := lipgloss.NewStyle().Foreground(appTheme.Colors.Warning)
		targetDisplay := m.target
		if len(targetDisplay) > 80 {
			targetDisplay = targetDisplay[:77] + "..."
		}
		b.WriteString(dim.Render("  target:    ") + targetStyle.Render(targetDisplay))
		b.WriteString("\n")
	}

	if m.reason != "" {
		reasonStyle := lipgloss.NewStyle().Foreground(appTheme.Colors.Fg).Italic(true)
		b.WriteString("\n")
		b.WriteString(dim.Render("  reason: ") + reasonStyle.Render(m.reason))
		b.WriteString("\n")
	}

	if m.suggestion != "" {
		suggStyle := lipgloss.NewStyle().Foreground(appTheme.Colors.Accent).Italic(true)
		b.WriteString(dim.Render("  tip: ") + suggStyle.Render(m.suggestion))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dim.Render("  " + m.question))
	b.WriteString("\n\n")

	b.WriteString("  " + m.dialog.View())
	b.WriteString("\n")

	return b.String()
}

func (m *ConfirmDialog) Reset(question, operation, tool, target string) {
	m.question = question
	m.operation = operation
	m.tool = tool
	m.target = target
	m.risk = ""
	m.reason = ""
	m.suggestion = ""
	m.dialog.Reset("confirmation required", "⚠")
}

func (m *ConfirmDialog) ResetWithDetails(question, operation, tool, target, risk, reason, suggestion string) {
	m.Reset(question, operation, tool, target)
	m.risk = risk
	m.reason = reason
	m.suggestion = suggestion
}

func (m *ConfirmDialog) Dismiss() {
	// reset dialog
}
