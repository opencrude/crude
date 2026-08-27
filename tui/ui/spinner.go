package ui

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/theme"
)

const (
	// ActivityBlinkCycleMillis is the canonical product activity-dot cycle.
	// spinFrame is wall-clock Unix milliseconds, not a rendered-frame count.
	ActivityBlinkCycleMillis = 800
	ActivityBlinkPhaseMillis = ActivityBlinkCycleMillis / 2
)

func BlinkOn(frame int) bool {
	return frame%ActivityBlinkCycleMillis < ActivityBlinkPhaseMillis
}

func BlinkOnTime(t time.Time) bool {
	return t.UnixMilli()%ActivityBlinkCycleMillis < ActivityBlinkPhaseMillis
}

func BlockCursor(frame int, clr color.Color) string {
	if BlinkOn(frame) {
		return lipgloss.NewStyle().Foreground(clr).Render("█")
	}
	return " "
}

func BlockCursorSolid(clr color.Color) string {
	return lipgloss.NewStyle().Foreground(clr).Render("█")
}

// ActivityDot is the only animated product status glyph. Every active dot uses
// the same wall-clock phase: 400ms filled, then 400ms hollow. Callers own color.
func ActivityDot(frame int) string {
	glyph := "○"
	if BlinkOn(frame) {
		glyph = "●"
	}
	// Filled and hollow circles are not guaranteed to share a terminal width.
	// Reserve their calibrated maximum so the animation never shifts its label.
	target := max(
		ansi.GraphemeWidth.StringWidth("●"),
		ansi.GraphemeWidth.StringWidth("○"),
	)
	width := ansi.GraphemeWidth.StringWidth(glyph)
	return glyph + strings.Repeat(" ", max(0, target-width))
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
	return lipgloss.NewStyle().Foreground(m.spinnerColor()).Render(ActivityDot(m.frame))
}

func (m *Spinner) SetPhase(p Phase) {
	m.phase = p
}

func (m *Spinner) SetFrame(f int) {
	m.frame = f
}

func (m Spinner) WorkAnimation() string {
	moon, moonClr, label, labelClr := m.WorkPlain()
	return lipgloss.NewStyle().Foreground(moonClr).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(labelClr).Render(label)
}

// WorkPlain returns unstyled spinner glyph + work label for cell paint.
func (m Spinner) WorkPlain() (moon string, moonClr color.Color, label string, labelClr color.Color) {
	moon = ActivityDot(m.frame)
	labelClr = m.theme.Colors.Fg
	switch m.phase {
	case PhaseGrounding:
		return moon, m.theme.Colors.Spinner.Grounding, m.groundingLabel(), labelClr
	case PhasePlanning:
		return moon, m.theme.Colors.Spinner.Planning, m.planningLabel(), labelClr
	case PhaseExecuting:
		return moon, m.theme.Colors.Spinner.Executing, m.executingLabel(), labelClr
	default:
		return moon, m.theme.Colors.Spinner.Default, m.thinkingLabel(), labelClr
	}
}

func (m Spinner) StreamAnimation() string {
	moon, moonClr, label, labelClr := m.StreamPlain()
	return lipgloss.NewStyle().Foreground(moonClr).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(labelClr).Render(label)
}

// StreamPlain returns unstyled streaming spinner parts for cell paint.
func (m Spinner) StreamPlain() (moon string, moonClr color.Color, label string, labelClr color.Color) {
	return ActivityDot(m.frame), m.theme.Colors.Spinner.Planning, m.streamingLabel(), m.theme.Colors.Fg
}

func (m Spinner) thinkingLabel() string {
	return "Thinking"
}

func (m Spinner) groundingLabel() string {
	return "Gathering context"
}

func (m Spinner) planningLabel() string {
	return "Planning"
}

func (m Spinner) executingLabel() string {
	return "Executing"
}

func (m Spinner) streamingLabel() string {
	return "Receiving response"
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
