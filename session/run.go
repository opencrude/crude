package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
)

func (s *Session) RunOrchestrator(o *Orchestrator, sender EventSender) {
	l := s.logger
	l.Info("SESSION", "run_start", "orchestrator_id", o.ID)
	s.sender = sender
	if o.subAgentSup != nil {
		o.subAgentSup.SetSender(sender)
		o.subAgentSup.SetOrchestratorID(o.ID)
	}
	defer func() {
		if r := recover(); r != nil {
			l.Error("SESSION", "panic", "error", fmt.Sprintf("%v", r))
			sender.SendAgentEventWithID("error", fmt.Sprintf("Internal error: %v", r), o.ID)
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
		case userInput, ok := <-o.InputCh:
			if !ok {
				l.Info("SESSION", "run_exit_input_closed", "orchestrator_id", o.ID)
				return
			}
			if o.closing.Load() {
				l.Info("SESSION", "run_exit_closing", "orchestrator_id", o.ID)
				return
			}
			if s.isHardCancelled() {
				s.clearHardCancel()
				s.drainWakeCh()
			}
			s.processTurnOrchestrator(userInput, o, sender, l)

		case wake := <-s.wakeCh:
			if o.closing.Load() {
				return
			}
			if s.isHardCancelled() {
				l.Info("SLEEP", "wake_suppressed_hard_cancel", "source", wake.SourceID)
				continue
			}
			switch wake.Type {
			case WakeSubAgentDone:
				l.Info("SLEEP", "wake_sub_agent_done", "source", wake.SourceID)

				if s.IsAndClearReportConsumed(wake.SourceID) {
					l.Info("SLEEP", "wake_suppressed_already_consumed", "source", wake.SourceID)
					continue
				}

				summary := ""
				reportPath := ""
				if result, ok := wake.Data.(agent.SubAgentResult); ok {
					summary = result.SummaryGist
					reportPath = result.ResultPath
				}
				wakeText := fmt.Sprintf("[System notification: Sub-agent '%s' completed. %s]", wake.SourceID, summary)
				if reportPath != "" {
					wakeText += fmt.Sprintf("\nReport available at: %s", reportPath)
				}
				s.processTurnOrchestrator(provider.PlainInput(wakeText), o, sender, l)

			case WakeShutdown:
				l.Info("SLEEP", "wake_shutdown")
				return
			}
		}
	}
}

