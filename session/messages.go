package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

type taggedMessage struct {
	msg  provider.ChatMessage
	turn int
	meta sessiondb.DisplayMeta
}

func childAgentIDFromResult(text string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) == nil {
		if value, _ := payload["agent_id"].(string); value != "" {
			return value
		}
		if value, _ := payload["handle_id"].(string); value != "" {
			return value
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "**Agent:** ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "**Agent:** "))
		}
	}
	// A synchronous wait can outlive its parent turn; the returned handle
	// still identifies the child even when there is no terminal report yet.
	if index := strings.Index(text, `handle_id="`); index >= 0 {
		id := text[index+len(`handle_id="`):]
		if end := strings.IndexByte(id, '"'); end >= 0 {
			return id[:end]
		}
	}
	return ""
}

type displayMetaTracker struct {
	metas           map[string]*sessiondb.DisplayMeta
	pendingPermTag  string
	pendingPermTool string
}

func (t *displayMetaTracker) updateToolCall(toolID, name, args string) {
	m, ok := t.metas[toolID]
	if !ok {
		m = &sessiondb.DisplayMeta{}
		t.metas[toolID] = m
	}
	m.ToolName = name
	m.ToolArgs = args
	m.ToolStatus = "calling"
	snapshot := ensureToolSnapshot(m)
	snapshot.Role = "tool"
	snapshot.ToolID = toolID
	snapshot.ToolName = name
	snapshot.ToolArgs = args
	snapshot.ToolStatus = "calling"
	snapshot.ToolPreviewPhase = "preparing"
	if t.pendingPermTag != "" && (t.pendingPermTool == "" || t.pendingPermTool == name) {
		m.PermTag = t.pendingPermTag
		snapshot.PermTag = t.pendingPermTag
		t.pendingPermTag = ""
		t.pendingPermTool = ""
	}
}

func (t *displayMetaTracker) updateToolResult(toolID, name, result, status string, durMs int64, estTokens, linesAdded, linesRemoved int) {
	m, ok := t.metas[toolID]
	if !ok {
		m = &sessiondb.DisplayMeta{}
		t.metas[toolID] = m
	}
	m.ToolStatus = status
	if name != "" {
		m.ToolName = name
	}
	m.DurationMs = durMs
	m.EstTokens = estTokens
	m.LinesAdded = linesAdded
	m.LinesRemoved = linesRemoved
	snapshot := ensureToolSnapshot(m)
	snapshot.Role = "tool"
	snapshot.ToolID = toolID
	if name != "" {
		snapshot.ToolName = name
	}
	snapshot.Content = result
	snapshot.ToolStatus = status
	snapshot.ToolPreviewPhase = "done"
	if status == "error" {
		snapshot.ToolPreviewPhase = "error"
	}
	snapshot.ToolDuration = time.Duration(durMs) * time.Millisecond
	snapshot.ToolEstTokens = estTokens
	snapshot.ToolLinesAdded = linesAdded
	snapshot.ToolLinesRemoved = linesRemoved
	if snapshot.ToolName == "run_sub_agent" {
		snapshot.SubPaneID = childAgentIDFromResult(result)
	}
}

func (t *displayMetaTracker) updateToolProgress(toolID, name, phase, detail, label string, linesAdded, linesRemoved, estTokens int) {
	m, ok := t.metas[toolID]
	if !ok {
		m = &sessiondb.DisplayMeta{}
		t.metas[toolID] = m
	}
	snapshot := ensureToolSnapshot(m)
	snapshot.Role = "tool"
	snapshot.ToolID = toolID
	if name != "" {
		snapshot.ToolName = name
	}
	snapshot.ToolProgressPhase = phase
	snapshot.ToolProgressMessage = detail
	if label != "" {
		snapshot.ToolProgressLabel = label
	}
	snapshot.ToolLinesAdded = linesAdded
	snapshot.ToolLinesRemoved = linesRemoved
	snapshot.ToolEstTokens = estTokens
}

func (t *displayMetaTracker) setPendingPermTag(tool, tag string) {
	t.pendingPermTag = tag
	t.pendingPermTool = tool
}

func (t *displayMetaTracker) setPermTag(toolID, tool, tag string) {
	if toolID != "" {
		m, ok := t.metas[toolID]
		if !ok {
			m = &sessiondb.DisplayMeta{}
			t.metas[toolID] = m
		}
		m.PermTag = tag
		ensureToolSnapshot(m).PermTag = tag
		return
	}
	t.setPendingPermTag(tool, tag)
}

func ensureToolSnapshot(meta *sessiondb.DisplayMeta) *ttypes.ConversationMessage {
	meta.SnapshotVersion = sessiondb.ConversationSnapshotVersion
	if len(meta.Snapshots) == 0 {
		meta.Snapshots = []ttypes.ConversationMessage{{Role: "tool"}}
	}
	return &meta.Snapshots[0]
}

