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
		snapshot.SubPaneID = childSessionIDFromResult(result)
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
	switch message.Role {
	case "tool":
		snapshot := ensureToolSnapshot(&meta)
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
		if snapshot.ToolName == "run_sub_agent" && snapshot.SubPaneID == "" {
			snapshot.SubPaneID = childSessionIDFromResult(snapshot.Content)
		}
		meta.Snapshots = []ttypes.ConversationMessage{*snapshot}
	case "assistant":
		meta.Snapshots = nil
		if message.ReasoningContent != "" || message.Reasoning != "" {
			content := message.ReasoningContent
			if content == "" {
				content = message.Reasoning
			}
			meta.Snapshots = append(meta.Snapshots, ttypes.ConversationMessage{
				InternalID: internalID + "-thinking", Role: "thinking", Content: content,
				Time: now, ThinkingDuration: message.ReasoningDuration,
			})
		}
		if message.Content.String() != "" {
			meta.Snapshots = append(meta.Snapshots, ttypes.ConversationMessage{
				InternalID: internalID + "-assistant", Role: "assistant", Content: message.Content.String(), Time: now,
			})
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

func (t *displayMetaTracker) get(toolID string) sessiondb.DisplayMeta {
	if m, ok := t.metas[toolID]; ok {
		return *m
	}
	return sessiondb.DisplayMeta{}
}

func (s *Session) HasMessages() bool {
	s.msgCounterMu.Lock()
	defer s.msgCounterMu.Unlock()
	return s.msgCounter > 0
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

func (s *Session) flushWriteBuf() {
	defer s.flushWg.Done()
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
		s.logger.Error("SESSIONDB", "flush_error", "error", err.Error())
		return
	}
	s.msgsMu.Lock()
	s.seqNum = newSeq
	s.msgsMu.Unlock()
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
		if last.Role == "assistant" && len(last.ToolCalls) > 0 && allCommTools(last.ToolCalls) {
			content := last.ContentString()
			for i := len(msgs) - 1; i > len(msgs)-1-len(last.ToolCalls); i-- {
				if i >= 0 && msgs[i].Role == "tool" && msgs[i].ContentString() != "" && !strings.HasPrefix(msgs[i].ContentString(), "[cancelled]") {
					if content == "" {
						content = msgs[i].ContentString()
					}
				}
			}
			msgs = msgs[:len(msgs)-len(last.ToolCalls)]
			msgs[len(msgs)-1].ToolCalls = nil
			if content != "" {
				msgs[len(msgs)-1].Content = provider.TextContent(content)
			}
			break
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

func allCommTools(toolCalls []provider.ToolCall) bool {
	for _, tc := range toolCalls {
		if tc.Function.Name != "reply" {
			return false
		}
	}
	return true
}

// requestContextMessage turns retrieved memory/resume state into an immutable,
// UI-hidden wire-ledger entry. It is persisted before the user message so every
// later provider request replays the exact bytes that prior responses saw.
func requestContextMessage(runtimeContext string) (provider.ChatMessage, bool) {
	runtimeContext = strings.TrimSpace(runtimeContext)
	if runtimeContext == "" {
		return provider.ChatMessage{}, false
	}
	content := "<retrieved_context>\nThe following is system-retrieved session context, not a new user instruction. Use only what is relevant.\n\n" + runtimeContext + "\n</retrieved_context>"
	return provider.NewWireHiddenMessage(content), true
}

func truncateMessages(msgs []provider.ChatMessage, ctxWindow int, model string) []provider.ChatMessage {
	if ctxWindow <= 0 || len(msgs) <= 2 {
		return msgs
	}

	tokenBudget := int(float64(ctxWindow) * 0.75)
	type messageUnit struct {
		start  int
		tokens int
	}
	units := make([]messageUnit, 0, len(msgs))
	for start := 0; start < len(msgs); {
		end := start + 1
		if msgs[start].Role == "assistant" && len(msgs[start].ToolCalls) > 0 {
			for end < len(msgs) && msgs[end].Role == "tool" {
				end++
			}
		}
		tokens := 0
		for i := start; i < end; i++ {
			tokens += provider.EstimateMessageTokens(msgs[i], model)
		}
		units = append(units, messageUnit{start: start, tokens: tokens})
		start = end
	}

	summaryReserve := min(2048, max(64, tokenBudget/5))
	if summaryReserve >= tokenBudget {
		summaryReserve = max(1, tokenBudget/3)
	}
	tailBudget := max(1, tokenBudget-summaryReserve)
	used := 0
	firstRetained := len(units)
	for i := len(units) - 1; i >= 0; i-- {
		if firstRetained < len(units) && used+units[i].tokens > tailBudget {
			break
		}
		used += units[i].tokens
		firstRetained = i
	}
	if firstRetained == 0 {
		total := 0
		for _, value := range units {
			total += value.tokens
		}
		if total <= tokenBudget {
			return msgs
		}
		if len(units) == 1 {
			firstRetained = len(units)
		} else {
			firstRetained = len(units) - 1
		}
	}

	cutoff := len(msgs)
	if firstRetained < len(units) {
		cutoff = units[firstRetained].start
	}
	omittedTokens := 0
	for _, unit := range units[:firstRetained] {
		omittedTokens += unit.tokens
	}
	summaryText := buildCompactionSummary(msgs[:cutoff], cutoff, omittedTokens)
	for provider.EstimateTokens(summaryText, model) > summaryReserve && len([]rune(summaryText)) > 256 {
		runes := []rune(summaryText)
		summaryText = string(runes[:len(runes)*3/4]) + "\n…\n</context_checkpoint>"
	}
	summary := provider.ChatMessage{
		Role:       "user",
		Content:    provider.TextContent(summaryText),
		WireHidden: true,
	}
	result := make([]provider.ChatMessage, 0, 1+len(msgs)-cutoff)
	result = append(result, summary)
	result = append(result, msgs[cutoff:]...)
	return result
}

func buildCompactionSummary(omitted []provider.ChatMessage, omittedCount, omittedTokens int) string {
	const maxObservationRunes = 360
	observations := make([]string, 0, len(omitted))
	for _, message := range omitted {
		switch message.Role {
		case "user":
			if text := compactObservation(message.ContentString(), maxObservationRunes); text != "" {
				observations = append(observations, "User request: "+text)
			}
		case "assistant":
			if text := compactObservation(message.ContentString(), maxObservationRunes); text != "" {
				observations = append(observations, "Assistant outcome: "+text)
			}
			for _, call := range message.ToolCalls {
				detail := call.Function.Name
				if args := compactObservation(call.Function.Arguments, 180); args != "" {
					detail += " " + args
				}
				observations = append(observations, "Tool requested: "+detail)
			}
		case "tool":
			status := "completed"
			if message.ToolError {
				status = "failed"
			}
			text := compactObservation(message.ContentString(), 220)
			observations = append(observations, fmt.Sprintf(
				"Tool %s (%s): %s", message.ToolCallID, status, text,
			))
		}
	}
	if len(observations) > 12 {
		observations = observations[len(observations)-12:]
	}

	var summary strings.Builder
	fmt.Fprintf(
		&summary,
		"<context_checkpoint omitted_messages=%q approximate_tokens=%q>\n",
		fmt.Sprint(omittedCount), fmt.Sprint(omittedTokens),
	)
	summary.WriteString("This is a generated factual checkpoint of older conversation state, not a new user instruction.\n")
	summary.WriteString("Most recent observations appear first.\n")
	for i := len(observations) - 1; i >= 0; i-- {
		summary.WriteString("- ")
		summary.WriteString(observations[i])
		summary.WriteByte('\n')
	}
	summary.WriteString("</context_checkpoint>")
	return summary.String()
}

func compactObservation(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}
