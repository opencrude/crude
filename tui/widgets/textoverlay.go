package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
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

func (m *TextOverlay) View(width, height, spinFrame int) []string {
	m.width = width
	m.height = height
	boxW := width * 72 / 100
	if boxW > 100 {
		boxW = 100
	}
	if boxW < 32 {
		boxW = 32
	}
	innerW := boxW - 4
	if innerW < 1 {
		innerW = 1
	}
	m.input.SetTextWidth(innerW)
	view := m.input.View(boxW, spinFrame, false)
	lines := strings.Split(view, "\n")
	maxBody := height*68/100 - 4
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
	title := m.title
	if title == "" {
		title = "Edit reference"
	}
	title = lipgloss.NewStyle().Foreground(m.th.Colors.Accent).Bold(true).Render(title)
	return RenderFloatingBox(m.th, rendering.StripAnsi(title), lines, "ctrl+s save · esc cancel", width, height)
}
