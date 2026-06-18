package widgets

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type YoloDialog struct {
	cursor int
	theme  *theme.Theme
}

func NewYoloDialog(th *theme.Theme) YoloDialog {
	return YoloDialog{
		theme: th,
	}
}

func (m YoloDialog) Update(msg tea.Msg) (YoloDialog, bool, bool) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, false, false
	}

	switch keyMsg.Code {
	case tea.KeyLeft:
		if m.cursor > 0 {
			m.cursor--
		}
		return m, false, false
	case tea.KeyRight:
		if m.cursor < 1 {
			m.cursor++
		}
		return m, false, false
	case tea.KeyTab:
		if keyMsg.Mod.Contains(tea.ModShift) {
			if m.cursor > 0 {
				m.cursor--
			}
		} else if m.cursor < 1 {
			m.cursor++
		}
		return m, false, false
	case tea.KeyEnter:
		return m, m.cursor == 0, true
	case tea.KeyEsc:
		return m, false, true
	default:
		s := keyMsg.String()
		if s == "y" {
			return m, true, true
		} else if s == "n" {
			return m, false, true
		}
	}

	return m, false, false
}

func (m YoloDialog) View() string {
	ws := m.theme.Styles.Window
	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)
	warn := lipgloss.NewStyle().Foreground(m.theme.Colors.Warning)
	danger := lipgloss.NewStyle().Foreground(m.theme.Colors.Error).Bold(true)

	indent := strings.Repeat(" ", m.theme.Spacing.FormIndent)

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(ws.TitleFocused.Render(fmt.Sprintf("\u2620  %s", "YOLO MODE")) + "  " + dim.Render("(esc to cancel)"))
	b.WriteString("\n")
	b.WriteString(dim.Render(strings.Repeat("\u2500", len("YOLO MODE")+4)))
	b.WriteString("\n")

	b.WriteString(warn.Render(indent + "This disables ALL safety checks."))
	b.WriteString("\n")
	b.WriteString(dim.Render(indent + "Your system is vulnerable to:"))
	b.WriteString("\n")
	b.WriteString(dim.Render(indent + "\u2022 accidental file deletion or damage"))
	b.WriteString("\n")
	b.WriteString(dim.Render(indent + "\u2022 prompt injection attacks"))
	b.WriteString("\n")
	b.WriteString(dim.Render(indent + "\u2022 unintended command execution"))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(danger.Render(indent + "Are you absolutely sure?"))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(m.renderButtons(m.cursor))
	b.WriteString("\n")

	return b.String()
}

func (m *YoloDialog) renderButtons(cursor int) string {
	enableStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Error).Bold(true)
	dim := m.theme.Styles.ButtonDim

	var parts []string

	if cursor == 0 {
		parts = append(parts, enableStyle.Render("❯ [ Enable YOLO ]")+"  "+dim.Render("(y)"))
	} else {
		parts = append(parts, dim.Render("  [ Enable YOLO ]")+"  "+dim.Render("(y)"))
	}

	if cursor == 1 {
		cancelStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)
		parts = append(parts, cancelStyle.Render("❯ [ Cancel ]")+"  "+dim.Render("(n)"))
	} else {
		parts = append(parts, dim.Render("  [ Cancel ]")+"  "+dim.Render("(n)"))
	}

	return strings.Join(parts, "  ")
}

func (m YoloDialog) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	ansi := m.View()
	rendering.RenderToSurface(surf, ansi)
}

func (m YoloDialog) Measure(availW, availH int) (int, int) {
	return availW, availH
}

func (m *YoloDialog) SetWidth(w int) {
}

func (m *YoloDialog) Reset() {
	m.cursor = 0
}

func (m *YoloDialog) Dismiss() {
	m.cursor = 0
}
