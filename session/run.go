package session

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
)

func (s *Session) Run(sender EventSender, input EventInput, blockResolveCh chan string, btwCh <-chan string) {
	l := s.logger
	l.Info("SESSION", "run_start", "session_id", s.sessionID)
	s.sender = sender
	if s.subAgentSup != nil {
		s.subAgentSup.SetSender(sender)
	}
	defer func() {
		if r := recover(); r != nil {
			l.Error("SESSION", "panic", "error", fmt.Sprintf("%v", r))
			sender.SendAgentEventWithID("error", fmt.Sprintf("Internal error: %v", r), "main")
		}
	}()

	go func() {
		l.Debug("MODEL", "models_dev_start")
		metas, err := provider.FetchModelsDevAll()
		if err != nil {
			l.Warn("MODEL", "load_error", "error", err.Error())
			return
		}
		provider.LoadModelMetas(metas)
		l.Info("MODEL", "load_ok", "count", len(metas))
	}()

	asyncPollingStop := make(chan struct{})
	defer close(asyncPollingStop)
	go func() {
		l.Debug("ASYNC", "poll_start", "interval", "1s")
		tick := time.NewTicker(1 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				cmds := s.asyncMgr.List()
				if len(cmds) > 0 {
					sender.SendAgentEvent("async_command_status", cmds)
				} else {
					sender.SendAgentEvent("async_command_status", []fileio.AsyncCmdInfo{})
				}
			case <-asyncPollingStop:
				l.Debug("ASYNC", "poll_stop")
				return
			}
		}
	}()

	for {
		select {
		case userText, ok := <-input.GetInputCh():
			if !ok {
				l.Info("SESSION", "run_exit_input_closed")
				return
			}
			if atomic.LoadInt32(&s.closing) == 1 {
				l.Info("SESSION", "run_exit_closing")
				return
			}
			s.processTurn(userText, sender, input, blockResolveCh, btwCh, l)

		case wake := <-s.wakeCh:
			if atomic.LoadInt32(&s.closing) == 1 {
				return
			}
			switch wake.Type {
			case WakeSubAgentDone:
				l.Info("SLEEP", "wake_sub_agent_done", "source", wake.SourceID)
				// Build a synthetic system message describing the sub-agent completion
				summary := ""
				if result, ok := wake.Data.(agent.SubAgentResult); ok {
					summary = result.SummaryGist
				}
				wakeText := fmt.Sprintf("[System notification: Sub-agent '%s' completed. %s]", wake.SourceID, summary)
				s.processTurn(wakeText, sender, input, blockResolveCh, btwCh, l)

			case WakeShutdown:
				l.Info("SLEEP", "wake_shutdown")
				return
			}
		}
	}
}

