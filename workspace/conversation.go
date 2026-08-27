package workspace

import "time"

const (
	MessageRoleUser      = "user"
	MessageRoleAssistant = "assistant"
	MessageRoleThinking  = "thinking"
	MessageRoleTool      = "tool"
	MessageRoleError     = "error"
	MessageRoleStatus    = "status"
)

// Message is live conversation data owned by the workspace. Rendering caches,
// selection, scrolling, and terminal layout remain in the TUI projection.
type Message struct {
	InternalID      string
	Role            string
	Content         string
	ReferenceLabels []string
	Time            time.Time

	ToolName            string
	ToolArgs            string
	ToolCommand         string
	ToolInput           string
	ToolScope           string
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
	ThinkingDuration       time.Duration
	LifecyclePhase         string
	LifecycleProgress      int
	LifecycleProvider      string
	LifecycleUploadedBytes int64
	LifecycleTotalBytes    int64
	Incomplete             bool
	IncompleteReason       string
	SubPaneID              string
	PermTag                string
}

type TurnStatus struct {
	Phase         string
	Progress      int
	Provider      string
	UploadedBytes int64
	TotalBytes    int64
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
	// BTW/slash notices arriving during a provider wait belong before the live
	// lifecycle row. Keeping that row last lets it continue updating and then
	// transform into the provider's first tool call without reordering history.
	if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == MessageRoleStatus && copy.Role != MessageRoleStatus {
		status := c.messages[len(c.messages)-1]
		c.messages[len(c.messages)-1] = &copy
		c.messages = append(c.messages, status)
	} else {
		c.messages = append(c.messages, &copy)
	}
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
	c.ClearTurnStatus()
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
	c.ClearTurnStatus()
	if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == MessageRoleAssistant {
		return
	}
	c.AppendMessage(Message{Role: MessageRoleAssistant, Time: time.Now()})
}

// UpdateTurnStatus maintains one transient lifecycle row at the tail of the
// conversation. It is live-only workspace state: session persistence never
// sees these events, and the row is consumed or removed before turn finality.
func (c *Conversation) UpdateTurnStatus(status TurnStatus) {
	if c == nil || status.Phase == "" {
		return
	}
	status.Progress = max(0, min(100, status.Progress))
	status.TotalBytes = max(int64(0), status.TotalBytes)
	status.UploadedBytes = max(int64(0), min(status.TotalBytes, status.UploadedBytes))
	message := c.activeTurnStatus()
	if message == nil {
		message = &Message{
			InternalID:             c.nextMessageID(),
			Role:                   MessageRoleStatus,
			Time:                   time.Now(),
			LifecyclePhase:         status.Phase,
			LifecycleProgress:      status.Progress,
			LifecycleProvider:      status.Provider,
			LifecycleUploadedBytes: status.UploadedBytes,
			LifecycleTotalBytes:    status.TotalBytes,
		}
		c.messages = append(c.messages, message)
		c.index(message)
		c.version++
		return
	}
	if message.LifecyclePhase != status.Phase {
		message.Time = time.Now()
	}
	if message.LifecyclePhase == status.Phase &&
		message.LifecycleProgress == status.Progress &&
		message.LifecycleProvider == status.Provider &&
		message.LifecycleUploadedBytes == status.UploadedBytes &&
		message.LifecycleTotalBytes == status.TotalBytes {
		return
	}
	message.LifecyclePhase = status.Phase
	message.LifecycleProgress = status.Progress
	message.LifecycleProvider = status.Provider
	message.LifecycleUploadedBytes = status.UploadedBytes
	message.LifecycleTotalBytes = status.TotalBytes
	c.moveToEnd(message)
	c.version++
}

// ClearTurnStatus removes an unconsumed lifecycle row when normal provider
// output starts or the turn ends.
func (c *Conversation) ClearTurnStatus() {
	if c == nil {
		return
	}
	changed := false
	kept := c.messages[:0]
	for _, message := range c.messages {
		if message.Role == MessageRoleStatus {
			delete(c.byID, message.InternalID)
			changed = true
			continue
		}
		kept = append(kept, message)
	}
	if changed {
		c.messages = kept
		c.version++
	}
}

func (c *Conversation) activeTurnStatus() *Message {
	for index := len(c.messages) - 1; index >= 0; index-- {
		if c.messages[index].Role == MessageRoleStatus {
			return c.messages[index]
		}
	}
	return nil
}

