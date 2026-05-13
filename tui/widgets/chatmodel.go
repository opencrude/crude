package widgets

import (
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

var selScrollInterval = 60 * time.Millisecond

type ChatModel struct {
	theme           *theme.Theme
	messages        []*types.ChatMessage
	offset          int
	scrolled        bool
	width           int
	height          int
	modelName       string
	selecting       bool
	selAnchorLine   int
	selAnchorCol    int
	selEndLine      int
	selEndCol       int
	selAutoScroll   bool
	cachedLines     []string
	cachedWidth     int
	cachedAllLines  []string
	chatAreaTop     int
	chatAreaHeight  int
	selMouseY       int
	scrollVelocity  float64
	lastScrollTime  time.Time
	mdRenderer      *glamour.TermRenderer
	spinner         components.Spinner
	streaming       bool
	scrollTimes     []time.Time
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
			m.ScrollUp(m.chatAreaHeight)
		case tea.KeyPgDown:
			m.ScrollDown(m.chatAreaHeight)
		case tea.KeyHome:
			m.offset = 1 << 30
			m.scrolled = true
		case tea.KeyEnd:
			m.ScrollToBottom()
		}
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case types.SelectionTickMsg:
		return m.handleSelectionTick()
	}
	return m, nil
}

func (m ChatModel) handleMouse(msg tea.MouseMsg) (ChatModel, tea.Cmd) {
	chatBottom := m.chatAreaTop + m.chatAreaHeight - 1

	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if msg.Y >= m.chatAreaTop && msg.Y <= chatBottom {
			lineIdx, ok := m.MouseToAllLine(msg.Y)
			if ok {
				m.selecting = true
				m.selAnchorLine = lineIdx
				m.selAnchorCol = rendering.Max(0, msg.X-m.theme.Spacing.PadLR)
				m.selEndLine = lineIdx
				m.selEndCol = m.selAnchorCol
				m.selMouseY = msg.Y
				m.selAutoScroll = false
			}
		} else {
			m.selecting = false
			m.selAutoScroll = false
		}

	case msg.Action == tea.MouseActionMotion && m.selecting:
		lineIdx, ok := m.MouseToAllLine(msg.Y)
		if ok {
			m.selEndLine = lineIdx
			m.selEndCol = rendering.Max(0, msg.X-m.theme.Spacing.PadLR)
		} else if msg.Y < m.chatAreaTop {
			firstLine, _ := m.ChatAreaContentBounds()
			if firstLine >= 0 {
				m.selEndLine = firstLine
				m.selEndCol = 0
			}
		} else if msg.Y > chatBottom {
			_, lastLine := m.ChatAreaContentBounds()
			if lastLine >= 0 {
				m.selEndLine = lastLine
				m.selEndCol = 1 << 30
			}
		}
		m.selMouseY = msg.Y

		if !m.selAutoScroll && (m.selMouseY <= m.chatAreaTop-1 || m.selMouseY >= chatBottom+1) {
			m.selAutoScroll = true
			return m, tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
				return types.SelectionTickMsg(t)
			})
		}

	case msg.Action == tea.MouseActionRelease && m.selecting:
		m.selecting = false
		m.selAutoScroll = false
		sLine, sCol, eLine, eCol := m.SelectionRange()
		text := rendering.ExtractSelectedText(m.cachedAllLines, sLine, sCol, eLine, eCol)
		text = strings.TrimRight(text, " \t\n\r")
		if text != "" {
			return m, rendering.CopyToClipboardCmd(text)
		}

	case msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown:
		now := time.Now()
		elapsed := now.Sub(m.lastScrollTime).Seconds()
		if elapsed < 0.1 {
			m.scrollVelocity = math.Min(m.scrollVelocity+0.5, 5)
		} else {
			m.scrollVelocity = 1
		}
		m.lastScrollTime = now
		n := int(m.scrollVelocity)
		if n < 1 {
			n = 1
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.ScrollUp(n)
		} else {
			m.ScrollDown(n)
		}
	}

	return m, nil
}

