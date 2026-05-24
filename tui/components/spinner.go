package components

import (
	"time"

	"image/color"

	"charm.land/lipgloss/v2"

	"crude/tui/theme"
)

const (
	BlinkPeriod   = 800
	BlinkHalf     = BlinkPeriod / 2
	DotCycleStep  = 300
	SpinnerPeriod = BlinkPeriod
	SpinnerHalf   = SpinnerPeriod / 2
)

func BlinkOn(frame int) bool {
	return frame%BlinkPeriod < BlinkHalf
}

func BlinkOnTime(t time.Time) bool {
	return t.UnixMilli()%BlinkPeriod < BlinkHalf
}

func ThinkingDotIndex(frame int) int {
	return (frame / DotCycleStep) % 4
}

func BlockCursor(frame int, clr color.Color) string {
	if BlinkOn(frame) {
		return lipgloss.NewStyle().Foreground(clr).Render("█")
	}
	return " "
}

func BlockCursorTime(clr color.Color) string {
	if BlinkOnTime(time.Now()) {
		return lipgloss.NewStyle().Foreground(clr).Render("█")
	}
	return " "
}

func BlockCursorSolid(clr color.Color) string {
	return lipgloss.NewStyle().Foreground(clr).Render("█")
}

func SpinnerPhase(frame int) string {
	if BlinkOn(frame) {
		return "●"
	}
	return "○"
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
	theme        *theme.Theme
	frame        int
	phase        Phase
	activityStep int
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

func (m *Spinner) SetActivityStep(step int) {
	m.activityStep = step
}

func (m Spinner) WorkAnimation() string {
	moon := SpinnerPhase(m.frame)

	var label string
	var clr color.Color
	switch m.phase {
	case PhaseGrounding:
		label = m.groundingLabel()
		clr = m.theme.Colors.Spinner.Grounding
	case PhasePlanning:
		label = m.planningLabel()
		clr = m.theme.Colors.Spinner.Planning
	case PhaseExecuting:
		label = m.executingLabel()
		clr = m.theme.Colors.Spinner.Executing
	default:
		label = m.thinkingLabel()
		clr = m.theme.Colors.Spinner.Default
	}

	return lipgloss.NewStyle().Foreground(clr).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Render(label)
}

func (m Spinner) StreamAnimation() string {
	moon := SpinnerPhase(m.frame)
	label := m.streamingLabel()
	return lipgloss.NewStyle().Foreground(m.theme.Colors.Spinner.Planning).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(m.theme.Colors.Fg).Render(label)
}

func (m Spinner) activityIdx(n int) int {
	if n == 0 {
		return 0
	}
	return m.activityStep % n
}

func (m Spinner) thinkingLabel() string {
	labels := []string{"Thinking", "Pondering", "Crunching", "Mulling It Over", "Connecting Dots"}
	return labels[m.activityIdx(len(labels))]
}

func (m Spinner) groundingLabel() string {
	labels := []string{"Gathering Context", "Digging In", "Scanning The Field", "Looking Around", "Getting The Lay Of The Land"}
	return labels[m.activityIdx(len(labels))]
}

func (m Spinner) planningLabel() string {
	labels := []string{"Planning", "Figuring It Out", "Mapping The Route", "Strategizing", "Plotting The Course"}
	return labels[m.activityIdx(len(labels))]
}

func (m Spinner) executingLabel() string {
	labels := []string{"Executing", "Making Moves", "Getting Things Done", "In The Weeds", "Grinding Away"}
	return labels[m.activityIdx(len(labels))]
}

func (m Spinner) streamingLabel() string {
	labels := []string{"Words Flowing In", "Tokens Arriving", "Streaming In", "Pipes Are Full", "Bits On The Wire"}
	return labels[m.activityIdx(len(labels))]
}

func (m Spinner) spinnerColor() color.Color {
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
