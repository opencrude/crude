package workspace

import (
	"strings"
	"time"

	"crude/provider"
	"crude/tools"
	ttypes "crude/types"
	"crude/types/reasoning"
	"crude/types/recovery"
)

const (
	MessageRoleUser       = "user"
	MessageRoleAssistant  = "assistant"
	MessageRoleThinking   = "thinking"
	MessageRoleTool       = "tool"
	MessageRoleError      = "error"
	MessageRoleStatus     = "status"
	MessageRoleAssignment = "assignment"
)

// Message is the shared canonical conversation state. Workspace owns live
// mutation; sessiondb owns JSON persistence; the TUI only projects it.
type Message = ttypes.ConversationMessage

type TurnStatus struct {
	Phase         string
	Progress      int
	Provider      string
	UploadedBytes int64
	TotalBytes    int64
	BeforeTokens  int
	AfterTokens   int
	OperationID   string
	Trigger       string
	StageName     string
	StageIndex    int
	Completed     int
	StageCount    int
	RetryAttempt  int
	RetryAt       time.Time
	RetryLimit    int
	ContextWindow int
	Detail        string
	ProviderID    string
	AccountID     string
	ModelID       string
}

// Conversation is the canonical live message state for one agent or thinking
// pane. It deliberately contains no rendering or interaction state.
type Conversation struct {
	brief            string
	messages         []*Message
	byID             map[string]*Message
	toolTimelines    map[*Message]*toolTimeline
	nextID           uint64
	version          uint64
	sourceGeneration uint64
	turnIdentity     ttypes.TurnIdentity
	lastRecovery     recovery.Status
	changeJournal    []conversationChange
	pendingChange    conversationChange
	// Only the currently growing row owns spare capacity, never every archived
	// message. Builder.String publishes immutable prefixes without copying them.
	textMessage *Message
	textBuilder strings.Builder
	// The current provider response's accepted assistant row survives lifecycle
	// transitions (including recovery settlement) until a new request begins.
	responseID      string
	responseMessage *Message
}

const toolPhaseMinDwell = 120 * time.Millisecond

type toolTransition struct {
	preview string
	status  string
}

type toolTimeline struct {
	since time.Time
	queue []toolTransition
}

func NewConversation() *Conversation {
	return &Conversation{
		byID:          make(map[string]*Message),
		toolTimelines: make(map[*Message]*toolTimeline),
	}
}

func (c *Conversation) Version() uint64 {
	if c == nil {
		return 0
	}
	return c.version
}

// SourceGeneration distinguishes accepted replacement snapshots from ordinary
// deltas. Protocol revisions may repeat when a new core replaces this mirror;
// presentation caches must compare both without changing the wire cursor.
func (c *Conversation) SourceGeneration() uint64 {
	if c == nil {
		return 0
	}
	return c.sourceGeneration
}

// BeginTurn installs the immutable identity for messages created by one
// admitted backend turn. Older events cannot move attribution backward.
func (c *Conversation) BeginTurn(identity ttypes.TurnIdentity) bool {
	if c == nil || identity.Generation == 0 || identity.ModelID == "" {
		return false
	}
	if identity.Generation < c.turnIdentity.Generation {
		return false
	}
	if identity.Generation == c.turnIdentity.Generation && c.turnIdentity.ModelID != "" {
		return c.turnIdentity == identity
	}
	c.turnIdentity = identity
	c.responseID, c.responseMessage = "", nil
	return true
}

// SwitchConnection replaces the active turn's connection (provider, account,
// model) within the same generation, so messages after a parent's
// request-boundary model change are attributed to the model that produced them.
func (c *Conversation) SwitchConnection(identity ttypes.TurnIdentity) bool {
	if c == nil || identity.ModelID == "" || identity.Generation == 0 || identity.Generation != c.turnIdentity.Generation {
		return false
	}
	c.turnIdentity = identity
	return true
}

func (c *Conversation) attributeTurn(message *Message) {
	if c == nil || message == nil || message.ModelID != "" {
		return
	}
	message.TurnGeneration = c.turnIdentity.Generation
	message.ProviderID = c.turnIdentity.ProviderID
	message.AccountID = c.turnIdentity.AccountID
	message.ModelID = c.turnIdentity.ModelID
}