func canonicalizeDisplayMeta(message provider.ChatMessage, meta sessiondb.DisplayMeta, rawToolArgs string, index int, now time.Time) sessiondb.DisplayMeta {
	meta.SnapshotVersion = sessiondb.ConversationSnapshotVersion
	internalID := fmt.Sprintf("persisted-%d", index)
	if message.InternalID != "" {
		internalID = message.InternalID
	}
	if message.EffectiveOrigin() == provider.OriginTurnStopped {
		meta.Snapshots = []ttypes.ConversationMessage{{InternalID: internalID, Role: ttypes.RoleStatus, LifecyclePhase: "stopped", LifecycleOperationID: internalID, Time: now, TurnGeneration: meta.TurnGeneration}}
		return meta
	}
	switch message.Role {
	case "tool":
		// A reply is delivered as assistant progress when its tool result
		// arrives live. Its provider envelope projects to that same message on
		// recovery, never a second tool row.
		if meta.ToolName == "reply" {
			meta.Snapshots = nil
			if message.Content.String() != "" {
				role := "assistant"
				if message.ToolError {
					role = "error"
				}
				meta.Snapshots = []ttypes.ConversationMessage{{
					InternalID: "reply:" + message.ToolCallID, Role: role, Content: message.Content.String(), Time: now,
					TurnGeneration: meta.TurnGeneration, ProviderID: meta.ProviderID, AccountID: meta.AccountID, ModelID: meta.ModelID,
				}}
			}
			break
		}
		snapshot := ensureToolSnapshot(&meta)
		applyTurnIdentity(snapshot, meta)
		snapshot.InternalID = internalID
		snapshot.Role = "tool"
		snapshot.Content = message.Content.String()
		snapshot.Time = now
		snapshot.ToolID = message.ToolCallID
		snapshot.ToolStatus = persistedToolStatus(message, meta.ToolStatus)
		if snapshot.ToolStatus == "" {
			snapshot.ToolStatus = "success"
		}
		if snapshot.ToolStatus == "error" {
			snapshot.ToolPreviewPhase = "error"
		} else {
			snapshot.ToolPreviewPhase = "done"
		}
		if snapshot.ToolName == "" {
			snapshot.ToolName = meta.ToolName
		}
		if snapshot.ToolArgs == "" {
			snapshot.ToolArgs = meta.ToolArgs
		}
		applyCanonicalToolOperands(snapshot, rawToolArgs)
		if snapshot.ToolName == "run_sub_agent" {
			snapshot.SubPaneID = childAgentIDFromResult(snapshot.Content)
		}
		meta.Snapshots = []ttypes.ConversationMessage{*snapshot}
	case "assistant":
		meta.Snapshots = nil
		if message.ReasoningContent != "" || message.Reasoning != "" {
			content := message.ReasoningContent
			if content == "" {
				content = message.Reasoning
			}
			thinking := ttypes.ConversationMessage{
				InternalID: internalID + "-thinking", Role: "thinking", Content: content,
				Time: now, ThinkingDuration: message.ReasoningDuration,
				ThinkingDisplay: message.ReasoningDisplay,
			}
			applyTurnIdentity(&thinking, meta)
			meta.Snapshots = append(meta.Snapshots, thinking)
		}
		if message.Content.String() != "" {
			assistant := ttypes.ConversationMessage{
				InternalID: internalID + "-assistant", Role: "assistant", Content: message.Content.String(), Time: now,
			}
			applyTurnIdentity(&assistant, meta)
			meta.Snapshots = append(meta.Snapshots, assistant)
		}
	case "user":
		content := meta.DisplayContent
		if content == "" {
			content = message.DisplayContent
		}
		if content == "" {
			content = message.Content.String()
		}
		role := "user"
		if meta.DisplayRole == "assignment" {
			role = "assignment"
		}
		meta.Snapshots = []ttypes.ConversationMessage{{
			InternalID: internalID, Role: role, Content: content,
			ReferenceLabels: append([]string(nil), meta.ReferenceLabels...), Time: now,
		}}
	default:
		meta.Snapshots = []ttypes.ConversationMessage{{
			InternalID: internalID, Role: message.Role, Content: message.Content.String(), Time: now,
		}}
	}
	return meta
}

func applyTurnIdentity(snapshot *ttypes.ConversationMessage, meta sessiondb.DisplayMeta) {
	if snapshot == nil {
		return
	}
	snapshot.TurnGeneration = meta.TurnGeneration
	snapshot.ProviderID = meta.ProviderID
	snapshot.AccountID = meta.AccountID
	snapshot.ModelID = meta.ModelID
}

func applyCanonicalToolOperands(snapshot *ttypes.ConversationMessage, rawArgs string) {
	if snapshot == nil || rawArgs == "" {
		return
	}
	var params map[string]any
	if json.Unmarshal([]byte(rawArgs), &params) != nil {
		return
	}
	stringParam := func(key string) string {
		value, _ := params[key].(string)
		return value
	}
	switch snapshot.ToolName {
	case "multi_tool_use.parallel":
		snapshot.ToolInput = rawArgs
	case "exec_command":
		snapshot.ToolCommand = stringParam("cmd")
	case "execute_command", "async_execute_command":
		snapshot.ToolCommand = stringParam("command")
	case "grep_files":
		snapshot.ToolInput = stringParam("pattern")
		snapshot.ToolScope = stringParam("path")
	case "web_search", "arxiv_search":
		snapshot.ToolInput = stringParam("query")
	case "web_fetch":
		snapshot.ToolInput = stringParam("url")
	}
}

