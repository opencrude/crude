package workspace

import "time"

const (
	MessageRoleUser      = "user"
	MessageRoleAssistant = "assistant"
	MessageRoleThinking  = "thinking"
	MessageRoleTool      = "tool"
	MessageRoleError     = "error"
)

// Message is live conversation data owned by the workspace. Rendering caches,
// selection, scrolling, and terminal layout remain in the TUI projection.
type Message struct {
	InternalID string
	Role       string
	Content    string
	Time       time.Time

	ToolName            string
	ToolArgs            string
	ToolID              string
	ToolStatus          string
	ToolIndex           int
	ToolProgressPhase   string
	ToolProgressMessage string
	ToolProgressLabel   string
	ToolPreviewPhase    string
	ToolEstTokens       int
	ToolLinesAdded      int
	ToolLinesRemoved    int
	ToolStartTime       time.Time
	ToolDuration        time.Duration

	ThinkingActive      bool
	ThinkingInterrupted bool
	// ThinkingDuration records how long this thinking block ran. It is set
	// when the block transitions out of the active state (done or
	// interrupted) so the TUI can render a "thought for Xs" label.
	ThinkingDuration time.Duration
	Incomplete       bool
	IncompleteReason string
	SubPaneID        string
	PermTag          string
}

// Conversation is the canonical live message state for one agent or thinking
// pane. It deliberately contains no rendering or interaction state.
type Conversation struct {
	messages []*Message
	byID     map[string]*Message
	nextID   uint64
	version  uint64
}

func NewConversation() *Conversation {
	return &Conversation{byID: make(map[string]*Message)}
}

func (c *Conversation) Version() uint64 {
	if c == nil {
		return 0
	}
	return c.version
}

func (c *Conversation) Messages() []Message {
	if c == nil {
		return nil
	}
	out := make([]Message, len(c.messages))
	for i, message := range c.messages {
		out[i] = *message
	}
	return out
}

func (c *Conversation) AppendMessage(message Message) {
	if c == nil {
		return
	}
	copy := message
	if copy.InternalID == "" || c.byID[copy.InternalID] != nil {
		copy.InternalID = c.nextMessageID()
	}
	c.messages = append(c.messages, &copy)
	c.index(&copy)
	c.version++
}

func (c *Conversation) AppendToken(token string) {
	if c == nil || token == "" || len(c.messages) == 0 {
		return
	}
	last := c.messages[len(c.messages)-1]
	if last.Role != MessageRoleAssistant {
		return
	}
	last.Content += token
	c.version++
}

func (c *Conversation) AppendThinking(content string) {
	if c == nil || content == "" {
		return
	}
	if len(c.messages) > 0 {
		last := c.messages[len(c.messages)-1]
		if last.Role == MessageRoleThinking {
			last.Content += content
			c.version++
			return
		}
	}
	for _, message := range c.messages {
		message.ThinkingActive = false
	}
	c.AppendMessage(Message{
		Role:           MessageRoleThinking,
		Content:        content,
		Time:           time.Now(),
		ThinkingActive: true,
	})
}

func (c *Conversation) EnsureAssistant() {
	if c == nil {
		return
	}
	if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == MessageRoleAssistant {
		return
	}
	c.AppendMessage(Message{Role: MessageRoleAssistant, Time: time.Now()})
}

func (c *Conversation) MarkAllThinkingDone() {
	if c == nil {
		return
	}
	changed := false
	for _, message := range c.messages {
		if message.Role == MessageRoleThinking && (message.ThinkingActive || message.ThinkingInterrupted) {
			if message.ThinkingActive {
				message.ThinkingDuration = time.Since(message.Time)
			}
			message.ThinkingActive = false
			message.ThinkingInterrupted = false
			changed = true
		}
	}
	c.bumpIf(changed)
}

func (c *Conversation) MarkAllThinkingInterrupted() {
	if c == nil {
		return
	}
	changed := false
	for _, message := range c.messages {
		if message.Role != MessageRoleThinking {
			continue
		}
		if message.ThinkingActive {
			message.ThinkingInterrupted = true
			message.ThinkingDuration = time.Since(message.Time)
		}
		if message.ThinkingActive || message.ThinkingInterrupted {
			message.ThinkingActive = false
			changed = true
		}
	}
	c.bumpIf(changed)
}

func (c *Conversation) MarkStreamingToolsCancelled() {
	if c == nil {
		return
	}
	changed := false
	for _, message := range c.messages {
		if message.ToolStatus == "streaming" || message.ToolStatus == "calling" {
			message.ToolStatus = "cancelled"
			message.ToolPreviewPhase = "done"
			message.ToolProgressPhase = ""
			message.ToolProgressMessage = ""
			changed = true
		}
	}
	c.bumpIf(changed)
}