func (c *Conversation) Messages() []Message {
	if c == nil {
		return nil
	}
	out := make([]Message, len(c.messages))
	for i, message := range c.messages {
		out[i] = ttypes.CloneConversationMessage(*message)
	}
	return out
}

func (c *Conversation) AppendMessage(message Message) string {
	if c == nil {
		return ""
	}
	copy := ttypes.CloneConversationMessage(message)
	if message.Recovery != nil && (c.lastRecovery.ID == "" || !message.Recovery.UpdatedAt.Before(c.lastRecovery.UpdatedAt)) {
		c.lastRecovery = *message.Recovery
	}
	if c.brief == "" && message.Role == MessageRoleUser && !message.PendingSteering &&
		message.Collaboration == nil &&
		(message.Origin == "" || message.Origin == provider.OriginHuman || message.Origin == provider.OriginHumanSteering) {
		for _, ch := range message.Content {
			if ch == '\n' || len(c.brief) >= 240 {
				break
			}
			c.brief += string(ch)
		}
	}
	if copy.InternalID == "" || c.byID[copy.InternalID] != nil {
		copy.InternalID = c.nextMessageID()
	}
	c.insertMessage(&copy)
	c.index(&copy)
	c.commitChange(&copy)
	return copy.InternalID
}

func (c *Conversation) AppendMessageIfAbsent(message Message) bool {
	if c == nil || message.InternalID == "" || c.byID[message.InternalID] != nil {
		return false
	}
	c.AppendMessage(message)
	return true
}

// Brief retains the first accepted human intent without per-frame transcript scans.
func (c *Conversation) Brief() string {
	if c == nil {
		return ""
	}
	return c.brief
}

// insertMessage keeps unresolved human steering notes at the visual tail. Provider output
// and lifecycle rows arriving after admission are inserted before those notes;
// once a note is acknowledged, normal chronological appends resume after it.
func (c *Conversation) insertMessage(message *Message) {
	c.pendingChange.structural = true
	if message.PendingSteering {
		c.messages = append(c.messages, message)
		return
	}
	insertAt := len(c.messages)
	for insertAt > 0 && c.messages[insertAt-1].PendingSteering {
		insertAt--
	}
	if message.Role != MessageRoleStatus && insertAt > 0 &&
		c.messages[insertAt-1].Role == MessageRoleStatus && c.messages[insertAt-1].LifecycleOperationID == "" {
		insertAt--
	}
	c.messages = append(c.messages, nil)
	copy(c.messages[insertAt+1:], c.messages[insertAt:])
	c.messages[insertAt] = message
}

func (c *Conversation) lastNonPending() *Message {
	for i := len(c.messages) - 1; i >= 0; i-- {
		if !c.messages[i].PendingSteering {
			return c.messages[i]
		}
	}
	return nil
}

// ActiveThinking returns the current live thought without exporting the dense
// transcript. Promoted thoughts live in their owning pane's Conversation.
func (c *Conversation) ActiveThinking() (Message, bool) {
	if c != nil {
		if last := c.lastNonPending(); last != nil && last.Role == MessageRoleThinking && last.ThinkingActive {
			return ttypes.CloneConversationMessage(*last), true
		}
	}
	return Message{}, false
}

// SetLatestThinkingKind settles presentation metadata without moving the
// accepted trace. A provisional thought keeps its original transcript slot,
// stable ID, and full body when post-response accounting becomes available.
func (c *Conversation) SetLatestThinkingKind(kind reasoning.Kind) bool {
	if c == nil || kind == reasoning.Unknown || kind == reasoning.Provisional {
		return false
	}
	for i := len(c.messages) - 1; i >= 0; i-- {
		message := c.messages[i]
		if message.TurnGeneration != c.turnIdentity.Generation {
			break
		}
		if message.Role != MessageRoleThinking {
			continue
		}
		if message.ThinkingDisplay.Kind != reasoning.Provisional {
			return false
		}
		message.ThinkingDisplay.Kind = kind
		c.commitChange(message)
		return true
	}
	return false
}

func (c *Conversation) AppendPendingSteering(message Message) string {
	message.PendingSteering = true
	return c.AppendMessage(message)
}

