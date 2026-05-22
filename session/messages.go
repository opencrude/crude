package session

import (
	"fmt"
	"strings"

	"crude/provider"
	"crude/sessiondb"
)

type taggedMessage struct {
	msg  provider.ChatMessage
	turn int
	meta sessiondb.DisplayMeta
}

type displayMetaTracker struct {
	metas          map[string]*sessiondb.DisplayMeta
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
	if t.pendingPermTag != "" && (t.pendingPermTool == "" || t.pendingPermTool == name) {
		m.PermTag = t.pendingPermTag
		t.pendingPermTag = ""
		t.pendingPermTool = ""
	}
}

func (t *displayMetaTracker) updateToolResult(toolID, status string, durMs int64, estTokens, linesAdded, linesRemoved int) {
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
}

func (t *displayMetaTracker) setPendingPermTag(tool, tag string) {
	t.pendingPermTag = tag
	t.pendingPermTool = tool
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
	for {
		if len(msgs) == 0 {
			break
		}
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
			content := last.Content
			for i := len(msgs) - 1; i > len(msgs)-1-len(last.ToolCalls); i-- {
				if i >= 0 && msgs[i].Role == "tool" && msgs[i].Content != "" && !strings.HasPrefix(msgs[i].Content, "[cancelled]") {
					if content == "" {
						content = msgs[i].Content
					}
				}
			}
			msgs = msgs[:len(msgs)-len(last.ToolCalls)]
			msgs[len(msgs)-1].ToolCalls = nil
			if content != "" {
				msgs[len(msgs)-1].Content = content
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
		result = append(result, msgs[i])
		if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
			presentIDs := make(map[string]bool)
			j := i + 1
			for j < len(msgs) && msgs[j].Role == "tool" {
				presentIDs[msgs[j].ToolCallID] = true
				result = append(result, msgs[j])
				j++
			}
			for _, tc := range msgs[i].ToolCalls {
				if !presentIDs[tc.ID] {
					result = append(result, provider.ChatMessage{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    "[cancelled] Operation cancelled by user",
					})
				}
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

func truncateMessages(msgs []provider.ChatMessage, ctxWindow int, model string) []provider.ChatMessage {
	if ctxWindow <= 0 || len(msgs) <= 2 {
		return msgs
	}

	tokenBudget := int(float64(ctxWindow) * 0.75)

	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		msgTokens := provider.EstimateMessageTokens(msgs[i], model)
		if used+msgTokens > tokenBudget && i > 0 {
			summary := provider.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("[Earlier conversation truncated — %d messages omitted, ~%d tokens saved]", i, used),
			}
			return append([]provider.ChatMessage{summary}, msgs[i+1:]...)
		}
		used += msgTokens
	}
	return msgs
}