func (c *Conversation) RemoveActiveThinking() string {
	if c == nil {
		return ""
	}
	var content string
	kept := c.messages[:0]
	for _, message := range c.messages {
		if message.Role == MessageRoleThinking && message.ThinkingActive {
			content = message.Content
			delete(c.byID, message.InternalID)
			continue
		}
		kept = append(kept, message)
	}
	if len(kept) != len(c.messages) {
		c.messages = kept
		c.version++
	}
	return content
}

func (c *Conversation) UpdateToolStreaming(toolID, internalID, name string, data map[string]any) {
	if c == nil {
		return
	}
	target, _ := data["target"].(string)
	estTokens := conversationInt(data["tool_est_tokens"])
	contentLines := conversationInt(data["content_lines_int"])
	oldLines := conversationInt(data["old_lines_int"])
	newLines := conversationInt(data["new_lines_int"])
	previewPhase := "preparing"
	if target != "" || contentLines > 0 || oldLines > 0 || newLines > 0 || estTokens > 0 {
		previewPhase = "streaming"
	}

	message := c.findTool(internalID, toolID)
	if message != nil && message.ToolStatus != "success" && message.ToolStatus != "error" && message.ToolStatus != "cancelled" {
		if content, ok := data["content"].(string); ok {
			message.Content = content
		}
		if value, ok := data["tool_est_tokens"]; ok {
			message.ToolEstTokens = conversationInt(value)
		}
		if value, ok := data["lived_added"]; ok {
			message.ToolLinesAdded = conversationInt(value)
		}
		if value, ok := data["lived_removed"]; ok {
			message.ToolLinesRemoved = conversationInt(value)
		}
		if target != "" {
			message.ToolProgressLabel = target
		}
		if contentLines > 0 {
			message.ToolLinesAdded = contentLines
		}
		if oldLines > 0 {
			message.ToolLinesRemoved = oldLines
		}
		if newLines > 0 {
			message.ToolLinesAdded = newLines
		}
		message.ToolPreviewPhase = previewPhase
		message.ToolProgressPhase = "streaming"
		message.ToolProgressMessage = ""
		c.bindToolID(message, toolID)
		c.version++
		return
	}

	message = &Message{
		InternalID:        c.availableID(internalID),
		Role:              MessageRoleTool,
		Time:              time.Now(),
		ToolName:          name,
		ToolID:            toolID,
		ToolStatus:        "streaming",
		ToolIndex:         len(c.messages),
		ToolProgressPhase: "streaming",
		ToolPreviewPhase:  previewPhase,
		ToolProgressLabel: target,
		ToolEstTokens:     estTokens,
		ToolLinesAdded:    contentLines,
		ToolLinesRemoved:  oldLines,
		ToolStartTime:     time.Now(),
	}
	if newLines > 0 {
		message.ToolLinesAdded = newLines
	}
	c.messages = append(c.messages, message)
	c.index(message)
	c.version++
}

func (c *Conversation) UpdateToolCall(toolID, internalID, name, argsCompact string) {
	if c == nil {
		return
	}
	message := c.findTool(internalID, toolID)
	if message != nil && message.ToolStatus != "success" && message.ToolStatus != "error" && message.ToolStatus != "cancelled" {
		message.ToolStatus = "calling"
		message.ToolArgs = argsCompact
		message.Content = name + " " + argsCompact
		message.ToolPreviewPhase = "streaming"
		message.ToolProgressPhase = ""
		message.ToolProgressMessage = ""
		if message.ToolProgressLabel == "" {
			message.ToolProgressLabel = argsCompact
		}
		c.bindToolID(message, toolID)
		c.version++
		return
	}

	message = &Message{
		InternalID:        c.availableID(internalID),
		Role:              MessageRoleTool,
		Content:           name + " " + argsCompact,
		Time:              time.Now(),
		ToolName:          name,
		ToolID:            toolID,
		ToolArgs:          argsCompact,
		ToolStatus:        "calling",
		ToolIndex:         len(c.messages),
		ToolPreviewPhase:  "streaming",
		ToolProgressLabel: argsCompact,
		ToolStartTime:     time.Now(),
	}
	c.messages = append(c.messages, message)
	c.index(message)
	c.version++
}