func toolCallArgsIndex(messages []provider.ChatMessage) map[string]string {
	index := make(map[string]string)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID != "" {
				index[call.ID] = call.Function.Arguments
			}
		}
	}
	return index
}

func isProviderControlMessage(message provider.ChatMessage) bool {
	return message.EffectiveVisibility() == provider.VisibilityContextOnly && message.EffectiveOrigin() != provider.OriginTurnStopped
}

func (t *displayMetaTracker) get(toolID string) sessiondb.DisplayMeta {
	if m, ok := t.metas[toolID]; ok {
		return *m
	}
	return sessiondb.DisplayMeta{}
}

func (s *Session) HasMessages() bool {
	s.msgsMu.Lock()
	hasMessages := len(s.msgs) > 0
	s.msgsMu.Unlock()
	if !hasMessages {
		s.writeMu.Lock()
		hasMessages = len(s.writeBuf) > 0
		s.writeMu.Unlock()
	}
	return hasMessages
}

func (s *Session) MessagesWithMeta() []sessiondb.MessageWithMeta {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	if len(s.msgs) == 0 {
		return nil
	}
	out := make([]sessiondb.MessageWithMeta, len(s.msgs))
	for i, m := range s.msgs {
		out[i].Msg = m
		out[i].Turn = 0
		if i < len(s.msgTurns) {
			out[i].Turn = s.msgTurns[i]
		}
		if i < len(s.displayMetas) {
			out[i].Meta = s.displayMetas[i]
		} else {
			out[i].Meta = sessiondb.DisplayMeta{DisplayRole: m.Role}
		}
		if i < len(s.createdAts) {
			out[i].CreatedAt = s.createdAts[i]
		}
	}
	return out
}

func (s *Session) appendToWriteBuf(msgs []provider.ChatMessage, turns []int, metas []sessiondb.DisplayMeta) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for i, m := range msgs {
		turn := 0
		if i < len(turns) {
			turn = turns[i]
		}
		var meta sessiondb.DisplayMeta
		if i < len(metas) {
			meta = metas[i]
		}
		s.writeBuf = append(s.writeBuf, taggedMessage{msg: m, turn: turn, meta: meta})
	}
}

func (s *Session) flushWriteBufSync() {
	s.writeMu.Lock()
	buf := s.writeBuf
	s.writeBuf = s.writeBuf[:0]
	s.writeMu.Unlock()

	if len(buf) == 0 || s.db == nil {
		return
	}

	msgs := make([]provider.ChatMessage, len(buf))
	turns := make([]int, len(buf))
	metas := make([]sessiondb.DisplayMeta, len(buf))
	for i, tm := range buf {
		msgs[i] = tm.msg
		turns[i] = tm.turn
		metas[i] = tm.meta
	}

	newSeq, err := sessiondb.ReplaceMessagesWithMeta(s.db, s.sessionID, msgs, turns, metas)
	if err != nil {
		s.logger.Error("SESSIONDB", "sync_flush_error", "error", err.Error())
		return
	}
	s.msgsMu.Lock()
	s.seqNum = newSeq
	s.msgsMu.Unlock()
}

func sanitizeMessages(msgs []provider.ChatMessage) []provider.ChatMessage {
	msgs = backfillMissingToolResults(msgs)
	for len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		if last.Role == "tool" {
			hasParent := false
			for i := len(msgs) - 2; i >= 0; i-- {
				if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
					for _, tc := range msgs[i].ToolCalls {
						if tc.ID == last.ToolCallID {
							hasParent = true
							break
						}
					}
					break
				}
			}
			if !hasParent {
				msgs = msgs[:len(msgs)-1]
				continue
			}
		}
		break
	}
	return msgs
}

func backfillMissingToolResults(msgs []provider.ChatMessage) []provider.ChatMessage {
	var result []provider.ChatMessage
	for i := 0; i < len(msgs); i++ {
		message := msgs[i]
		if message.Role == "tool" {
			// A tool result without an immediately preceding assistant tool
			// exchange is invalid for every supported provider.
			continue
		}
		result = append(result, message)
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			present := make(map[string]provider.ChatMessage)
			j := i + 1
			for j < len(msgs) && msgs[j].Role == "tool" {
				if _, duplicate := present[msgs[j].ToolCallID]; !duplicate {
					present[msgs[j].ToolCallID] = msgs[j]
				}
				j++
			}
			for _, call := range message.ToolCalls {
				if toolResult, ok := present[call.ID]; ok {
					result = append(result, toolResult)
					continue
				}
				result = append(result, provider.ChatMessage{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    provider.TextContent("[cancelled] Operation cancelled by user"),
					ToolError:  true,
				})
			}
			i = j - 1
		}
	}
	return result
}

// Reply calls remain in the provider transcript; their display snapshots are
// assistant progress messages with stable tool-call identities.