// ConsumePendingSteering resolves one backend-confirmed note while retaining its
// user message.
func (c *Conversation) ConsumePendingSteering(id string) bool {
	if c == nil || id == "" {
		return false
	}
	message := c.byID[id]
	if message == nil || !message.PendingSteering {
		return false
	}
	message.PendingSteering = false
	// The note is now part of the next request. Keep the live turn indicator
	// below it even if that indicator existed before the note was submitted.
	if status := c.activeTurnStatus(""); status != nil {
		c.moveBeforePending(status)
	}
	c.commitChange(message)
	return true
}

// SettlePendingSteeringMessages settles matching notes in place. A locally
// accepted send belongs to conversation context even when its execution ends.
func (c *Conversation) SettlePendingSteeringMessages(ids []string) []Message {
	if c == nil || len(ids) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	var settled []Message
	for _, message := range c.messages {
		_, selected := wanted[message.InternalID]
		if selected && message.Role == MessageRoleUser {
			message.PendingSteering = false
			c.changed(message)
			settled = append(settled, ttypes.CloneConversationMessage(*message))
		}
	}
	if len(settled) > 0 {
		c.commitChange(nil)
	}
	return settled
}

func (c *Conversation) SettlePendingSteeringIDs(ids []string) int {
	return len(c.SettlePendingSteeringMessages(ids))
}

func (c *Conversation) PendingSteeringCount() int {
	if c == nil {
		return 0
	}
	count := 0
	for _, message := range c.messages {
		if message.PendingSteering {
			count++
		}
	}
	return count
}

func (c *Conversation) AppendToken(token string) {
	if c == nil || token == "" || len(c.messages) == 0 {
		return
	}
	last := c.lastNonPending()
	if last == nil {
		return
	}
	if last.Role != MessageRoleAssistant {
		return
	}
	offset := len(last.Content)
	c.appendText(last, token)
	c.responseMessage = last
	c.commitText(last, offset)
}

func (c *Conversation) beginResponse(id string) {
	if c != nil && (id == "" || id != c.responseID) {
		c.responseID, c.responseMessage = id, nil
	}
}

func (c *Conversation) appendText(message *Message, text string) {
	if c.textMessage != message {
		c.textBuilder = strings.Builder{}
		c.textBuilder.WriteString(message.Content)
		c.textMessage = message
	}
	c.textBuilder.WriteString(text)
	message.Content = c.textBuilder.String()
}

func (c *Conversation) AppendThinking(content string) {
	c.AppendReasoning(reasoning.Delta{Text: content})
}

func (c *Conversation) AppendReasoning(delta reasoning.Delta) {
	if c == nil || delta.Text == "" || delta.Kind == reasoning.Encrypted {
		return
	}
	c.ClearTurnStatus()
	if len(c.messages) > 0 {
		last := c.lastNonPending()
		if last == nil {
			return
		}
		if last.Role == MessageRoleThinking && last.ThinkingActive {
			offset := len(last.Content)
			display := last.ThinkingDisplay
			if offset == 0 || display.Kind == reasoning.Provisional && delta.Kind != reasoning.Provisional && delta.Kind != reasoning.Unknown {
				display.Kind = delta.Kind
			} else if display.Kind != delta.Kind {
				display.Kind = reasoning.Unknown
			}
			if delta.BlockID != "" && display.BlockID != delta.BlockID && offset != 0 {
				c.appendText(last, "\n\n")
			}
			if delta.BlockID != "" && last.ThinkingDisplay.BlockID != delta.BlockID {
				display.Start = len(last.Content)
				display.BlockID = delta.BlockID
			}
			last.ThinkingDisplay = display
			c.appendText(last, delta.Text)
			c.commitText(last, offset)
			return
		}
	}
	for _, message := range c.messages {
		if message.ThinkingActive {
			c.changed(message)
			message.ThinkingActive = false
		}
	}
	display := reasoning.Display{}
	content := display.Append("", delta)
	c.AppendMessage(Message{
		Role:            MessageRoleThinking,
		Content:         content,
		ThinkingDisplay: display,
		Time:            time.Now(),
		ThinkingActive:  true,
		TurnGeneration:  c.turnIdentity.Generation,
		AccountID:       c.turnIdentity.AccountID,
		ProviderID:      c.turnIdentity.ProviderID,
		ModelID:         c.turnIdentity.ModelID,
	})
}

