package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"image/color"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/widget"
)

type ConfirmDialog struct {
	th         *theme.Theme
	question   string
	operation  string
	tool       string
	target     string
	risk       string
	reason     string
	suggestion string
	width      int
	focused    int
}

func NewConfirmDialog(th *theme.Theme) ConfirmDialog {
	return ConfirmDialog{th: th}
}

func (m ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, types.ConfirmResult, bool) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, types.ConfirmResult{}, false
	}

	switch keyMsg.Code {
	case tea.KeyLeft:
		if m.focused > 0 {
			m.focused--
		}
		return m, types.ConfirmResult{}, false
	case tea.KeyRight:
		if m.focused < 2 {
			m.focused++
		}
		return m, types.ConfirmResult{}, false
	case tea.KeyTab:
		if keyMsg.Mod.Contains(tea.ModShift) {
			if m.focused > 0 {
				m.focused--
			}
		} else if m.focused < 2 {
			m.focused++
		}
		return m, types.ConfirmResult{}, false
	case tea.KeyEnter:
		choice := types.ConfirmAllow
		switch m.focused {
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
	case tea.KeyEsc:
		return m, types.ConfirmResult{
			Choice:    types.ConfirmDeny,
			Operation: m.operation,
			Tool:      m.tool,
			Target:    m.target,
		}, true
	default:
		switch keyMsg.String() {
		case "y":
			return m, types.ConfirmResult{
				Choice:    types.ConfirmAllow,
				Operation: m.operation,
				Tool:      m.tool,
				Target:    m.target,
			}, true
		case "a":
			return m, types.ConfirmResult{
				Choice:    types.ConfirmAlways,
				Operation: m.operation,
				Tool:      m.tool,
				Target:    m.target,
			}, true
		case "n":
			return m, types.ConfirmResult{
				Choice:    types.ConfirmDeny,
				Operation: m.operation,
				Tool:      m.tool,
				Target:    m.target,
			}, true
		}
	}

	return m, types.ConfirmResult{}, false
}

func (m ConfirmDialog) View(spinFrame int) string {
	ws := m.th.Styles.Window
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
			targetDisplay = rendering.TruncateVisual(targetDisplay, 77, "\u2026")
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

	var b strings.Builder

	titleStyle := ws.TitleFocused

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("\u26a0  confirmation required") + "  " + dim.Render("(esc to deny)"))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("\u2500", 24)))
	b.WriteString("\n")

	if body != "" {
		for _, line := range strings.Split(body, "\n") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(m.renderButtons())
	b.WriteString("\n")

	return b.String()
}

func (m ConfirmDialog) renderButtons() string {
	dim := m.th.Styles.ButtonDim

	type btnInfo struct {
		Label  string
		Key    string
		Cancel bool
	}
	buttons := []btnInfo{
		{Label: "Allow", Key: "y"},
		{Label: "Always Allow", Key: "a"},
		{Label: "Deny", Key: "n", Cancel: true},
	}
	colors := []struct {
		focused  color.Color
		unfocused color.Color
	}{
		{focused: m.th.Colors.Success, unfocused: m.th.Colors.Success},
		{focused: m.th.Colors.Accent, unfocused: m.th.Colors.Accent},
		{focused: m.th.Colors.Error, unfocused: m.th.Colors.Error},
	}

	var btnStrs []string
	for i, btn := range buttons {
		c := colors[i]
		if m.focused == i {
			focusedStyle := lipgloss.NewStyle().Foreground(c.focused).Bold(true)
			btnStrs = append(btnStrs, focusedStyle.Render("\u276f [ "+btn.Label+" ]")+"  "+dim.Render("("+btn.Key+")"))
		} else if btn.Cancel {
			cancelStyle := lipgloss.NewStyle().Foreground(c.unfocused)
			btnStrs = append(btnStrs, dim.Render("  [ ")+cancelStyle.Render(btn.Label)+dim.Render(" ]")+"  "+dim.Render("("+btn.Key+")"))
		} else {
			btnStrs = append(btnStrs, dim.Render("  [ "+btn.Label+" ]")+"  "+dim.Render("("+btn.Key+")"))
		}
	}
	return strings.Join(btnStrs, "  ")
}

func (m *ConfirmDialog) SetWidth(w int) {
	m.width = w
}

func (m *ConfirmDialog) Reset(question, operation, tool, target string) {
	m.question = question
	m.operation = operation
	m.tool = tool
	m.target = target
	m.risk = ""
	m.reason = ""
	m.suggestion = ""
	m.focused = 0
}

func (m *ConfirmDialog) ResetWithDetails(question, operation, tool, target, risk, reason, suggestion string) {
	m.Reset(question, operation, tool, target)
	m.risk = risk
	m.reason = reason
	m.suggestion = suggestion
}

func (m *ConfirmDialog) Dismiss() {
	m.risk = ""
	m.reason = ""
	m.suggestion = ""
	m.focused = 0
}

func (m *ConfirmDialog) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	ansi := m.View(ctx.SpinFrame)
	rendering.RenderToSurface(surf, ansi)
}

func (m *ConfirmDialog) Measure(availW, availH int) (int, int) {
	prefW := availW
	if prefW > 80 {
		prefW = 80
	}
	prefH := 16
	if prefH > availH {
		prefH = availH
	}
	return prefW, prefH
}
