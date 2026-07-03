package widgets

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type ComposerAction int

const (
	ComposerNone   ComposerAction = iota
	ComposerSubmit
	ComposerCancel
)

type Composer struct {
	th     *theme.Theme
	input  components.TextInput
	open   bool
	scroll int
}

func NewComposer(th *theme.Theme) *Composer {
	return &Composer{
		th:    th,
		input: components.NewTextInput(th),
	}
}

func (c *Composer) Open(initial string) {
	c.open = true
	c.scroll = 0
	if initial != "" {
		c.input.SetValue(initial)
	} else {
		c.input.Clear()
	}
}

func (c *Composer) Close() {
	c.open = false
	c.input.Clear()
	c.scroll = 0
}

func (c *Composer) IsOpen() bool  { return c.open }
func (c *Composer) Value() string { return c.input.Value() }

func (c *Composer) SetValue(s string) { c.input.SetValue(s) }

// Selecting returns true if the user is currently selecting text
// (mouse drag in progress). Used by the viewmodel to suppress
// global key handling while a selection is being made.
func (c *Composer) Selecting() bool { return c.input.IsSelecting() }

// SelectedText returns the currently selected text, or "" if no
// selection. The viewmodel uses this for the Ctrl+C copy action.
func (c *Composer) SelectedText() string { return c.input.SelectedText() }

// SelectionRange returns the (start, end) flat rune indices of the
// selection. -1, -1 if no selection.
func (c *Composer) SelectionRange() (int, int) { return c.input.SelectionRange() }

// HasActiveSelection returns true if there is a non-empty selection
// (even if the user is no longer dragging).
func (c *Composer) HasActiveSelection() bool { return c.input.HasActiveSelection() }

// View returns the rendered composer content for display inside a floating window.
// It shows the current text with scroll indicators and the hint line.
func (c *Composer) View(availW, availH int) string {
	if !c.open {
		return ""
	}
	if availW < 10 || availH < 3 {
		return ""
	}

	inputW := availW - 2
	if inputW < 1 {
		inputW = 1
	}
	c.input.SetTextWidth(inputW)

	raw := c.input.View(availW, 0, false)
	bodyLines := strings.Split(raw, "\n")

	msgH := availH - 1
	if msgH < 1 {
		msgH = 1
	}

	cursorLine := c.input.CursorVisualLine()
	if cursorLine < c.scroll {
		c.scroll = cursorLine
	}
	if cursorLine >= c.scroll+msgH {
		c.scroll = cursorLine - msgH + 1
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
	maxScroll := len(bodyLines) - msgH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if c.scroll > maxScroll {
		c.scroll = maxScroll
	}

	visible := bodyLines
	if len(bodyLines) > msgH {
		visible = bodyLines[c.scroll : c.scroll+msgH]
	}

	dim := lipgloss.NewStyle().Foreground(c.th.Colors.Muted)
	for i, line := range visible {
		visible[i] = " " + line
	}

	if maxScroll > 0 {
		scrollPct := 0
		if maxScroll > 0 {
			scrollPct = c.scroll * 100 / maxScroll
		}
		visible = append(visible, dim.Render(fmt.Sprintf(" %d%%", scrollPct)))
	} else {
		visible = append(visible, "")
	}

	visible[len(visible)-1] = dim.Render(" ctrl+enter submit · esc cancel")

	return strings.Join(visible, "\n")
}

func (c *Composer) Update(msg tea.Msg) (*Composer, tea.Cmd, ComposerAction) {
	if !c.open {
		return c, nil, ComposerNone
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if ok {
		switch {
		case keyMsg.String() == "ctrl+enter":
			return c, nil, ComposerSubmit
		case keyMsg.Code == tea.KeyEnter:
			c.input.InsertString("\n")
			return c, nil, ComposerNone
		case keyMsg.Code == tea.KeyEsc:
			return c, nil, ComposerCancel
		}
	}

	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg, false)
	return c, cmd, ComposerNone
}

func (c *Composer) Measure(availW, availH int) (int, int) {
	return availW, availH
}

func (c *Composer) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	if !c.open {
		return
	}

	w, h := surf.Width(), surf.Height()
	if w < 10 || h < 4 {
		return
	}

	innerW := w - 4
	if innerW < 1 {
		innerW = 1
	}
	c.input.SetTextWidth(innerW)

	view := c.input.View(w, ctx.SpinFrame, false)
	bodyLines := strings.Split(view, "\n")

	contentH := h - 2
	if contentH < 3 {
		contentH = 3
	}

	if len(bodyLines) > contentH {
		cursorLine := c.input.CursorVisualLine()
		if cursorLine < c.scroll {
			c.scroll = cursorLine
		}
		if cursorLine >= c.scroll+contentH {
			c.scroll = cursorLine - contentH + 1
		}
		if c.scroll < 0 {
			c.scroll = 0
		}
		if c.scroll > len(bodyLines)-contentH {
			c.scroll = len(bodyLines) - contentH
		}
		bodyLines = bodyLines[c.scroll : c.scroll+contentH]
	}

	dim := lipgloss.NewStyle().Foreground(c.th.Colors.Muted)
	pad := strings.Repeat(" ", 2)

	var lines []string
	for _, line := range bodyLines {
		lines = append(lines, pad+line+pad)
	}
	lines = append(lines, pad+dim.Render("ctrl+enter submit · esc cancel")+pad)

	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}

	rendering.RenderToSurface(surf, strings.Join(lines, "\n"))
}