func (c *Conversation) EnsureAssistant() {
	if c == nil {
		return
	}
	c.ClearTurnStatus()
	if last := c.lastNonPending(); last != nil && last.Role == MessageRoleAssistant {
		return
	}
	c.AppendMessage(Message{
		Role: MessageRoleAssistant, Time: time.Now(),
		TurnGeneration: c.turnIdentity.Generation,
		AccountID:      c.turnIdentity.AccountID,
		ProviderID:     c.turnIdentity.ProviderID, ModelID: c.turnIdentity.ModelID,
	})
}

// UpdateTurnStatus maintains transient turn status or one ID-keyed durable
// operation row. Compaction rows remain after terminal completion and are
// restored from the backend operation ledger on resume.
func (c *Conversation) UpdateTurnStatus(status TurnStatus) {
	if c == nil || status.Phase == "" {
		return
	}
	status.Progress = max(0, min(100, status.Progress))
	status.TotalBytes = max(int64(0), status.TotalBytes)
	status.UploadedBytes = max(int64(0), min(status.TotalBytes, status.UploadedBytes))
	message := c.activeTurnStatus(status.OperationID)
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
			LifecycleBeforeTokens:  status.BeforeTokens,
			LifecycleAfterTokens:   status.AfterTokens,
			LifecycleOperationID:   status.OperationID,
			LifecycleTrigger:       status.Trigger,
			LifecycleStageName:     status.StageName,
			LifecycleStageIndex:    status.StageIndex,
			LifecycleCompleted:     status.Completed,
			LifecycleStageCount:    status.StageCount,
			LifecycleRetryAttempt:  status.RetryAttempt,
			LifecycleRetryAt:       status.RetryAt,
			LifecycleRetryLimit:    status.RetryLimit,
			LifecycleContextWindow: status.ContextWindow,
			Content:                status.Detail,
			AccountID:              status.AccountID,
			ProviderID:             status.ProviderID,
			ModelID:                status.ModelID,
		}
		c.insertMessage(message)
		c.index(message)
		c.commitChange(message)
		return
	}
	if message.LifecyclePhase != status.Phase && status.OperationID == "" {
		message.Time = time.Now()
	}
	if message.LifecyclePhase == status.Phase &&
		message.LifecycleProgress == status.Progress &&
		message.LifecycleProvider == status.Provider &&
		message.LifecycleUploadedBytes == status.UploadedBytes &&
		message.LifecycleTotalBytes == status.TotalBytes &&
		message.LifecycleBeforeTokens == status.BeforeTokens &&
		message.LifecycleAfterTokens == status.AfterTokens &&
		message.LifecycleOperationID == status.OperationID &&
		message.LifecycleTrigger == status.Trigger &&
		message.LifecycleStageName == status.StageName &&
		message.LifecycleStageIndex == status.StageIndex &&
		message.LifecycleCompleted == status.Completed &&
		message.LifecycleStageCount == status.StageCount &&
		message.LifecycleRetryAttempt == status.RetryAttempt &&
		message.LifecycleRetryAt.Equal(status.RetryAt) &&
		message.LifecycleRetryLimit == status.RetryLimit &&
		message.LifecycleContextWindow == status.ContextWindow &&
		message.Content == status.Detail &&
		message.AccountID == status.AccountID &&
		message.ProviderID == status.ProviderID &&
		message.ModelID == status.ModelID {
		return
	}
	message.LifecyclePhase = status.Phase
	message.LifecycleProgress = status.Progress
	message.LifecycleProvider = status.Provider
	message.LifecycleUploadedBytes = status.UploadedBytes
	message.LifecycleTotalBytes = status.TotalBytes
	message.LifecycleBeforeTokens = status.BeforeTokens
	message.LifecycleAfterTokens = status.AfterTokens
	message.LifecycleOperationID = status.OperationID
	message.LifecycleTrigger = status.Trigger
	message.LifecycleStageName = status.StageName
	message.LifecycleStageIndex = status.StageIndex
	message.LifecycleCompleted = status.Completed
	message.LifecycleStageCount = status.StageCount
	message.LifecycleRetryAttempt = status.RetryAttempt
	message.LifecycleRetryAt = status.RetryAt
	message.LifecycleRetryLimit = status.RetryLimit
	message.LifecycleContextWindow = status.ContextWindow
	message.Content = status.Detail
	if status.AccountID != "" {
		message.AccountID = status.AccountID
	}
	if status.ProviderID != "" {
		message.ProviderID = status.ProviderID
	}
	if status.ModelID != "" {
		message.ModelID = status.ModelID
	}
	c.moveBeforePending(message)
	c.commitChange(message)
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
		if message.Role == MessageRoleStatus && message.LifecycleOperationID == "" {
			delete(c.byID, message.InternalID)
			c.pendingChange.removed = append(c.pendingChange.removed, message.InternalID)
			c.pendingChange.structural = true
			changed = true
			continue
		}
		kept = append(kept, message)
	}
	if changed {
		c.messages = kept
		c.commitChange(nil)
	}
}

