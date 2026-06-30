package components

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/theme"
	"crude/tui/widget"
)

type Button struct {
	Label  string
	Key    string
	Color  color.Color
	Cancel bool
}

// ButtonGroup is a horizontal row of buttons with keyboard AND mouse
// navigation. The keyboard path is unchanged from before mouse support
// was added (←/→/Tab/Enter/Esc). The mouse path lets the user point at
// a button with the cursor and either hover it (highlight) or click
// it (activate).
//
// The visual style is unchanged — buttons still render as
// "❯ [ Label ]  (key)" in focused state and "  [ Label ]  (key)" in
// idle/hover state. Hover adds the theme's hover background colour to
// the idle rendering so the user gets the "live" feel of a GUI
// button bar without changing the visual language.
type ButtonGroup struct {
	theme   *theme.Theme
	buttons []Button
	focused int
	// Hover tracks which button the mouse is currently pointing at
	// (-1 = none). Set by SetHover, consumed by View. Independent
	// from `focused` (the keyboard cursor) — a hovered button is
	// not the same as a focused one. Convention from the rest of
	// the codebase: hover doesn't move the keyboard cursor, it
	// just changes the visual; clicking does both.
	Hover widget.Hover
}

func NewButtonGroup(t *theme.Theme, buttons ...Button) ButtonGroup {
	return ButtonGroup{theme: t, buttons: buttons, Hover: widget.NewHover()}
}

func (m ButtonGroup) Update(msg tea.Msg) (ButtonGroup, int, bool) {
	// ---- Mouse handling ----
	// Mouse click on a button activates it. Mouse motion updates
	// the hover state. We use a single switch so the same Update
	// method handles all the input the ButtonGroup cares about.
	if clickMsg, ok := msg.(tea.MouseClickMsg); ok && clickMsg.Button == tea.MouseLeft {
		if idx := m.Hover.Index; idx >= 0 && idx < len(m.buttons) {
			m.focused = idx
			return m, idx, true
		}
	}
	if motionMsg, ok := msg.(tea.MouseMotionMsg); ok {
		idx := m.buttonAtX(motionMsg.X)
		m.Hover.Set(idx)
		// Returning (m, 0, false) — motion never activates a button.
		return m, 0, false
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, 0, false
	}

	switch keyMsg.Code {
	case tea.KeyLeft:
		if m.focused > 0 {
			m.focused--
		}
		return m, 0, false
	case tea.KeyRight:
		if m.focused < len(m.buttons)-1 {
			m.focused++
		}
		return m, 0, false
	case tea.KeyTab:
		if keyMsg.Mod.Contains(tea.ModShift) {
			if m.focused > 0 {
				m.focused--
			}
		} else if m.focused < len(m.buttons)-1 {
			m.focused++
		}
		return m, 0, false
	case tea.KeyEnter:
		return m, m.focused, true
	case tea.KeyEsc:
		return m, len(m.buttons) - 1, true
	default:
		s := keyMsg.String()
		for i, btn := range m.buttons {
			if s == btn.Key {
				return m, i, true
			}
		}
	}

	return m, 0, false
}

// buttonAtX returns the index of the button that contains the given
// X coordinate (in the ButtonGroup's own coordinate space — i.e.
// starting at X=0 for the leftmost button). Returns -1 if X is past
// the rightmost button.
//
// We compute the width of each button as the rendered visible width
// of "[ Label ]  (key)" plus a 2-cell gap between buttons. The
// focused/hovered/disabled/cancel button renderings all have the
// same visible width for a given label+key, so a single width calc
// suffices.
func (m ButtonGroup) buttonAtX(x int) int {
	if x < 0 || len(m.buttons) == 0 {
		return -1
	}
	// Use the focused button's rendering as the canonical width
	// for every button — all renderings have the same visible
	// width for the same label/key pair.
	w := lipgloss.Width(buttonRender(m.theme, m.buttons[0], true, false))
	gap := 2
	cur := 0
	for i := range m.buttons {
		if x >= cur && x < cur+w {
			return i
		}
		cur += w + gap
	}
	return -1
}

// buttonRender renders a single button. Extracted so the width
// calculation in buttonAtX matches the actual rendered width
// exactly — changing one without the other would cause hit-test
// drift.
func buttonRender(th *theme.Theme, btn Button, focused, hovered bool) string {
	dim := th.Styles.ButtonDim
	hoverBg := th.Colors.Hover

	if focused {
		focusedStyle := lipgloss.NewStyle().Foreground(btn.Color).Bold(true)
		return focusedStyle.Render("❯ [ "+btn.Label+" ]") + "  " + dim.Render("("+btn.Key+")")
	}
	// Hover style: dim text on hover background, no arrow.
	if hovered {
		hovStyle := lipgloss.NewStyle().Background(hoverBg)
		labelStyle := hovStyle
		if btn.Cancel && btn.Color != nil {
			labelStyle = labelStyle.Foreground(btn.Color)
		}
		return hovStyle.Render("  [ ")+labelStyle.Render(btn.Label)+hovStyle.Render(" ]")+"  "+dim.Render("("+btn.Key+")")
	}
	if btn.Cancel && btn.Color != nil {
		cancelStyle := lipgloss.NewStyle().Foreground(btn.Color)
		return dim.Render("  [ ") + cancelStyle.Render(btn.Label) + dim.Render(" ]") + "  " + dim.Render("("+btn.Key+")")
	}
	return dim.Render("  [ "+btn.Label+" ]") + "  " + dim.Render("("+btn.Key+")")
}

func (m ButtonGroup) View() string {
	var btnStrs []string
	for i, btn := range m.buttons {
		focused := i == m.focused
		hovered := m.Hover.Index == i
		btnStrs = append(btnStrs, buttonRender(m.theme, btn, focused, hovered))
	}
	return strings.Join(btnStrs, "  ")
}

func (m ButtonGroup) FocusedIndex() int {
	return m.focused
}

func (m *ButtonGroup) FocusIndex(idx int) {
	if idx >= 0 && idx < len(m.buttons) {
		m.focused = idx
	}
}

// SetHover updates the hover state. Returns true if the value
// changed — the caller (typically the viewmodel) can use this to
// set the dirty flag and avoid re-rendering on every motion
// event that doesn't actually change which button is hovered.
//
// Pass -1 to clear the hover (e.g. when the mouse leaves the
// dialog's bounds).
func (m *ButtonGroup) SetHover(idx int) bool {
	return m.Hover.Set(idx)
}

func indexOf(buttons []Button, btn Button) int {
	for i, b := range buttons {
		if b.Label == btn.Label && b.Key == btn.Key && b.Color == btn.Color && b.Cancel == b.Cancel {
			return i
		}
	}
	return -1
}