func (s *Session) processTurn(userText string, sender EventSender, input EventInput, blockResolveCh chan string, btwCh <-chan string, l *logger.Logger) {
	// Make a local copy of cancelCh
	cancelCh := input.GetCancelCh()

	// Drain any pending cancel signal
	select {
	case <-cancelCh:
		l.Debug("CANCEL", "cancel_drained", "iteration", "start_of_loop")
	default:
	}

	activeCfg := s.cfg.GetActiveMainCategory()
	if activeCfg.APIKey == "" {
		l.Warn("INPUT", "no_api_key")
		sender.SendAgentEvent("error", "No API key configured. Press Ctrl+S to open settings.")
		return
	}

	s.blockMu.Lock()
	isBlocked := s.pendingBlock
	s.pendingBlock = false
	s.blockMu.Unlock()
	if isBlocked {
		l.Debug("BLOCK", "block_resolve", "text_len", len(userText))
		s.blockRespMu.Lock()
		s.lastBlockResp = userText
		s.blockRespMu.Unlock()
		blockResolveCh <- userText
		return
	}

	s.turnNumber++
	s.toolCallCountMu.Lock()
	s.toolCallCount = 0
	s.toolCallCountMu.Unlock()
	s.msgCounterMu.Lock()
	s.msgCounter++
	currentMsgID := fmt.Sprintf("msg-%d", s.msgCounter)
	s.msgCounterMu.Unlock()
	l.Info("INPUT", "received", "len", len(userText), "msg_id", currentMsgID, "is_blocked", isBlocked, "turn", s.turnNumber)

	if s.db != nil {
		sessiondb.UpdateFirstPrompt(s.db, s.sessionID, userText)
		sessiondb.UpdateLastPrompt(s.db, s.sessionID, userText)
	}

	s.msgsMu.Lock()
	s.msgs = append(s.msgs, provider.ChatMessage{Role: "user", Content: userText})
	s.msgTurns = append(s.msgTurns, s.turnNumber)
	s.displayMetas = append(s.displayMetas, sessiondb.DisplayMeta{DisplayRole: "user"})
	msgsCopy := make([]provider.ChatMessage, len(s.msgs))
	copy(msgsCopy, s.msgs)
	s.msgsMu.Unlock()
	l.Info("CONV", "user_msg", "msg_id", currentMsgID, "len", len(userText))

	l.Info("AGENT", "loop_start", "msg_id", currentMsgID)

	if s.editor != nil {
		s.editor = s.editor.WithMessage(currentMsgID)
	}

	s.provider.Model = activeCfg.Model
	s.provider.SetThinkingEffort(activeCfg.ThinkingEffort)

	ag := s.newAgent()
	if s.subAgentSup != nil {
		s.subAgentSup.SetParentToolFn(ag.ToolFn)
		ag.SubAgentMgr = s.subAgentSup.Manager()
	}
	ag.SetToolCallSeqStart(s.toolCallSeq)
	var finalContent string
	var prompt string = agent.BuildSystemPrompt(s.cfg.IsMultiAgentEnabled())

	displayTracker := &displayMetaTracker{
		metas: make(map[string]*sessiondb.DisplayMeta),
	}

	ctx, cancel := context.WithCancel(context.Background())
	ag.Ctx = ctx
	go func() {
		select {
		case <-cancelCh:
			l.Debug("CANCEL", "ctx_cancelled", "msg_id", currentMsgID)
			cancel()
		case <-ctx.Done():
		}
	}()
	activeCfg = s.cfg.GetActiveMainCategory()

	var emitActive atomic.Bool
	emitActive.Store(true)

	emit := func(evt agent.AgentEvent) {
		if !emitActive.Load() {
			return
		}
		if evt.Type == "blocking" {
			data, _ := evt.Data.(map[string]string)
			if s.handleBlockingEvent(data, evt, sender, blockResolveCh) {
				return
			}
		}
		if evt.Type == "confirmation_always" {
			if data, ok := evt.Data.(map[string]string); ok {
				s.handleConfirmationAlways(data)
			}
			return
		}
		sender.SendAgentEventWithID(evt.Type, evt.Data, evt.AgentID)
		s.updateDisplayFromEvent(evt, displayTracker)
		if evt.Type == "usage" {
			s.processUsageEvent(evt.Data, activeCfg.Model)
		}
		if evt.Type == "done" {
			if content, ok := evt.Data.(string); ok {
				finalContent = content
			} else if m, ok := evt.Data.(map[string]any); ok {
				if c, ok := m["content"].(string); ok {
					finalContent = c
				}
			}
		}
	}
	ag.Emit = emit
	agentMsgs := ag.Run(ctx, msgsCopy, prompt, emit, blockResolveCh, s.sm, s.sessionID, btwCh)
	emitActive.Store(false)
	s.toolCallSeq = ag.ToolCallSeq()

	cancel()

	l.Info("AGENT", "loop_end", "len", len(finalContent))
	s.finalizeTurn(agentMsgs, displayTracker, finalContent, currentMsgID, activeCfg.Model)
}

func (s *Session) handleBlockingEvent(data map[string]string, evt agent.AgentEvent, sender EventSender, blockResolveCh chan string) bool {
	if data != nil {
		op := safety.CRUDEOp(data["operation"])
		tool := data["tool"]
		target := data["target"]
		if s.permStore.IsAlwaysAllowed(op, tool, target) {
			s.logger.Debug("PERM", "auto_allowed", "operation", op, "tool", tool, "target", target)
			blockResolveCh <- "allow"
			sender.SendAgentEventWithID("blocking_auto", evt.Data, evt.AgentID)
			return true
		}
	}
	if data != nil && data["risk"] != "" {
		riskStr := data["risk"]
		for _, r := range []safety.CommandRisk{
			safety.RiskDestructive, safety.RiskNetwork,
			safety.RiskProjectWrite, safety.RiskUnknown,
		} {
			if r.String() == riskStr {
				sr := &safety.CommandSafetyResult{Risk: r, BaseCommand: data["tool"]}
				if s.permStore.IsCommandAllowed(sr) {
					s.logger.Debug("PERM", "command_auto_allowed", "risk", riskStr, "tool", data["tool"])
					blockResolveCh <- "always"
					sender.SendAgentEventWithID("blocking_auto", evt.Data, evt.AgentID)
					return true
				}
				break
			}
		}
	}
	s.logger.Info("BLOCK", "prompt", "operation", data["operation"], "tool", data["tool"], "target", data["target"], "question", data["question"])
	s.blockMu.Lock()
	s.pendingBlock = true
	s.blockMu.Unlock()
	sender.SendAgentEventWithID(evt.Type, evt.Data, evt.AgentID)
	return true
}