func (c *Conversation) activeTurnStatus(operationID string) *Message {
	for index := len(c.messages) - 1; index >= 0; index-- {
		if c.messages[index].Role == MessageRoleStatus && c.messages[index].LifecycleOperationID == operationID {
			return c.messages[index]
		}
	}
	return nil
}

func (c *Conversation) moveBeforePending(message *Message) {
	if len(c.messages) == 0 {
		return
	}
	current := -1
	for index, candidate := range c.messages {
		if candidate != message {
			continue
		}
		current = index
		break
	}
	if current < 0 {
		return
	}
	copy(c.messages[current:], c.messages[current+1:])
	c.messages = c.messages[:len(c.messages)-1]
	insertAt := len(c.messages)
	for insertAt > 0 && c.messages[insertAt-1].PendingSteering {
		insertAt--
	}
	c.messages = append(c.messages, nil)
	copy(c.messages[insertAt+1:], c.messages[insertAt:])
	c.messages[insertAt] = message
	if current != insertAt {
		c.pendingChange.structural = true
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
			c.changed(message)
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
			c.changed(message)
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
		// Dwell is presentation-only: a result already received before stop is
		// authoritative even if its terminal phase is still behind queued frames.
		// Do not let an old queued streaming frame reactivate a stopped turn.
		timeline := c.toolTimelines[message]
		terminal := toolTransition{}
		if timeline != nil {
			for _, transition := range timeline.queue {
				if transition.preview == "done" || transition.preview == "error" {
					terminal = transition
				}
			}
			delete(c.toolTimelines, message)
		}
		if terminal.preview != "" {
			message.ToolStatus, message.ToolPreviewPhase = terminal.status, terminal.preview
		} else if message.ToolStatus == "streaming" || message.ToolStatus == "calling" {
			message.ToolStatus, message.ToolPreviewPhase = "cancelled", "done"
		} else if timeline == nil {
			continue
		}
		message.ToolProgressPhase = ""
		message.ToolProgressMessage = ""
		c.changed(message)
		changed = true
	}
	c.bumpIf(changed)
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
	hasStreamingDetail := target != "" || contentLines > 0 || oldLines > 0 || newLines > 0 || estTokens > 0

	message := c.findTool(internalID, toolID)
	if message != nil && (message.ToolStatus == "success" || message.ToolStatus == "error" || message.ToolStatus == "cancelled") {
		return // Late evidence for this exact call cannot reopen or clone it.
	}
	if message == nil {
		message = c.promoteTurnStatusToTool(toolID, internalID, name)
	}
	if message != nil && message.ToolStatus != "success" && message.ToolStatus != "error" && message.ToolStatus != "cancelled" {
		c.attributeTurn(message)
		c.startToolTimeline(message)
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
		if hasStreamingDetail {
			c.scheduleToolTransition(message, "streaming", "streaming")
		}
		message.ToolProgressPhase = "streaming"
		message.ToolProgressMessage = ""
		c.bindToolID(message, toolID)
		c.bindInternalID(message, internalID)
		c.commitChange(message)
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
		ToolPreviewPhase:  "preparing",
		ToolProgressLabel: target,
		ToolEstTokens:     estTokens,
		ToolLinesAdded:    contentLines,
		ToolLinesRemoved:  oldLines,
		ToolStartTime:     time.Now(),
	}
	c.attributeTurn(message)
	applyToolInput(message, name, data)
	if newLines > 0 {
		message.ToolLinesAdded = newLines
	}
	c.insertMessage(message)
	c.index(message)
	c.startToolTimeline(message)
	if hasStreamingDetail {
		c.scheduleToolTransition(message, "streaming", "streaming")
	}
	c.commitChange(message)
}

