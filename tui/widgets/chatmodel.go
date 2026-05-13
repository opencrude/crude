package widgets

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"crude/tui/components"
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
	allLinesDirty   bool
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

func (m ChatModel) View() string {
	return ""
}

func (m *ChatModel) AppendMessage(msg types.ChatMessage) {
	msgCopy := msg
	m.messages = append(m.messages, &msgCopy)
	m.allLinesDirty = true
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
	m.allLinesDirty = true
	if strings.Contains(token, "\n") {
		last.StreamCompletedLines = nil
	}
	if !m.scrolled {
		m.offset = 0
	}
}

func (m *ChatModel) AppendThinking(s string) {
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.Role == types.RoleThinking {
			last.Content += s
			last.CachedLines = nil
			m.allLinesDirty = true
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
	m.allLinesDirty = true
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
			m.allLinesDirty = true
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
	m.allLinesDirty = true
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
			m.allLinesDirty = true
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
		m.allLinesDirty = true
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
			m.allLinesDirty = true
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
			m.allLinesDirty = true
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
	m.allLinesDirty = true
}

func (m *ChatModel) SetModelName(name string) {
	m.modelName = name
}

func (m *ChatModel) SetSize(w, h int) {
	if m.width != w {
		for i := range m.messages {
			m.messages[i].CachedLines = nil
			m.messages[i].StreamCompletedLines = nil
			m.messages[i].StreamCompletedLen = 0
		}
		m.mdRenderer = nil
		m.allLinesDirty = true
	}
	m.width = w
	m.height = h
}

func (m *ChatModel) MarkAllThinkingDone() {
	for _, msg := range m.messages {
		if msg.Role == types.RoleThinking {
			msg.ThinkingActive = false
			msg.CachedLines = nil
		}
	}
	m.allLinesDirty = true
}

func (m *ChatModel) InvalidateCache() {
	invalidated := false
	for _, msg := range m.messages {
		if msg.Role == types.RoleTool && (msg.ToolStatus == "streaming" || msg.ToolStatus == "calling") {
			msg.CachedLines = nil
			invalidated = true
		}
		if msg.Role == types.RoleThinking && msg.ThinkingActive {
			msg.CachedLines = nil
			invalidated = true
		}
	}
	if invalidated {
		m.allLinesDirty = true
	}
}

func (m *ChatModel) Messages() []*types.ChatMessage {
	return m.messages
}

func (m *ChatModel) SetStreaming(s bool) { m.streaming = s }

func (m ChatModel) CachedAllLines() []string { return m.cachedAllLines }

func (m *ChatModel) SetAllLinesDirty(d bool) { m.allLinesDirty = d }

func (m ChatModel) AllLinesDirty() bool { return m.allLinesDirty }