func (c *Conversation) moveToEnd(message *Message) {
	if len(c.messages) == 0 || c.messages[len(c.messages)-1] == message {
		return
	}
	for index, candidate := range c.messages {
		if candidate != message {
			continue
		}
		copy(c.messages[index:], c.messages[index+1:])
		c.messages[len(c.messages)-1] = message
		return
	}
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
	if message == nil {
		message = c.promoteTurnStatusToTool(toolID, internalID, name)
	}
	if message != nil && message.ToolStatus != "success" && message.ToolStatus != "error" && message.ToolStatus != "cancelled" {
		message.Role = MessageRoleTool
		message.ToolName = name
		message.ToolStatus = "streaming"
		if content, ok := data["content"].(string); ok {
			message.Content = content
		}
		if command, ok := data["command"].(string); ok && command != "" {
			message.ToolCommand = command
		}
		applyToolInput(message, name, data)
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
		c.bindInternalID(message, internalID)
		c.version++
		return
	}

	message = &Message{
		InternalID:        c.availableID(internalID),
		Role:              MessageRoleTool,
		Time:              time.Now(),
		ToolName:          name,
		ToolCommand:       conversationString(data["command"]),
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
	applyToolInput(message, name, data)
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
	if message == nil {
		message = c.promoteTurnStatusToTool(toolID, internalID, name)
	}
	if message != nil && message.ToolStatus != "success" && message.ToolStatus != "error" && message.ToolStatus != "cancelled" {
		message.Role = MessageRoleTool
		message.ToolName = name
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
		c.bindInternalID(message, internalID)
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

// SetToolCommand snapshots the exact command separately from the compact title
// summary so projection can wrap it without losing information.
func (c *Conversation) SetToolCommand(toolID, internalID, command string) {
	if c == nil || command == "" {
		return
	}
	if message := c.findTool(internalID, toolID); message != nil {
		message.ToolCommand = command
		c.version++
	}
}

// SetToolInput snapshots the exact primary operand and optional scope for
// tools whose compact title may truncate what matters (grep/search/fetch).
func (c *Conversation) SetToolInput(toolID, internalID, input, scope string) {
	if c == nil || input == "" {
		return
	}
	if message := c.findTool(internalID, toolID); message != nil {
		message.ToolInput = input
		message.ToolScope = scope
		c.version++
	}
}

func applyToolInput(message *Message, name string, data map[string]any) {
	if message == nil {
		return
	}
	switch name {
	case "grep_files":
		message.ToolInput = conversationString(data["pattern"])
		message.ToolScope = conversationString(data["path"])
	case "web_search", "arxiv_search":
		message.ToolInput = conversationString(data["query"])
	case "web_fetch":
		message.ToolInput = conversationString(data["url"])
	}
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

func (c *Conversation) SetToolPermTag(toolID, internalID, tag string) bool {
	message := c.findTool(internalID, toolID)
	if message == nil {
		return false
	}
	if message.PermTag == tag {
		return true
	}
	message.PermTag = tag
	c.version++
	return true
}

func (c *Conversation) MarkError(reason string) {
	if c == nil {
		return
	}
	c.ClearTurnStatus()
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
	c.ClearTurnStatus()
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

func (c *Conversation) bindInternalID(message *Message, internalID string) {
	if message == nil || internalID == "" {
		return
	}
	if existing := c.byID[internalID]; existing == nil || existing == message {
		c.byID[internalID] = message
	}
}

func (c *Conversation) promoteTurnStatusToTool(toolID, internalID, name string) *Message {
	message := c.activeTurnStatus()
	if message == nil {
		return nil
	}
	message.Role = MessageRoleTool
	message.Content = ""
	message.ToolName = name
	message.ToolID = ""
	message.ToolStatus = "streaming"
	message.ToolIndex = c.messageIndex(message)
	message.ToolProgressPhase = "streaming"
	message.ToolPreviewPhase = "preparing"
	message.ToolStartTime = time.Now()
	message.LifecyclePhase = ""
	message.LifecycleProgress = 0
	message.LifecycleProvider = ""
	message.LifecycleUploadedBytes = 0
	message.LifecycleTotalBytes = 0
	c.bindInternalID(message, internalID)
	c.bindToolID(message, toolID)
	return message
}

func (c *Conversation) messageIndex(target *Message) int {
	for index, message := range c.messages {
		if message == target {
			return index
		}
	}
	return len(c.messages)
}

func (c *Conversation) bumpIf(changed bool) {
	if changed {
		c.version++
	}
}

func conversationString(value any) string {
	text, _ := value.(string)
	return text
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