func (c *Conversation) UpdateToolCall(toolID, internalID, name, argsCompact string) {
	if c == nil {
		return
	}
	message := c.findTool(internalID, toolID)
	if message != nil && (message.ToolStatus == "success" || message.ToolStatus == "error" || message.ToolStatus == "cancelled") {
		return
	}
	if message == nil {
		message = c.promoteTurnStatusToTool(toolID, internalID, name)
	}
	if message != nil && message.ToolStatus != "success" && message.ToolStatus != "error" && message.ToolStatus != "cancelled" {
		c.attributeTurn(message)
		c.startToolTimeline(message)
		message.Role = MessageRoleTool
		message.ToolName = name
		message.ToolStatus = "calling"
		message.ToolArgs = argsCompact
		message.Content = name + " " + argsCompact
		c.scheduleToolTransition(message, "streaming", "calling")
		message.ToolProgressPhase = ""
		message.ToolProgressMessage = ""
		if message.ToolProgressLabel == "" {
			message.ToolProgressLabel = argsCompact
		}
		c.bindToolID(message, toolID)
		c.bindInternalID(message, internalID)
		c.commitChange(message)
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
		ToolPreviewPhase:  "preparing",
		ToolProgressLabel: argsCompact,
		ToolStartTime:     time.Now(),
	}
	c.attributeTurn(message)
	c.insertMessage(message)
	c.index(message)
	c.startToolTimeline(message)
	c.scheduleToolTransition(message, "streaming", "calling")
	c.commitChange(message)
}

// AttachSubPaneToTool links a child to the exact parent agent_start call.
// Simultaneous launches may finish or send start events in any order; neither
// the latest call nor a guessed description is a safe correlation key.
func (c *Conversation) AttachSubPaneToTool(toolID, paneID, label string) bool {
	if c == nil || toolID == "" || paneID == "" {
		return false
	}
	message := c.findTool("", toolID)
	if message == nil || message.ToolID != toolID || message.Role != MessageRoleTool || message.ToolName != "agent_start" || message.SubPaneID != "" ||
		message.ToolStatus == "error" || message.ToolStatus == "cancelled" {
		return false
	}
	message.SubPaneID = paneID
	message.ToolProgressLabel = label
	c.commitChange(message)
	return true
}

// SetToolCommand snapshots the exact command separately from the compact title
// summary so projection can wrap it without losing information.
func (c *Conversation) SetToolCommand(toolID, internalID, command string) {
	if c == nil || command == "" {
		return
	}
	if message := c.findTool(internalID, toolID); message != nil {
		message.ToolCommand = command
		c.commitChange(message)
	}
}

func (c *Conversation) SetToolRound(toolID, internalID, roundID string) {
	if message := c.findTool(internalID, toolID); message != nil && roundID != "" && message.ToolRoundID != roundID {
		message.ToolRoundID = roundID
		c.commitChange(message)
	}
}

func (c *Conversation) MarkToolNotExecuted(toolID, internalID string) {
	if msg := c.findTool(internalID, toolID); msg != nil {
		msg.ToolStatus, msg.ToolPreviewPhase = "interrupted", ttypes.ToolPreviewDone
		delete(c.toolTimelines, msg)
		c.commitChange(msg)
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
		c.commitChange(message)
	}
}

