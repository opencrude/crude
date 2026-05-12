package chat

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	"crude/tui/widget"
)

type Model struct {
	theme       *widget.Theme
	messages    []ChatMessage
	offset      int
	scrolled    bool
	width       int
	height      int
	modelName   string
	selecting   bool
	selAnchorLine int
	selAnchorCol  int
	selEndLine    int
	selEndCol     int
	selAutoScroll bool
	cachedLines []string
	cachedWidth int
	mdRenderer  *glamour.TermRenderer
	spinner     widget.Spinner
	streaming   bool
	scrollTimes []time.Time
}

func NewModel(t *widget.Theme) Model {
	return Model{
		theme:   t,
		spinner: widget.NewSpinner(t),
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyPgUp:
			m.ScrollUp(m.height)
		case tea.KeyPgDown:
			m.ScrollDown(m.height)
		case tea.KeyHome:
			m.offset = 1 << 30
			m.scrolled = true
		case tea.KeyEnd:
			m.ScrollToBottom()
		}
	case tea.MouseMsg:
		now := time.Now()
		cutoff := now.Add(-300 * time.Millisecond)
		var recent []time.Time
		for _, t := range m.scrollTimes {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		recent = append(recent, now)
		m.scrollTimes = recent
		n := 3
		if len(recent) > 3 {
			n = 6
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.ScrollUp(n)
		case tea.MouseButtonWheelDown:
			m.ScrollDown(n)
		}
	}
	return m, nil
}

func (m Model) View() string {
	return ""
}

func (m *Model) AppendMessage(msg ChatMessage) {
	m.messages = append(m.messages, msg)
}

func (m *Model) AppendToken(token string) {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	if last.Role != RoleAssistant {
		return
	}
	last.Content += token
}

func (m *Model) AppendThinking(s string) {
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.Role == RoleThinking {
			last.Content += s
			return
		}
	}
	m.messages = append(m.messages, ChatMessage{
		Role:          RoleThinking,
		Content:       s,
		ThinkingActive: true,
	})
}

func (m *Model) UpdateToolStreaming(toolID, name string, data map[string]any) {
	for i := range m.messages {
		if m.messages[i].ToolID == toolID {
			msg := &m.messages[i]
			if v, ok := data["content"]; ok {
				if s, ok := v.(string); ok {
					msg.Content = s
				}
			}
			if v, ok := data["tool_est_tokens"]; ok {
				if n, ok := v.(float64); ok {
					msg.ToolEstTokens = int(n)
				}
			}
			if v, ok := data["lived_added"]; ok {
				if n, ok := v.(float64); ok {
					msg.ToolLivedAdded = int(n)
				}
			}
			if v, ok := data["lived_removed"]; ok {
				if n, ok := v.(float64); ok {
					msg.ToolLivedRemoved = int(n)
				}
			}
			return
		}
	}
}

func (m *Model) UpdateToolCall(toolID, name, args string) {
	for i := range m.messages {
		if m.messages[i].ToolID == toolID {
			msg := &m.messages[i]
			msg.ToolName = name
			msg.ToolArgs = args
			return
		}
	}
}

func (m *Model) UpdateToolProgress(toolID, name, phase, message string) {
	for i := range m.messages {
		if m.messages[i].ToolID == toolID || m.messages[i].ToolName == name {
			msg := &m.messages[i]
			msg.ToolProgressPhase = phase
			msg.ToolProgressMessage = message
			return
		}
	}
}

func (m *Model) UpdateToolResult(toolID, name, result string, isError bool) {
	for i := range m.messages {
		if m.messages[i].ToolID == toolID {
			msg := &m.messages[i]
			msg.Content = result
			if isError {
				msg.ToolStatus = "error"
			} else {
				msg.ToolStatus = "success"
			}
			if msg.ToolStartTime.IsZero() || msg.ToolStartTime.After(time.Now()) {
				msg.ToolDuration = 0
			} else {
				msg.ToolDuration = time.Since(msg.ToolStartTime)
			}
			return
		}
	}
}

func (m *Model) ClearStreamingTools() {
	var keep []ChatMessage
	for _, msg := range m.messages {
		if msg.ToolStatus == "streaming" || msg.ToolStatus == "calling" {
			continue
		}
		keep = append(keep, msg)
	}
	m.messages = keep
}

func (m *Model) SetModelName(name string) {
	m.modelName = name
}

func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *Model) ScrollToBottom() {
	m.scrolled = false
	m.offset = 0
}

func (m *Model) MarkAllThinkingDone() {
	for i := range m.messages {
		if m.messages[i].Role == RoleThinking {
			m.messages[i].ThinkingActive = false
		}
	}
}

func (m *Model) InvalidateCache() {
	m.cachedLines = nil
	m.cachedWidth = 0
}

func (m *Model) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if m.selAnchorLine < m.selEndLine ||
		(m.selAnchorLine == m.selEndLine && m.selAnchorCol <= m.selEndCol) {
		return m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol
	}
	return m.selEndLine, m.selEndCol, m.selAnchorLine, m.selAnchorCol
}

func (m *Model) MouseToAllLine(mouseY int) (allIdx int, ok bool) {
	return 0, false
}

func (m *Model) ChatAreaContentBounds() (firstLine, lastLine int) {
	return 0, 0
}

func (m *Model) Messages() []ChatMessage {
	return m.messages
}

func (m *Model) ScrollUp(n int) {
	m.offset += n
	m.scrolled = true
}

func (m *Model) ScrollDown(n int) {
	m.offset -= n
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset == 0 {
		m.scrolled = false
	}
}

func (m *Model) Scrolled() bool {
	return m.scrolled
}

func (m *Model) ChatOffset() int {
	return m.offset
}

func (m *Model) MdRenderer() *glamour.TermRenderer {
	return m.makeMdRenderer()
}

func (m *Model) makeMdRenderer() *glamour.TermRenderer {
	w := m.width - 4 - 16
	if w < 20 {
		w = 20
	}
	if m.mdRenderer != nil && m.cachedWidth == w {
		return m.mdRenderer
	}
	style := styles.DarkStyleConfig
	style.Document.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Chroma = &ansi.Chroma{
		Text:                ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		Error:               ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		Comment:             ansi.StylePrimitive{Color: strPtr("#8b949e")},
		CommentPreproc:      ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		Keyword:             ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordReserved:     ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordNamespace:    ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordType:         ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		Operator:            ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		Punctuation:         ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		Name:                ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		NameBuiltin:         ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameTag:             ansi.StylePrimitive{Color: strPtr("#7ee787")},
		NameAttribute:       ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameClass:           ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameConstant:        ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameDecorator:       ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
		NameFunction:        ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
		LiteralNumber:       ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		LiteralString:       ansi.StylePrimitive{Color: strPtr("#a5d6ff")},
		LiteralStringEscape: ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		GenericDeleted:      ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		GenericInserted:     ansi.StylePrimitive{Color: strPtr("#7ee787")},
		GenericStrong:       ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		GenericSubheading:   ansi.StylePrimitive{Color: strPtr("#e6edf3")},
	}
	style.CodeBlock.StylePrimitive.Color = strPtr("#e6edf3")
	style.CodeBlock.StylePrimitive.BackgroundColor = strPtr("#161b22")
	style.CodeBlock.BlockPrefix = ""
	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.Table.Margin = uintPtr(0)

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(w),
	)
	if err == nil {
		m.mdRenderer = r
		m.cachedWidth = w
		return r
	}
	return nil
}

func uintPtr(u uint) *uint { return &u }
func strPtr(s string) *string { return &s }