func (c *Conversation) UpdateToolProgress(toolID, internalID, name, phase, detail, path string, linesAdded, linesRemoved, estTokens int) {
	message := c.findTool(internalID, toolID)
	if message == nil || (message.ToolStatus != "calling" && message.ToolStatus != "streaming" && message.ToolStatus != "success") {
		return
	}
	message.ToolProgressPhase = phase
	message.ToolProgressMessage = detail
	if path != "" {
		message.ToolProgressLabel = path
	}
	c.bindToolID(message, toolID)
	switch phase {
	case "done":
		message.ToolLinesAdded = linesAdded
		message.ToolLinesRemoved = linesRemoved
		message.ToolPreviewPhase = "done"
		message.ToolDuration = elapsed(message.ToolStartTime)
		if estTokens > 0 {
			message.ToolEstTokens = estTokens
		}
	case "error":
		message.ToolPreviewPhase = "error"
		if linesAdded > 0 {
			message.ToolLinesAdded = linesAdded
		}
		if linesRemoved > 0 {
			message.ToolLinesRemoved = linesRemoved
		}
		if estTokens > 0 {
			message.ToolEstTokens = estTokens
		}
	}
	c.version++
}

func (c *Conversation) UpdateToolResult(toolID, internalID, name, result string, isError bool, estTokens, linesAdded, linesRemoved int) {
	message := c.findTool(internalID, toolID)
	if message == nil || (message.ToolStatus != "calling" && message.ToolStatus != "streaming") {
		return
	}
	if isError {
		message.ToolStatus = "error"
	} else {
		message.ToolStatus = "success"
	}
	message.ToolPreviewPhase = "done"
	message.ToolProgressPhase = ""
	message.ToolProgressMessage = ""
	message.ToolDuration = elapsed(message.ToolStartTime)
	if estTokens > 0 {
		message.ToolEstTokens = estTokens
	}
	if linesAdded > 0 || linesRemoved > 0 {
		message.ToolLinesAdded = linesAdded
		message.ToolLinesRemoved = linesRemoved
	}
	if result != "" {
		message.Content = result
	} else {
		message.Content = message.ToolName + " " + message.ToolArgs
	}
	c.version++
}

func (c *Conversation) SetToolPermTag(toolID, internalID, tag string) {
	message := c.findTool(internalID, toolID)
	if message == nil || message.PermTag == tag {
		return
	}
	message.PermTag = tag
	c.version++
}

func (c *Conversation) MarkError(reason string) {
	if c == nil {
		return
	}
	if len(c.messages) > 0 {
		message := c.messages[len(c.messages)-1]
		if message.Role == MessageRoleAssistant && message.Content != "" {
			message.Incomplete = true
			message.IncompleteReason = reason
			c.version++
			return
		}
	}
	c.AppendMessage(Message{Role: MessageRoleError, Content: reason, Time: time.Now()})
}

func (c *Conversation) Finish(content, reason string, wasStreaming bool) {
	if c == nil {
		return
	}
	incomplete := reason != "" && reason != "completed"
	if content != "" && !incomplete {
		if wasStreaming && len(c.messages) > 0 {
			last := c.messages[len(c.messages)-1]
			if last.Role == MessageRoleAssistant {
				if last.Content == "" {
					last.Content = content
					c.version++
				}
				return
			}
		}
		c.AppendMessage(Message{Role: MessageRoleAssistant, Content: content, Time: time.Now()})
		return
	}
	if !incomplete {
		return
	}
	label := reason
	if reason == "cancelled" {
		label = "operation cancelled by user"
	}
	for i := len(c.messages) - 1; i >= 0; i-- {
		message := c.messages[i]
		if message.Role == MessageRoleAssistant || message.Role == MessageRoleThinking {
			message.Incomplete = true
			message.IncompleteReason = label
			c.version++
			return
		}
	}
	c.AppendMessage(Message{Role: MessageRoleAssistant, Incomplete: true, IncompleteReason: label, Time: time.Now()})
}

func (c *Conversation) nextMessageID() string {
	for {
		c.nextID++
		id := "msg-" + formatUint(c.nextID)
		if c.byID[id] == nil {
			return id
		}
	}
}

func (c *Conversation) availableID(candidate string) string {
	if candidate == "" || c.byID[candidate] != nil {
		return c.nextMessageID()
	}
	return candidate
}

func (c *Conversation) index(message *Message) {
	if c.byID == nil {
		c.byID = make(map[string]*Message)
	}
	c.byID[message.InternalID] = message
	if message.ToolID != "" {
		c.byID[message.ToolID] = message
	}
}

func (c *Conversation) findTool(internalID, toolID string) *Message {
	if c == nil {
		return nil
	}
	if internalID != "" && c.byID[internalID] != nil {
		return c.byID[internalID]
	}
	if toolID != "" {
		return c.byID[toolID]
	}
	return nil
}

func (c *Conversation) bindToolID(message *Message, toolID string) {
	if toolID == "" || message.ToolID != "" {
		return
	}
	if existing := c.byID[toolID]; existing == nil || existing == message {
		message.ToolID = toolID
		c.byID[toolID] = message
	}
}

func (c *Conversation) bumpIf(changed bool) {
	if changed {
		c.version++
	}
}

func conversationInt(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return 0
	}
}

func elapsed(start time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}

func formatUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