func applyToolInput(message *Message, name string, data map[string]any) {
	if message == nil {
		return
	}
	if tools.RetainsStructuredToolInput(name) {
		if input := conversationString(data["args"]); input != "" {
			message.ToolInput = input
		}
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
		c.scheduleToolTransition(message, "done", "success")
		message.ToolDuration = elapsed(message.ToolStartTime)
		if estTokens > 0 {
			message.ToolEstTokens = estTokens
		}
	case "error":
		c.scheduleToolTransition(message, "error", "error")
		if linesAdded > 0 {
			message.ToolLinesAdded = linesAdded
		}
		if linesRemoved > 0 {
			message.ToolLinesRemoved = linesRemoved
		}
		if estTokens > 0 {
			message.ToolEstTokens = estTokens
		}
	default:
		c.scheduleToolTransition(message, "streaming", message.ToolStatus)
	}
	c.commitChange(message)
}

func (c *Conversation) UpdateToolResult(toolID, internalID, name, result string, isError bool, estTokens, linesAdded, linesRemoved int) {
	message := c.findTool(internalID, toolID)
	if message == nil {
		return
	}
	// Stop settles unfinished calls as cancelled for immediate presentation, but
	// a real backend result that was already in flight remains authoritative
	// evidence. Admit it without recreating the dwell timeline or active state.
	if message.ToolStatus == "cancelled" {
		if isError {
			message.ToolStatus, message.ToolPreviewPhase = "error", "error"
		} else {
			message.ToolStatus, message.ToolPreviewPhase = "success", "done"
		}
		message.ToolProgressPhase, message.ToolProgressMessage = "", ""
		message.ToolDuration = elapsed(message.ToolStartTime)
		if result != "" {
			message.Content = result
		}
		delete(c.toolTimelines, message)
		c.commitChange(message)
		return
	}
	if isError {
		c.scheduleToolTransition(message, "error", "error")
	} else {
		c.scheduleToolTransition(message, "done", "success")
	}
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
	c.commitChange(message)
}

func (c *Conversation) startToolTimeline(message *Message) *toolTimeline {
	if c == nil || message == nil {
		return nil
	}
	if c.toolTimelines == nil {
		c.toolTimelines = make(map[*Message]*toolTimeline)
	}
	if timeline := c.toolTimelines[message]; timeline != nil {
		return timeline
	}
	if message.ToolStartTime.IsZero() {
		message.ToolStartTime = time.Now()
	}
	message.ToolPreviewPhase = "preparing"
	if message.ToolStatus == "" {
		message.ToolStatus = "streaming"
	}
	timeline := &toolTimeline{since: time.Now()}
	c.toolTimelines[message] = timeline
	return timeline
}

func (c *Conversation) scheduleToolTransition(message *Message, preview, status string) {
	if preview == "" || message == nil {
		return
	}
	timeline := c.startToolTimeline(message)
	if timeline == nil {
		return
	}
	transition := toolTransition{preview: preview, status: status}
	if len(timeline.queue) > 0 {
		last := timeline.queue[len(timeline.queue)-1]
		if last == transition {
			return
		}
		if last.preview == preview && preview != "done" && preview != "error" {
			timeline.queue[len(timeline.queue)-1] = transition
			return
		}
		if preview == "done" || preview == "error" {
			for len(timeline.queue) > 0 {
				last = timeline.queue[len(timeline.queue)-1]
				if last.preview != "done" && last.preview != "error" {
					break
				}
				timeline.queue = timeline.queue[:len(timeline.queue)-1]
			}
		}
	}
	if len(timeline.queue) == 0 && message.ToolPreviewPhase == preview && message.ToolStatus == status {
		return
	}
	timeline.queue = append(timeline.queue, transition)
}

// AdvanceToolLifecycles paints at most one queued stage per tick. Provider
// chunking and fast local file operations can no longer collapse preparing,
// doing, and terminal states into one unobservable update burst.
func (c *Conversation) AdvanceToolLifecycles(now time.Time) bool {
	if c == nil || len(c.toolTimelines) == 0 {
		return false
	}
	changed := false
	for message, timeline := range c.toolTimelines {
		if len(timeline.queue) == 0 {
			if message.ToolPreviewPhase == "done" || message.ToolPreviewPhase == "error" || message.ToolStatus == "cancelled" {
				delete(c.toolTimelines, message)
			}
			continue
		}
		if now.Sub(timeline.since) < toolPhaseMinDwell {
			continue
		}
		transition := timeline.queue[0]
		timeline.queue = timeline.queue[1:]
		timeline.since = now
		message.ToolPreviewPhase = transition.preview
		if transition.status != "" {
			message.ToolStatus = transition.status
		}
		if transition.preview == "done" || transition.preview == "error" {
			message.ToolProgressPhase = ""
			message.ToolProgressMessage = ""
			if len(timeline.queue) == 0 {
				delete(c.toolTimelines, message)
			}
		}
		c.changed(message)
		changed = true
	}
	if changed {
		c.commitChange(nil)
	}
	return changed
}

