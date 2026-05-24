package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

type ConfirmDialog struct {
	dialog     components.Dialog
	th         *theme.Theme
	question   string
	operation  string
	tool       string
	target     string
	risk       string
	reason     string
	suggestion string
}

func NewConfirmDialog(th *theme.Theme) ConfirmDialog {
	buttons := components.NewButtonGroup(th,
		components.Button{Label: "Allow", Key: "y", Color: th.Colors.Success},
		components.Button{Label: "Always Allow", Key: "a", Color: th.Colors.Accent},
		components.Button{Label: "Deny", Key: "n", Color: th.Colors.Error, Cancel: true},
	)
	return ConfirmDialog{
		dialog: components.NewDialog(th, "confirmation required", "⚠", buttons),
		th:     th,
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
	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)

	indent := strings.Repeat(" ", m.th.Spacing.FormIndent)

	var details strings.Builder

	if m.risk != "" {
		riskColor := m.th.Colors.Warning
		riskLabel := "medium"
		switch m.risk {
		case "blocked":
			riskColor = lipgloss.Color("#ff0000")
			riskLabel = "BLOCKED"
		case "destructive":
			riskColor = m.th.Colors.Warning
			riskLabel = "DANGEROUS"
		case "network":
			riskColor = m.th.Colors.Accent
			riskLabel = "network"
		case "project-write":
			riskColor = lipgloss.Color("#d4a017")
			riskLabel = "write"
		case "unknown":
			riskColor = m.th.Colors.Muted
			riskLabel = "unknown"
		default:
			riskColor = m.th.Colors.Muted
			riskLabel = m.risk
		}
		riskStyle := lipgloss.NewStyle().Foreground(riskColor).Bold(true)
		details.WriteString(dim.Render(indent+"risk:      ") + riskStyle.Render(strings.ToUpper(riskLabel)))
		details.WriteString("\n")
	}

	if m.operation != "" {
		opStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Accent2)
		details.WriteString(dim.Render(indent+"operation: ") + opStyle.Render(m.operation))
		details.WriteString("\n")
	}
	if m.tool != "" {
		details.WriteString(dim.Render(indent+"tool:      ") + m.th.Styles.AccentLabel.Render(m.tool))
		details.WriteString("\n")
	}
	if m.target != "" {
		targetStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Warning)
		targetDisplay := m.target
		if rendering.VisibleWidth(targetDisplay) > 80 {
			targetDisplay = rendering.TruncateVisual(targetDisplay, 77, "…")
		}
		details.WriteString(dim.Render(indent+"target:    ") + targetStyle.Render(targetDisplay))
		details.WriteString("\n")
	}

	if m.reason != "" {
		reasonStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Fg).Italic(true)
		details.WriteString("\n")
		details.WriteString(dim.Render(indent+"reason: ") + reasonStyle.Render(m.reason))
		details.WriteString("\n")
	}

	if m.suggestion != "" {
		suggStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Italic(true)
		details.WriteString(dim.Render(indent+"tip: ") + suggStyle.Render(m.suggestion))
		details.WriteString("\n")
	}

	details.WriteString("\n")
	details.WriteString(dim.Render(indent + m.question))

	body := details.String()
	m.dialog.SetBody(body)

	return m.dialog.View()
}

func (m *ConfirmDialog) SetWidth(w int) {
	m.dialog.SetWidth(w)
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
	m.dialog.Reset("confirmation required", "⚠")
	m.risk = ""
	m.reason = ""
	m.suggestion = ""
}