func (s *Session) handleConfirmationAlways(data map[string]string) {
	opStr := data["operation"]
	tool := data["tool"]
	target := data["target"]
	s.logger.Info("PERM", "grant", "operation", opStr, "tool", tool, "target", target)

	switch tool {
	case "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		s.permStore.GrantAlways(safety.CRUDEOp(opStr), tool, target)
	default:
		for _, r := range []safety.CommandRisk{
			safety.RiskDestructive, safety.RiskNetwork,
			safety.RiskProjectWrite, safety.RiskUnknown,
		} {
			if r.String() == opStr {
				s.permStore.GrantCommandRisk(r)
				s.logger.Debug("PERM", "risk_granted", "risk", opStr)
				break
			}
		}
	}
}

func (s *Session) processUsageEvent(evtData any, model string) {
	var promptT, compT, cachedT int
	isEstimated := false
	switch d := evtData.(type) {
	case map[string]int:
		promptT = d["prompt"]
		compT = d["completion"]
		cachedT = d["prompt_cached"]
	case map[string]any:
		if v, ok := d["prompt"].(float64); ok {
			promptT = int(v)
		} else if v, ok := d["prompt"].(int); ok {
			promptT = v
		}
		if v, ok := d["completion"].(float64); ok {
			compT = int(v)
		} else if v, ok := d["completion"].(int); ok {
			compT = v
		}
		if v, ok := d["prompt_cached"].(float64); ok {
			cachedT = int(v)
		} else if v, ok := d["prompt_cached"].(int); ok {
			cachedT = v
		}
		if v, ok := d["estimated"].(bool); ok {
			isEstimated = v
		}
	}
	if promptT > 0 || compT > 0 {
		s.cost.RecordUsage(model, promptT, compT, cachedT)
		if !isEstimated {
			s.tokens.Record(promptT, compT)
		}
		var perTurnCost float64
		if c, ok := evtData.(map[string]any); ok {
			if v, ok := c["cost"].(float64); ok {
				perTurnCost = v
			}
		}
		sessionTotal := s.cost.Snapshot().TotalCost
		s.logger.Info("COST", "turn_usage", "model", model, "prompt", promptT, "completion", compT, "cached", cachedT, "turn_cost", perTurnCost, "session_total", sessionTotal, "estimated", isEstimated)
	}
}

func (s *Session) updateDisplayFromEvent(evt agent.AgentEvent, tracker *displayMetaTracker) {
	switch evt.Type {
	case "tool_call_streaming", "tool_call":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name != "" && name != "reply" && toolID != "" {
				args, _ := data["args"].(string)
				tracker.updateToolCall(toolID, name, args)
			}
		}
	case "tool_result":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			if toolID != "" {
				errFlag, _ := data["error"].(string)
				status := "success"
				if errFlag == "true" {
					status = "error"
				}
				durMs, _ := data["tool_dur_ms"].(float64)
				estTok := 0
				if v, ok := data["tool_est_tokens"].(float64); ok {
					estTok = int(v)
				}
				la := 0
				if v, ok := data["lines_added"].(float64); ok {
					la = int(v)
				}
				lr := 0
				if v, ok := data["lines_removed"].(float64); ok {
					lr = int(v)
				}
				tracker.updateToolResult(toolID, status, int64(durMs), estTok, la, lr)
			}
		}
	case "blocking_auto":
		if data, ok := evt.Data.(map[string]any); ok {
			tool, _ := data["tool"].(string)
			tracker.setPendingPermTag(tool, "auto")
		} else if data, ok := evt.Data.(map[string]string); ok {
			tool := data["tool"]
			tracker.setPendingPermTag(tool, "auto")
		}
	case "blocking":
		if data, ok := evt.Data.(map[string]string); ok {
			tracker.setPendingPermTag(data["tool"], "")
		}
	case "confirmation_always":
		if data, ok := evt.Data.(map[string]string); ok {
			tracker.setPendingPermTag(data["tool"], "always")
		}
	}
}

