package widgets

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	"crude/tui/components"
	"crude/tui/theme"
	"crude/tui/types"
)

type ChatModel struct {
	theme         *theme.Theme
	messages      []*types.ChatMessage
	offset        int
	scrolled      bool
	width         int
	height        int
	modelName     string
	selecting     bool
	selAnchorLine int
	selAnchorCol  int
	selEndLine    int
	selEndCol     int
	selAutoScroll bool
	cachedLines   []string
	cachedWidth   int
	mdRenderer    *glamour.TermRenderer
	spinner       components.Spinner
	streaming     bool
	scrollTimes   []time.Time
}

func NewChatModel(t *theme.Theme) ChatModel {
	return ChatModel{
		theme:   t,
		spinner: components.NewSpinner(t),
	}
}

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
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
		// velocity-based scrolling
		scrollCount := len(recent)
		if scrollCount > 5 {
			n = 9
		} else if scrollCount > 3 {
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

func (m ChatModel) View() string {
	return ""
}

func (m *ChatModel) AppendMessage(msg types.ChatMessage) {
	msgCopy := msg
	m.messages = append(m.messages, &msgCopy)
}

func (m *ChatModel) AppendToken(token string) {
	if len(m.messages) == 0 {
		return
	}
	last := m.messages[len(m.messages)-1]
	if last.Role != types.RoleAssistant {
		return
	}
	last.Content += token
	last.CachedLines = nil
	// auto-scroll if user hasn't manually scrolled
	if !m.scrolled {
		m.offset = 0
	}
}

func (m *ChatModel) AppendThinking(s string) {
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.Role == types.RoleThinking {
			last.Content += s
			return
		}
	}
	// mark all previous thinking as done
	for _, msg := range m.messages {
		if msg.ThinkingActive {
			msg.ThinkingActive = false
			msg.CachedLines = nil
		}
	}
	m.messages = append(m.messages, &types.ChatMessage{
		Role:           types.RoleThinking,
		Content:        s,
		ThinkingActive: true,
	})
}

func (m *ChatModel) UpdateToolStreaming(toolID, name string, data map[string]any) {
	target, _ := data["target"].(string)
	estTokens := 0
	if v, ok := data["tool_est_tokens"]; ok {
		if n, ok := v.(float64); ok {
			estTokens = int(n)
		}
	}
	contentLines := 0
	if v, ok := data["content_lines_int"]; ok {
		if n, ok := v.(float64); ok {
			contentLines = int(n)
		}
	}
	oldLines := 0
	if v, ok := data["old_lines_int"]; ok {
		if n, ok := v.(float64); ok {
			oldLines = int(n)
		}
	}
	newLines := 0
	if v, ok := data["new_lines_int"]; ok {
		if n, ok := v.(float64); ok {
			newLines = int(n)
		}
	}

	previewPhase := types.ToolPreviewPreparing
	if target != "" {
		previewPhase = types.ToolPreviewStreaming
	}

	for i := range m.messages {
		if m.messages[i].ToolID == toolID {
			msg := m.messages[i]
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
			if target != "" {
				msg.ToolProgressLabel = target
			}
			if contentLines > 0 {
				msg.ToolLivedAdded = contentLines
			}
			if oldLines > 0 {
				msg.ToolLivedRemoved = oldLines
			}
			if newLines > 0 {
				msg.ToolLivedAdded = newLines
			}
			msg.ToolPreviewPhase = previewPhase
			msg.ToolProgressPhase = "streaming"
			msg.ToolProgressMessage = ""
			msg.CachedLines = nil
			return
		}
	}

	msg := types.ChatMessage{
		Role:              types.RoleTool,
		Time:              time.Now(),
		ToolName:          name,
		ToolID:            toolID,
		ToolArgs:          "",
		ToolStatus:        "streaming",
		ToolIndex:         len(m.messages),
		ToolProgressPhase: "streaming",
		ToolPreviewPhase:  previewPhase,
		ToolProgressLabel: target,
		ToolEstTokens:     estTokens,
		ToolLivedAdded:    contentLines,
		ToolLivedRemoved:  oldLines,
		ToolStartTime:     time.Now(),
	}
	if newLines > 0 {
		msg.ToolLivedAdded = newLines
	}
	m.messages = append(m.messages, &msg)
}

