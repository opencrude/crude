package ui

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

// HeartbeatDot renders a single dot that animates only while the server is
// actually doing work. When idle, the dot is a static colored shape — the
// eye should not be drawn to it, so a still dot reads as "everything is
// fine" rather than "something is happening right now".
//
// Visual design notes:
//
//   - The glyph never changes. All animation is rendered with the SAME
//     character (●), in the SAME cell, with the SAME baseline. The
//     animation is a brightness pulse (bold ↔ faint), never a shape
//     change. This guarantees zero vertical jumping — the dot stays
//     pinned to the same line as the surrounding text.
//
//   - Earlier iterations used quartered-circle / half-circle glyphs that
//     rotated the filled portion around the cell. While smooth in
//     well-tuned fonts, those glyphs cause the visual center of mass to
//     shift between frames (a top-filled quadrant reads as "higher" than
//     a bottom-filled one), which the eye reads as jitter. Using a
//     single fixed glyph with a brightness pulse is the only way to
//     guarantee pixel-stable animation across all terminals.
//
//   - The pace distinguishes the states. Busy pulses fastest because
//     the user expects an active request to feel urgent; syncing
//     pulses slowest because background indexing should feel calm;
//     starting falls in the middle.
//
//     ready   (idle):     ●  static
//     busy    (working):  ●  bold ↔ faint,  ~200ms pulse
//     syncing (loading):  ●  bold ↔ faint,  ~600ms pulse
//     starting (booting): ●  bold ↔ faint,  ~400ms pulse
//     failed  (broken):   ✕  static red
//
// `state` is one of "ready", "busy", "syncing", "starting", "failed". The
// frame argument is ignored for static states.
func HeartbeatDot(frame int, state string, clr color.Color) string {
	glyph, bold, faint := HeartbeatDotPlain(frame, state)
	style := lipgloss.NewStyle().Foreground(clr)
	if bold {
		style = style.Bold(true)
	}
	if faint {
		style = style.Faint(true)
	}
	return style.Render(glyph)
}

// HeartbeatDotPlain returns the heartbeat glyph and intensity flags without ANSI.
// `state` is one of "ready", "busy", "syncing", "starting", "failed".
func HeartbeatDotPlain(frame int, state string) (glyph string, bold, faint bool) {
	switch state {
	case "busy":
		if (frame/200)%2 == 0 {
			return "●", true, false
		}
		return "●", false, true
	case "syncing":
		if (frame/600)%2 == 0 {
			return "●", true, false
		}
		return "●", false, true
	case "starting":
		if (frame/400)%2 == 0 {
			return "●", true, false
		}
		return "●", false, true
	case "failed":
		return "✕", false, false
	default:
		return "●", false, false
	}
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
	moon, moonClr, label, labelClr := m.WorkPlain()
	return lipgloss.NewStyle().Foreground(moonClr).Render(moon+" ") +
		lipgloss.NewStyle().Foreground(labelClr).Render(label)
}

// WorkPlain returns unstyled spinner glyph + work label for cell paint.
func (m Spinner) WorkPlain() (moon string, moonClr color.Color, label string, labelClr color.Color) {
	moon = SpinnerPhase(m.frame)
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
	return SpinnerPhase(m.frame), m.theme.Colors.Spinner.Planning, m.streamingLabel(), m.theme.Colors.Fg
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
