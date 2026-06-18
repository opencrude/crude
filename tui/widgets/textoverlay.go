package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type TextOverlayAction int

const (
	TextOverlayNone TextOverlayAction = iota
	TextOverlaySave
	TextOverlayCancel
)

type TextOverlay struct {
	th      *theme.Theme
	input   components.TextInput
	title   string
	refID   string
	scroll  int
	width   int
	height  int
}

func NewTextOverlay(th *theme.Theme) TextOverlay {
	return TextOverlay{th: th, input: components.NewTextInput(th)}
}

func (m *TextOverlay) Open(refID, title, content string) {
	m.refID = refID
	m.title = title
	m.input.SetValue(content)
	m.scroll = 0
}

func (m *TextOverlay) RefID() string { return m.refID }
func (m *TextOverlay) Value() string { return m.input.Value() }
func (m *TextOverlay) IsOpen() bool  { return m.refID != "" }
func (m *TextOverlay) Close()        { m.refID = ""; m.title = ""; m.input.Clear(); m.scroll = 0 }

func (m TextOverlay) Update(msg tea.Msg) (TextOverlay, tea.Cmd, TextOverlayAction) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if ok {
		switch keyMsg.String() {
		case "ctrl+s":
			return m, nil, TextOverlaySave
		case "ctrl+c":
			return m, nil, TextOverlayCancel
		}
		if keyMsg.Code == tea.KeyEsc {
			return m, nil, TextOverlayCancel
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg, false)
	return m, cmd, TextOverlayNone
}

func (m *TextOverlay) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	lines := m.ViewInner(surf.Width(), surf.Height(), ctx.SpinFrame)
	if len(lines) == 0 {
		return
	}
	rendering.RenderToSurface(surf, strings.Join(lines, "\n"))
}

func (m *TextOverlay) Measure(availW, availH int) (int, int) {
	return availW, availH
}

// ViewInner renders the text overlay content for embedding inside a container
// (e.g., a FloatingWindow).
func (m *TextOverlay) ViewInner(width, height, spinFrame int) []string {
	m.width = width
	m.height = height
	if width < 10 || height < 4 {
		return nil
	}

	// Use full content area — no 72% scaling or box border subtraction.
	innerW := width - 4
	if innerW < 1 {
		innerW = 1
	}
	m.input.SetTextWidth(innerW)
	view := m.input.View(width, spinFrame, false)
	lines := strings.Split(view, "\n")

	maxBody := height - 1 // reserve 1 line for hint
	if maxBody < 3 {
		maxBody = 3
	}
	if len(lines) > maxBody {
		cursorLine := m.input.CursorVisualLine()
		if cursorLine < m.scroll {
			m.scroll = cursorLine
		}
		if cursorLine >= m.scroll+maxBody {
			m.scroll = cursorLine - maxBody + 1
		}
		if m.scroll < 0 {
			m.scroll = 0
		}
		if m.scroll > len(lines)-maxBody {
			m.scroll = len(lines) - maxBody
		}
		lines = lines[m.scroll : m.scroll+maxBody]
	}
	for i, line := range lines {
		lines[i] = rendering.TruncateLine(line, innerW)
	}

	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)

	result := make([]string, 0, height)
	for _, line := range lines {
		result = append(result, "  "+padRight(line, innerW)+"  ")
	}

	hint := "ctrl+s save · esc cancel"
	result = append(result, "  "+dim.Render(padRight(rendering.TruncateLine(hint, innerW), innerW))+"  ")

	// Fill remaining lines with blanks to cover the content area.
	for len(result) < height {
		result = append(result, strings.Repeat(" ", width))
	}

	return result
}

func padRight(s string, width int) string {
	pad := rendering.VisibleWidth(s)
	if pad >= width {
		return s
	}
	return s + strings.Repeat(" ", width-pad)
}