func (m *ChatModel) UpdateToolCall(toolID, name, argsCompact string) {
	found := false
	for _, msg := range m.messages {
		if msg.ToolID == toolID && msg.ToolName == name &&
			(msg.ToolStatus == "calling" || msg.ToolStatus == "streaming") {
			msg.ToolStatus = "calling"
			msg.ToolArgs = argsCompact
			msg.Content = name + " " + argsCompact
			msg.ToolPreviewPhase = types.ToolPreviewStreaming
			msg.ToolProgressPhase = ""
			msg.ToolProgressMessage = ""
			if msg.ToolProgressLabel == "" {
				msg.ToolProgressLabel = argsCompact
			}
			msg.CachedLines = nil
			found = true
			break
		}
	}
	if !found {
		m.messages = append(m.messages, &types.ChatMessage{
			Role:              types.RoleTool,
			Content:           name + " " + argsCompact,
			Time:              time.Now(),
			ToolName:          name,
			ToolID:            toolID,
			ToolArgs:          argsCompact,
			ToolStatus:        "calling",
			ToolIndex:         len(m.messages),
			ToolPreviewPhase:  types.ToolPreviewStreaming,
			ToolProgressLabel: argsCompact,
			ToolStartTime:     time.Now(),
		})
	}
}

func (m *ChatModel) UpdateToolProgress(toolID, name, phase, message, path string, linesAdded, linesRemoved int) {
	for _, msg := range m.messages {
		if msg.ToolStatus != "calling" && msg.ToolStatus != "streaming" {
			continue
		}
		matched := false
		if toolID != "" && msg.ToolID == toolID && msg.ToolName == name {
			matched = true
		} else if toolID == "" && msg.ToolName == name {
			matched = true
		}
		if matched {
			msg.ToolProgressPhase = phase
			msg.ToolProgressMessage = message
			if path != "" {
				msg.ToolProgressLabel = path
			}
			if linesAdded > 0 {
				msg.ToolLivedAdded = linesAdded
			}
			if linesRemoved > 0 {
				msg.ToolLivedRemoved = linesRemoved
			}
			msg.CachedLines = nil
			return
		}
	}
}

func (m *ChatModel) UpdateToolResult(toolID, name, result string, isError bool) {
	for _, msg := range m.messages {
		if msg.ToolStatus != "calling" && msg.ToolStatus != "streaming" {
			continue
		}
		matched := false
		if toolID != "" && msg.ToolID == toolID && msg.ToolName == name {
			matched = true
		} else if toolID == "" && msg.ToolName == name {
			matched = true
		}
		if matched {
			if isError {
				msg.ToolStatus = "error"
			} else {
				msg.ToolStatus = "success"
			}
			msg.ToolPreviewPhase = types.ToolPreviewDone
			msg.ToolProgressPhase = ""
			msg.ToolProgressMessage = ""
			if !msg.ToolStartTime.IsZero() {
				msg.ToolDuration = time.Since(msg.ToolStartTime)
			}
			msg.CachedLines = nil
			if result != "" {
				msg.Content = result
			} else {
				msg.Content = msg.ToolName + " " + msg.ToolArgs
			}
			return
		}
	}
}

func (m *ChatModel) ClearStreamingTools() {
	var keep []*types.ChatMessage
	for _, msg := range m.messages {
		if msg.ToolStatus == "streaming" || msg.ToolStatus == "calling" {
			continue
		}
		keep = append(keep, msg)
	}
	m.messages = keep
}

func (m *ChatModel) SetModelName(name string) {
	m.modelName = name
}

func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *ChatModel) ScrollToBottom() {
	m.scrolled = false
	m.offset = 0
}

func (m *ChatModel) MarkAllThinkingDone() {
	for _, msg := range m.messages {
		if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
		}
	}
}

func (m *ChatModel) InvalidateCache() {
	m.cachedLines = nil
	m.cachedWidth = 0
}

func (m *ChatModel) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if m.selAnchorLine < m.selEndLine ||
		(m.selAnchorLine == m.selEndLine && m.selAnchorCol <= m.selEndCol) {
		return m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol
	}
	return m.selEndLine, m.selEndCol, m.selAnchorLine, m.selAnchorCol
}

func (m *ChatModel) MouseToAllLine(mouseY int) (allIdx int, ok bool) {
	return 0, false
}

func (m *ChatModel) ChatAreaContentBounds() (firstLine, lastLine int) {
	return 0, 0
}

func (m *ChatModel) Messages() []*types.ChatMessage {
	return m.messages
}

func (m *ChatModel) ScrollUp(n int) {
	m.offset += n
	m.scrolled = true
}

func (m *ChatModel) ScrollDown(n int) {
	m.offset -= n
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset == 0 {
		m.scrolled = false
	}
}

func (m *ChatModel) Scrolled() bool {
	return m.scrolled
}

func (m *ChatModel) ChatOffset() int {
	return m.offset
}

func (m *ChatModel) ClampOffset(maxOffset int) {
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *ChatModel) MdRenderer() *glamour.TermRenderer {
	return m.makeMdRenderer()
}

func (m *ChatModel) makeMdRenderer() *glamour.TermRenderer {
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