func (s *Session) processTurnOrchestrator(userInput provider.UserInput, o *Orchestrator, sender EventSender, l *logger.Logger) {
	cancelCh := o.CancelCh

	// Drain any pending cancel signal
	select {
	case <-cancelCh:
		l.Debug("CANCEL", "cancel_drained", "iteration", "start_of_loop")
	default:
	}

	activeCfg := s.cfg.GetActiveDefaultCategory()
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
		l.Debug("BLOCK", "block_resolve", "text_len", len(userInput.Text))
		s.blockRespMu.Lock()
		s.lastBlockResp = userInput.Text
		s.blockRespMu.Unlock()
		o.BlockResolveCh <- userInput.Text
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
	l.Info("INPUT", "received", "len", len(userInput.Text), "refs", len(userInput.ReferenceIDs), "msg_id", currentMsgID, "is_blocked", isBlocked, "turn", s.turnNumber)

	userMsg := s.buildUserMessage(userInput)

	if s.db != nil {
		sessiondb.UpdateFirstPrompt(s.db, s.sessionID, userInput.Text)
		sessiondb.UpdateLastPrompt(s.db, s.sessionID, userInput.Text)
	}

	s.msgsMu.Lock()
	s.msgs = append(s.msgs, userMsg)
	s.msgTurns = append(s.msgTurns, s.turnNumber)
	s.displayMetas = append(s.displayMetas, sessiondb.DisplayMeta{DisplayRole: "user"})
	msgsCopy := make([]provider.ChatMessage, len(s.msgs))
	copy(msgsCopy, s.msgs)
	s.msgsMu.Unlock()
	l.Info("CONV", "user_msg", "msg_id", currentMsgID, "len", len(userInput.Text))

	l.Info("AGENT", "loop_start", "msg_id", currentMsgID)

	if s.editor != nil {
		s.editor = s.editor.WithMessage(currentMsgID)
	}

	s.provider.Model = activeCfg.Model
	s.provider.SetThinkingEffort(activeCfg.ThinkingEffort)
	provider.SetModelCap(activeCfg.Model, activeCfg.ModelSupportsVision, activeCfg.ModelSupportsTools)

	ag := s.newAgent()
	if o.subAgentSup != nil {
		o.subAgentSup.SetParentToolFn(ag.ToolFn)
		ag.SubAgentMgr = o.subAgentSup.Manager()
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
			select {
			case o.BlockResolveCh <- "deny":
				l.Debug("CANCEL", "block_unblocked", "msg_id", currentMsgID)
			default:
			}
			if o.subAgentSup != nil {
				cancelled := o.subAgentSup.CancelAllActive()
				if len(cancelled) > 0 {
					l.Info("CANCEL", "sub_agents_cancelled", "count", len(cancelled))
				}
			}
		case <-ctx.Done():
		}
	}()
	activeCfg = s.cfg.GetActiveDefaultCategory()

	btwMsgCh := make(chan provider.ChatMessage, 8)
	go func() {
		for {
			select {
			case btwInput, ok := <-o.BtwCh:
				if !ok {
					close(btwMsgCh)
					return
				}
				btwMsg := s.buildBtwMessage(btwInput)
				select {
				case btwMsgCh <- btwMsg:
				case <-ctx.Done():
					close(btwMsgCh)
					return
				}
			case <-ctx.Done():
				close(btwMsgCh)
				return
			}
		}
	}()

	var emitActive atomic.Bool
	emitActive.Store(true)

	emit := func(evt agent.AgentEvent) {
		if !emitActive.Load() {
			return
		}
		if evt.Type == "blocking" {
			data, _ := evt.Data.(map[string]string)
			if o.handleBlockingEvent(data, evt, sender) {
				return
			}
		}
		if evt.Type == "confirmation_always" {
			if data, ok := evt.Data.(map[string]string); ok {
				s.handleConfirmationAlways(data)
			}
			return
		}
		agentID := evt.AgentID
		if agentID == "" {
			agentID = o.ID
		}
		sender.SendAgentEventWithID(evt.Type, evt.Data, agentID)
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
	agentMsgs := ag.Run(ctx, msgsCopy, prompt, emit, o.BlockResolveCh, s.sm, s.sessionID, btwMsgCh)
	emitActive.Store(false)
	s.toolCallSeq = ag.ToolCallSeq()

	cancel()

	l.Info("AGENT", "loop_end", "len", len(finalContent))
	s.finalizeTurn(agentMsgs, displayTracker, finalContent, currentMsgID, activeCfg.Model)
}

