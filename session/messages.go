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

func (t *displayMetaTracker) setPermTag(toolID, tool, tag string) {
	if toolID != "" {
		m, ok := t.metas[toolID]
		if !ok {
			m = &sessiondb.DisplayMeta{}
			t.metas[toolID] = m
		}
		m.PermTag = tag
		return
	}
	t.setPendingPermTag(tool, tag)
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
		return msgs
	}

	cutoff := units[firstRetained].start
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
		Role:    "user",
		Content: provider.TextContent(summaryText),
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