func (c *Conversation) HasPendingToolLifecycles() bool {
	if c == nil {
		return false
	}
	for _, timeline := range c.toolTimelines {
		if len(timeline.queue) > 0 {
			return true
		}
	}
	return false
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
	c.commitChange(message)
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
			c.commitChange(message)
		}
	}
	c.AppendMessage(Message{
		Role: MessageRoleError, Content: reason, Time: time.Now(),
		TurnGeneration: c.turnIdentity.Generation,
		AccountID:      c.turnIdentity.AccountID,
		ProviderID:     c.turnIdentity.ProviderID, ModelID: c.turnIdentity.ModelID,
	})
}

func (c *Conversation) Finish(content, reason, boundaryID string) {
	if c == nil {
		return
	}
	c.ClearTurnStatus()
	if reason == "cancelled" {
		c.AppendMessage(Message{InternalID: boundaryID, Role: MessageRoleStatus, LifecyclePhase: "stopped", LifecycleOperationID: boundaryID, Time: time.Now(), TurnGeneration: c.turnIdentity.Generation})
		return
	}
	incomplete := reason != "" && reason != "completed"
	if content != "" && !incomplete {
		// Completion acknowledges accepted text, not the presentation's current
		// streaming flag. Recovery settlement can clear that flag or add a status
		// row between the final token and done. Only this request's accepted
		// assistant row is acknowledged; never deduplicate historical prose or
		// append a continuation's suffix to a row which already contains it.
		if last := c.responseMessage; last != nil && c.byID[last.InternalID] == last &&
			last.Role == MessageRoleAssistant && last.TurnGeneration == c.turnIdentity.Generation {
			return
		}
		if last := c.lastNonPending(); last != nil && last.Role == MessageRoleAssistant &&
			last.TurnGeneration == c.turnIdentity.Generation && last.Content == "" {
			last.Content = content
			c.commitChange(last)
			return
		}
		c.AppendMessage(Message{
			Role: MessageRoleAssistant, Content: content, Time: time.Now(),
			TurnGeneration: c.turnIdentity.Generation,
			AccountID:      c.turnIdentity.AccountID,
			ProviderID:     c.turnIdentity.ProviderID, ModelID: c.turnIdentity.ModelID,
		})
		return
	}
	if !incomplete {
		return
	}
	label := reason
	for i := len(c.messages) - 1; i >= 0; i-- {
		message := c.messages[i]
		if message.Role == MessageRoleUser {
			break
		}
		if message.Role == MessageRoleAssistant || message.Role == MessageRoleThinking {
			message.Incomplete = true
			message.IncompleteReason = label
			c.commitChange(message)
			return
		}
	}
	c.AppendMessage(Message{
		Role: MessageRoleAssistant, Incomplete: true, IncompleteReason: label, Time: time.Now(),
		TurnGeneration: c.turnIdentity.Generation,
		AccountID:      c.turnIdentity.AccountID,
		ProviderID:     c.turnIdentity.ProviderID, ModelID: c.turnIdentity.ModelID,
	})
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
	if toolID != "" {
		if message := c.byID[toolID]; message != nil {
			return message
		}
	}
	if internalID != "" {
		if message := c.byID[internalID]; message != nil && (toolID == "" || message.ToolID == "" || message.ToolID == toolID) {
			return message
		}
	}
	return nil
}

func (c *Conversation) HasToolMessage(toolID, internalID string) bool {
	return c != nil && c.findTool(internalID, toolID) != nil
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
	message := c.activeTurnStatus("")
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
	message.LifecycleBeforeTokens = 0
	message.LifecycleAfterTokens = 0
	c.attributeTurn(message)
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
		c.commitChange(nil)
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