func (m ChatModel) handleSelectionTick() (ChatModel, tea.Cmd) {
	if !m.selecting || !m.selAutoScroll {
		return m, nil
	}

	chatBottom := m.chatAreaTop + m.chatAreaHeight - 1

	if m.selMouseY <= m.chatAreaTop-1 {
		m.ScrollUp(1)
		lineIdx, ok := m.MouseToAllLine(m.chatAreaTop)
		if ok {
			m.selEndLine = lineIdx
			m.selEndCol = 0
		}
	} else if m.selMouseY >= chatBottom+1 {
		m.ScrollDown(1)
		lineIdx, ok := m.MouseToAllLine(chatBottom)
		if ok {
			m.selEndLine = lineIdx
			m.selEndCol = 1 << 30
		}
	}

	if m.selMouseY <= m.chatAreaTop-1 || m.selMouseY >= chatBottom+1 {
		return m, tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
			return types.SelectionTickMsg(t)
		})
	}

	m.selAutoScroll = false
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
	if m.width != w {
		for i := range m.messages {
			m.messages[i].CachedLines = nil
		}
		m.mdRenderer = nil
	}
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
	for _, msg := range m.messages {
		if msg.Role == types.RoleTool && (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") {
			msg.CachedLines = nil
		}
	}
}

func (m *ChatModel) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if m.selAnchorLine < m.selEndLine ||
		(m.selAnchorLine == m.selEndLine && m.selAnchorCol <= m.selEndCol) {
		return m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol
	}
	return m.selEndLine, m.selEndCol, m.selAnchorLine, m.selAnchorCol
}

func (m *ChatModel) MouseToAllLine(mouseY int) (allIdx int, ok bool) {
	if len(m.cachedAllLines) == 0 {
		return -1, false
	}
	chatY := mouseY - m.chatAreaTop
	if chatY < 0 || chatY >= m.chatAreaHeight {
		return -1, false
	}
	total := len(m.cachedAllLines)
	maxOffset := total - m.chatAreaHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.offset
	if offset > maxOffset {
		offset = maxOffset
	}
	endIdx := total - offset
	startIdx := endIdx - m.chatAreaHeight
	if startIdx < 0 {
		startIdx = 0
	}
	visible := endIdx - startIdx
	emptyTop := m.chatAreaHeight - visible
	if emptyTop < 0 {
		emptyTop = 0
	}
	if chatY < emptyTop {
		return -1, false
	}
	allIdx = startIdx + (chatY - emptyTop)
	if allIdx < 0 || allIdx >= total {
		return -1, false
	}
	return allIdx, true
}

func (m *ChatModel) ChatAreaContentBounds() (firstLine, lastLine int) {
	if len(m.cachedAllLines) == 0 {
		return -1, -1
	}
	total := len(m.cachedAllLines)
	maxOffset := total - m.chatAreaHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.offset
	if offset > maxOffset {
		offset = maxOffset
	}
	endIdx := total - offset
	startIdx := endIdx - m.chatAreaHeight
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	return startIdx, endIdx - 1
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
		NameDecorator:        ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
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

func (m *ChatModel) SetCachedAllLines(lines []string) { m.cachedAllLines = lines }
func (m *ChatModel) SetChatAreaBounds(top, height int) {
	m.chatAreaTop = top
	m.chatAreaHeight = height
}

func (m *ChatModel) HasActiveSelection() bool {
	if !m.selecting && m.selAnchorLine == m.selEndLine && m.selAnchorCol == m.selEndCol {
		return false
	}
	return m.selAnchorLine != m.selEndLine || m.selAnchorCol != m.selEndCol
}

func (m *ChatModel) SetStreaming(s bool) { m.streaming = s }

func (m *ChatModel) ClearSelection() {
	m.selecting = false
	m.selAutoScroll = false
	m.selAnchorLine = 0
	m.selAnchorCol = 0
	m.selEndLine = 0
	m.selEndCol = 0
}

func uintPtr(u uint) *uint    { return &u }
func strPtr(s string) *string { return &s }