func (s *Session) finalizeTurn(agentMsgs []provider.ChatMessage, tracker *displayMetaTracker, finalContent string, msgID string, model string) {
	s.blockMu.Lock()
	wasPending := s.pendingBlock
	s.pendingBlock = false
	s.blockMu.Unlock()
	if wasPending {
		s.logger.Debug("BLOCK", "pending_cleared", "msg_id", msgID)
	}

	s.blockRespMu.Lock()
	if s.lastBlockResp != "" && finalContent == "" {
		finalContent = s.lastBlockResp
		s.logger.Debug("BLOCK", "block_resp_used", "len", len(s.lastBlockResp))
	}
	s.lastBlockResp = ""
	s.blockRespMu.Unlock()

	s.msgsMu.Lock()
	var persisted []provider.ChatMessage
	var persistedTurns []int
	var persistedMetas []sessiondb.DisplayMeta
	for _, m := range agentMsgs {
		if m.Role == "system" {
			continue
		}
		persisted = append(persisted, m)
		persistedTurns = append(persistedTurns, s.turnNumber)
		var dm sessiondb.DisplayMeta
		switch m.Role {
		case "tool":
			dm = tracker.get(m.ToolCallID)
		case "assistant":
			dm.DisplayRole = "assistant"
			if m.ReasoningContent != "" || m.Reasoning != "" {
				dm.DisplayRole = "thinking"
			}
		case "user":
			dm.DisplayRole = "user"
		}
		persistedMetas = append(persistedMetas, dm)
	}
	preSanitize := len(persisted)
	persisted = sanitizeMessages(persisted)
	delta := len(persisted) - preSanitize
	if delta < 0 {
		removedCount := -delta
		persistedTurns = persistedTurns[:len(persisted)]
		persistedMetas = persistedMetas[:len(persisted)]
		s.logger.Debug("SANITIZE", "messages_removed", "count", removedCount, "remaining", len(persisted))
	} else if delta > 0 {
		extra := delta
		extraTurns := make([]int, extra)
		for i := range extraTurns {
			extraTurns[i] = s.turnNumber
		}
		persistedTurns = append(persistedTurns, extraTurns...)
		extraMetas := make([]sessiondb.DisplayMeta, extra)
		for i := range extraMetas {
			extraMetas[i] = sessiondb.DisplayMeta{DisplayRole: "tool", ToolStatus: "cancelled"}
		}
		persistedMetas = append(persistedMetas, extraMetas...)
		s.logger.Debug("SANITIZE", "messages_backfilled", "count", delta, "total", len(persisted))
	}
	if len(persisted) == 0 {
		persistedTurns = nil
		persistedMetas = nil
	}
	ctxWindow := provider.LookupContextWindow(model)
	preTruncate := len(persisted)
	persisted = truncateMessages(persisted, ctxWindow, model)
	if len(persisted) < preTruncate {
		persistedTurns = persistedTurns[preTruncate-len(persisted):]
		persistedMetas = persistedMetas[preTruncate-len(persisted):]
		s.logger.Info("TRUNCATE", "messages", "before", preTruncate, "after", len(persisted), "window", ctxWindow)
	}
	if len(persisted) > len(persistedTurns) {
		extra := len(persisted) - len(persistedTurns)
		prefix := make([]int, extra)
		persistedTurns = append(prefix, persistedTurns...)
		metaPrefix := make([]sessiondb.DisplayMeta, extra)
		persistedMetas = append(metaPrefix, persistedMetas...)
	}
	if len(persisted) > 0 {
		s.msgs = persisted
		s.msgTurns = persistedTurns
		s.displayMetas = persistedMetas
	} else if finalContent != "" {
		s.msgs = append(s.msgs, provider.ChatMessage{Role: "assistant", Content: finalContent})
		s.msgTurns = append(s.msgTurns, s.turnNumber)
		s.displayMetas = append(s.displayMetas, sessiondb.DisplayMeta{DisplayRole: "assistant"})
	}

	if s.db != nil && len(s.msgs) > 0 {
		s.appendToWriteBuf(s.msgs, s.msgTurns, s.displayMetas)
	}

	s.msgsMu.Unlock()

	if s.db != nil {
		s.flushWg.Add(1)
		go s.flushWriteBuf()
	}

	if finalContent != "" {
		s.logger.Info("CONV", "assistant_msg", "msg_id", msgID, "len", len(finalContent))
	}
}
