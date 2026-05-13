package components

import (
	"github.com/charmbracelet/lipgloss"

	"crude/tui/theme"
)

const (
	BlinkPeriod    = 800
	BlinkHalf      = BlinkPeriod / 2
	DotCycleStep   = 300
	SpinnerPeriod  = BlinkPeriod
	SpinnerHalf    = SpinnerPeriod / 2
)

func BlinkOn(frame int) bool {
	return frame%BlinkPeriod < BlinkHalf
}

func SpinnerPhase(frame int) string {
	if BlinkOn(frame) {
		return "●"
	}
	return "○"
}

func ThinkingDotIndex(frame int) int {
	return (frame / DotCycleStep) % 4
}

func BlockCursor(frame int, color lipgloss.Color) string {
	if BlinkOn(frame) {
		return lipgloss.NewStyle().Foreground(color).Render("█")
	}
	return " "
}

func BlockCursorSolid(color lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(color).Render("█")
}

type Phase string

const (
	PhaseGrounding Phase = "grounding"
	PhasePlanning  Phase = "planning"
	PhaseExecuting Phase = "executing"
	PhaseStreaming Phase = "streaming"
	PhaseDefault   Phase = ""
)

type Spinner struct {
	theme *theme.Theme
	frame int
	phase Phase
}

func NewSpinner(t *theme.Theme) Spinner {
	return Spinner{theme: t, phase: PhaseDefault}
}

func (m Spinner) View() string {
	return lipgloss.NewStyle().Foreground(m.spinnerColor()).Render(SpinnerPhase(m.frame))
}

func (m *Spinner) SetPhase(p Phase) {
	m.phase = p
}

func (m *Spinner) SetFrame(f int) {
	m.frame = f
}

func (m Spinner) WorkAnimation() string {
	moon := SpinnerPhase(m.frame)

	var label string
	var color lipgloss.Color
	switch m.phase {
	case PhaseGrounding:
		label = "gathering context"
		color = m.theme.Colors.Spinner.Grounding
	case PhasePlanning:
		label = "planning"
		color = m.theme.Colors.Spinner.Planning
	case PhaseExecuting:
		label = "executing"
		color = m.theme.Colors.Spinner.Executing
	default:
		label = "thinking"
		color = m.theme.Colors.Spinner.Default
	}

	return lipgloss.NewStyle().Foreground(color).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Render(label)
}

func (m Spinner) StreamAnimation() string {
	moon := SpinnerPhase(m.frame)
	return lipgloss.NewStyle().Foreground(m.theme.Colors.Spinner.Planning).Render(moon + " streaming")
}

func (m Spinner) spinnerColor() lipgloss.Color {
	switch m.phase {
	case PhaseGrounding:
		return m.theme.Colors.Spinner.Grounding
	case PhasePlanning:
		return m.theme.Colors.Spinner.Planning
	case PhaseExecuting:
		return m.theme.Colors.Spinner.Executing
	case PhaseStreaming:
		return m.theme.Colors.Spinner.Planning
	case PhaseDefault:
		return m.theme.Colors.Spinner.Default
	}
	return m.theme.Colors.Spinner.Default
}