func (o *Orchestrator) handleBlockingEvent(data map[string]string, evt agent.AgentEvent, sender EventSender) bool {
	s := o.session
	if data != nil {
		op := safety.CRUDEOp(data["operation"])
		tool := data["tool"]
		target := data["target"]
		if s.permStore.IsAlwaysAllowed(op, tool, target) {
			s.logger.Debug("PERM", "auto_allowed", "operation", op, "tool", tool, "target", target)
			o.BlockResolveCh <- "allow"
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
					o.BlockResolveCh <- "always"
			agentID := evt.AgentID
			if agentID == "" {
				agentID = o.ID
			}
			sender.SendAgentEventWithID("blocking_auto", evt.Data, agentID)
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
		s.msgs = append(s.msgs, provider.ChatMessage{Role: "assistant", Content: provider.TextContent(finalContent)})
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

func (s *Session) buildUserMessage(input provider.UserInput) provider.ChatMessage {
	if len(input.ReferenceIDs) == 0 || s.db == nil {
		return provider.NewTextMessage("user", input.Text)
	}

	// Check if the current model supports vision
	hasVision := provider.SupportsVision(s.provider.Model)

	// Load only the referenced prompt references (filtered query)
	refs, err := sessiondb.LoadPromptReferencesByIDs(s.db, input.ReferenceIDs)
	if err != nil {
		s.logger.Warn("REF", "load_refs_error", "error", err.Error())
		return provider.NewTextMessage("user", input.Text)
	}

	// Build an order-preserving map keyed by reference ID
	refMap := make(map[string]sessiondb.Reference, len(refs))
	for _, r := range refs {
		refMap[r.ID] = r
	}

	var parts []provider.ContentPart
	if input.Text != "" {
		parts = append(parts, provider.ContentPart{Type: "text", Text: input.Text})
	}

	for _, id := range input.ReferenceIDs {
		ref, ok := refMap[id]
		if !ok || !ref.Active() {
			continue
		}
		switch ref.Kind {
		case sessiondb.KindPastedImage:
			if !hasVision {
				// Model doesn't support images; add text description instead
				label := ref.DisplayName()
				desc := fmt.Sprintf("--- %s (image", label)
				if ref.FilePath != "" {
					desc += fmt.Sprintf(": %s", ref.FilePath)
				}
				if ref.Mime != "" {
					desc += fmt.Sprintf(", %s", ref.Mime)
				}
				desc += ") ---"
				parts = append(parts, provider.ContentPart{Type: "text", Text: desc})
				continue
			}
			var dataURL string
			if ref.FilePath != "" {
				// Validate file size before reading
				if warning := provider.FileSizeExceeded(ref.FilePath, provider.MaxImageFileSizeFor(s.provider.Model)); warning != "" {
					s.logger.Warn("REF", "image_too_large", "warning", warning)
					parts = append(parts, provider.ContentPart{
						Type: "text",
						Text: fmt.Sprintf("--- %s (image omitted: too large) ---", ref.DisplayName()),
					})
					continue
				}
				// Read from file (preferred path — no base64 bloat in DB)
				imgData, err := os.ReadFile(ref.FilePath)
				if err != nil {
					s.logger.Warn("REF", "image_read_error", "path", ref.FilePath, "error", err.Error())
					continue
				}
				mime := ref.Mime
				if mime == "" {
					if detected, ok := provider.ImageMimeFromExt(filepath.Ext(ref.FilePath)); ok {
						mime = detected
					}
				}
				if mime == "" {
					mime = "image/png"
				}
				dataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(imgData)
			} else if ref.Content != "" {
				// Validate inline content size (base64 string length approximates byte size)
				if int64(len(ref.Content)) > provider.MaxInlineContentSizeFor(s.provider.Model) {
					s.logger.Warn("REF", "inline_image_too_large", "content_len", len(ref.Content))
					parts = append(parts, provider.ContentPart{
						Type: "text",
						Text: fmt.Sprintf("--- %s (image omitted: too large) ---", ref.DisplayName()),
					})
					continue
				}
				// Fallback for legacy entries or inline content
				mime := ref.Mime
				if mime == "" {
					mime = "image/png"
				}
				dataURL = "data:" + mime + ";base64," + ref.Content
			}
			if dataURL == "" {
				continue
			}
			parts = append(parts, provider.ContentPart{
				Type:     "image_url",
				ImageURL: &provider.ImageURL{URL: dataURL},
			})
		case sessiondb.KindPastedText:
			parts = append(parts, provider.ContentPart{Type: "text", Text: ref.Content})
		case sessiondb.KindPastedTextFile, sessiondb.KindSelectedFile:
			content := ref.Content
			if content == "" && ref.FilePath != "" {
				// Validate file size before reading
				if warning := provider.FileSizeExceeded(ref.FilePath, provider.MaxTextFileSizeFor(s.provider.Model)); warning != "" {
					s.logger.Warn("REF", "file_too_large", "warning", warning)
					parts = append(parts, provider.ContentPart{
						Type: "text",
						Text: fmt.Sprintf("--- %s (omitted: too large) ---", ref.DisplayName()),
					})
					continue
				}
				data, err := os.ReadFile(ref.FilePath)
				if err == nil {
					content = string(data)
				}
			}
			if content != "" {
				label := ref.DisplayName()
				parts = append(parts, provider.ContentPart{
					Type: "text",
					Text: fmt.Sprintf("--- %s ---\n%s", label, content),
				})
			}
		}
	}

	if len(parts) == 0 {
		return provider.NewTextMessage("user", input.Text)
	}
	return provider.NewMultiPartMessage("user", parts)
}

const btwUrgentPrefix = "[USER INTERRUPTION — READ THIS NOW]\nThe user interrupted your work with an important message while you were running. Stop what you're doing, read this carefully, and adjust your approach accordingly. This takes priority over whatever you are doing.\n\n"

// buildBtwMessage creates a ChatMessage for a BTW (by-the-way) interruption.
// It resolves any reference IDs via buildUserMessage and prepends the urgent
// interruption prefix so the LLM prioritises the message.
func (s *Session) buildBtwMessage(input provider.UserInput) provider.ChatMessage {
	msg := s.buildUserMessage(input)
	if msg.Content.IsText() {
		return provider.ChatMessage{
			Role:    "user",
			Content: provider.TextContent(btwUrgentPrefix + msg.Content.String()),
		}
	}
	// Multipart: insert the urgent prefix as a text part at the beginning
	prefixPart := provider.ContentPart{Type: "text", Text: btwUrgentPrefix}
	parts := []provider.ContentPart{prefixPart}
	parts = append(parts, msg.Content.Parts()...)
	return provider.NewMultiPartMessage("user", parts)
}
