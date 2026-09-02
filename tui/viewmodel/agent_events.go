package viewmodel

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/provider"
	"crude/tui/screens/confirmation"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// memoryContextPaths collects IDE context for the memory system: the
// active editor file first, then other open editor tabs. The backend uses
// these to boost recall of memories scoped to the same paths.
func (m *App) memoryContextPaths() []string {
	if m.EditorState == nil {
		return nil
	}
	var paths []string
	if m.EditorState.ActiveView != "" {
		paths = append(paths, m.EditorState.ActiveView)
	}
	for path := range m.EditorState.Tabs {
		if path == m.EditorState.ActiveView {
			continue
		}
		if len(paths) >= 8 {
			break
		}
		paths = append(paths, path)
	}
	return paths
}

func (m *App) submitInput(sessionID, text string, refIDs []string) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	input := provider.InputWithRefs(text, refIDs)
	input.ActivePaths = m.memoryContextPaths()
	if m.Workspace == nil || !m.SubmitAgentInput(sessionID, input) {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Message was not sent: this agent already has an active turn")
		}
		return nil
	}
	agent := n.State
	referenceLabels := m.referenceLabels(refIDs)
	agent.Messages.AppendMessage(workspace.Message{
		Role:            ttypes.RoleUser,
		Content:         text,
		ReferenceLabels: referenceLabels,
		Time:            time.Now(),
	})
	if len(m.History) == 0 || m.History[len(m.History)-1] != text {
		m.History = append(m.History, text)
	}
	if len(m.History) > 500 {
		m.History = m.History[len(m.History)-500:]
	}
	if OnHistorySave != nil {
		OnHistorySave(m.History)
	}
	agent.SubmitInput(text)
	m.syncAgentMessages(sessionID, true)
	if m.StatusBar != nil {
		m.StatusBar.SetThinkingForAgent(sessionID, true, false, false)
	}
	return nil
}

func (m *App) submitBtw(sessionID, text string, refIDs []string) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	agent := n.State
	referenceLabels := m.referenceLabels(refIDs)
	messageID := agent.Messages.AppendPendingBtw(workspace.Message{
		Role:            ttypes.RoleUser,
		Content:         text,
		ReferenceLabels: referenceLabels,
		Time:            time.Now(),
	})
	input := provider.InputWithRefs(text, refIDs)
	input.InternalID = messageID
	if m.Workspace == nil || !m.SubmitAgentBtw(sessionID, input) {
		agent.Messages.RemovePendingBtwIDs([]string{messageID})
		if m.StatusBar != nil {
			m.StatusBar.SetError("BTW was not queued: this turn is no longer accepting notes")
		}
		return nil
	}
	if len(m.History) == 0 || m.History[len(m.History)-1] != text {
		m.History = append(m.History, text)
	}
	if len(m.History) > 500 {
		m.History = m.History[len(m.History)-500:]
	}
	if OnHistorySave != nil {
		OnHistorySave(m.History)
	}
	m.syncPendingBtwCount()
	m.syncAgentMessages(sessionID, true)
	return nil
}

func (m *App) referenceLabels(refIDs []string) []string {
	store := m.RefStore()
	if store == nil || len(refIDs) == 0 {
		return nil
	}
	labels := make([]string, 0, len(refIDs))
	seen := make(map[string]bool, len(refIDs))
	for _, id := range refIDs {
		if id == "" || seen[id] {
			continue
		}
		ref, ok := store.GetReference(id)
		if !ok || !ref.Active() || ref.DisplayName() == "" {
			continue
		}
		seen[id] = true
		labels = append(labels, ref.DisplayName())
	}
	return labels
}

func (m *App) handleAgentEvent(evt ttypes.AgentEventMsg) tea.Cmd {
	return m.handleAgentDomainEvent(workspace.AgentEvent{
		Type:    evt.Type,
		Data:    evt.Data,
		AgentID: evt.AgentID,
	})
}

func (m *App) handleAgentDomainEvent(evt workspace.AgentEvent) tea.Cmd {
	if evt.Type == "btw_consumed" {
		if node := m.AgentMgr.Get(evt.AgentID); node != nil && node.State != nil && node.State.Messages != nil {
			data, _ := evt.Data.(map[string]any)
			internalID, _ := data["internal_id"].(string)
			node.State.Messages.ConsumePendingBtw(internalID)
			m.syncAgentMessages(evt.AgentID, false)
		}
		m.syncPendingBtwCount()
		m.syncAgentWindowTabs()
		return nil
	}
	if evt.Type == "btw_cleared" {
		if node := m.AgentMgr.Get(evt.AgentID); node != nil && node.State != nil && node.State.Messages != nil {
			node.State.Messages.RemovePendingBtwIDs(agentEventStringSlice(evt.Data, "internal_ids"))
			m.syncAgentMessages(evt.AgentID, true)
		}
		m.syncPendingBtwCount()
		return nil
	}
	if evt.Type == "memory_saved" {
		// Background extraction saved durable memories. Surface it visibly
		// but without blocking (status line, not a modal or chat message).
		if data, ok := evt.Data.(map[string]any); ok && m.StatusBar != nil {
			count, _ := data["count"].(int)
			if count > 0 {
				notice := fmt.Sprintf("Memory: saved %d from this session", count)
				if titles, ok := data["titles"].([]string); ok && len(titles) > 0 {
					notice = fmt.Sprintf("Memory: saved %q", titles[0])
					if count > 1 {
						notice += fmt.Sprintf(" +%d more", count-1)
					}
				}
				m.StatusBar.SetInfo(notice)
			}
		}
		if m.memoryBrowser != nil {
			m.memoryBrowser.Refresh()
			m.markMemoryBrowserDirty()
		}
		return nil
	}
	if evt.Type == "async_command_status" || evt.Type == "retry" {
		switch evt.Type {
		case "async_command_status":
			if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
				m.updateSessionBackgroundJobs(evt.AgentID, cmds, time.Now())
			}
			m.syncAgentWindowTabs()
			return nil
		case "retry":
			switch d := evt.Data.(type) {
			case map[string]any:
				attempt := 0
				switch a := d["attempt"].(type) {
				case float64:
					attempt = int(a)
				case int:
					attempt = a
				}
				backoff, _ := d["backoff"].(float64)
				m.StatusBar.SetRetry(fmt.Sprintf("⚠ retrying in %.0fs... (attempt %d)", backoff, attempt))
			case string:
				m.StatusBar.SetRetry(d)
			}
		}
		m.syncAgentWindowTabs()
		return nil
	}

	agentID := evt.AgentID
	if agentID == "" {
		m.syncAgentWindowTabs()
		return nil
	}

	agentNode := m.AgentMgr.Get(agentID)
	var agent *workspace.AgentState
	if agentNode != nil {
		agent = agentNode.State
	}

	if agentNode == nil || agent == nil {
		// Check if this is an orchestrator (no parent) or a sub-agent
		evtData, _ := evt.Data.(map[string]string)
		parentID := evtData["parent_id"]

		if parentID == "" {
			// Orchestrator nodes are registered with their live channels before
			// normal events arrive. Never manufacture a channel-less fallback for
			// stale events after a tab has closed.
			if existing := m.AgentMgr.Get(agentID); existing != nil {
				sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, agentID)
				_ = sp
				proj := m.subPaneProjections.Get(agentID, workspace.PaneAgent)
				proj.renderer.SetModelName(m.ModelName)
				proj.renderer.SetUsername(m.Username)
				proj.renderer.SetSubPaneContentFn(m.renderSubPaneContentForApp)
				agent = workspace.NewAgentState(agentID, sp.Messages, m.Logger)
				existing.State = agent
				agentNode = existing
			} else {
				if m.Logger != nil {
					m.Logger.Warn("AGENT", "event_for_unknown_orchestrator", "id", agentID, "type", evt.Type)
				}
				return nil
			}
			m.AgentMgr.SetActive(agentID)
			m.syncAgentWindowTabs()
		} else {
			// A sub-agent start can arrive after its orchestrator tab was
			// closed. The parent tree is the lifecycle authority: without it,
			// creating only a pane/projection would produce a ghost that the
			// status bar can see but the Agents panel cannot.
			if m.AgentMgr.Get(parentID) == nil {
				if m.Logger != nil {
					m.Logger.Warn(
						"AGENT", "event_for_unknown_subagent_parent",
						"id", agentID, "parent_id", parentID, "type", evt.Type,
					)
				}
				return nil
			}
			// Sub-agent: create sub-pane
			label := evtData["agent_name"]
			if label == "" {
				label = agentID
			}
			sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, label)
			sp.Title = label
			subModel := m.ModelName
			if model, ok := evtData["model"]; ok && model != "" {
				subModel = model
			}
			proj := m.subPaneProjections.Get(agentID, workspace.PaneAgent)
			proj.renderer.SetModelName(subModel)
			proj.renderer.SetUsername(m.Username)
			// Nested subpanes (e.g. a promoted thinking block inside this
			// sub-agent's own chat) render through the same registry.
			proj.renderer.SetSubPaneContentFn(m.renderSubPaneContentForApp)
			agent = workspace.NewAgentState(agentID, sp.Messages, m.Logger)
			if assignment := evtData["prompt"]; assignment != "" {
				agent.Messages.AppendMessage(workspace.Message{
					Role: workspace.MessageRoleAssignment, Content: assignment, Time: time.Now(),
				})
			}
			// Keep the canonical label intact. Tabs and sidebar rows own their
			// width-specific elision; truncating here permanently loses identity.
			modelStr := m.ModelName
			if md, ok := evtData["model"]; ok && md != "" {
				modelStr = md
			}
			cat := ""
			if c, ok := evtData["category"]; ok {
				cat = c
			}
			agentNode = m.AgentMgr.AddSubAgent(agentID, parentID, label, modelStr, cat, agent)
			if agentNode == nil {
				m.PaneMgr.Remove(agentID)
				m.subPaneProjections.Remove(agentID)
				return nil
			}
			if parent := m.AgentMgr.Get(parentID); parent != nil && parent.State != nil && parent.State.Messages != nil {
				if !parent.State.Messages.AttachSubPaneToLatestTool("run_sub_agent", agentID) {
					parent.State.Messages.AppendMessage(workspace.Message{
						Role:      ttypes.RoleTool,
						Content:   agentID,
						SubPaneID: agentID,
						Time:      time.Now(),
					})
				}
				m.syncAgentMessages(parentID, false)
			}
		}
	} else if agent.Completed {
		if evt.Type == "done" || evt.Type == "error" {
			m.handleSubPaneCompletion(agentID)
		}
		m.syncAgentWindowTabs()
		return nil
	}
	if agent == nil {
		return nil
	}
	if (evt.Type == "blocking" || evt.Type == "safety_alert") && m.AgentMgr.ActiveID() != agentID {
		m.switchToAgentView(agentID)
	}

	switch evt.Type {
	case "sub_agent_started":
		m.handleSubAgentStarted(evt)
	case "sub_agent_completed":
		m.handleSubAgentCompleted(evt)
		return nil
	case "sub_agent_question":
		m.handleSubAgentQuestion(evt)
	case "file_claim":
		m.handleFileClaim(evt)
	}

	thinkingTarget := m.getThinkingTarget(agentID, agent)

	// agent is guaranteed non-nil past the guard above.
	hasThinking := agent.ThinkingPaneID != ""

	cmd := agent.HandleEvent(evt, thinkingTarget,
		func(string) {},
		func(thinking, streaming, blocking bool) {
			m.StatusBar.SetThinkingForAgent(agentID, thinking, streaming, blocking)
		},
		func() {
			m.OverlayState.SetBlocking(true)
			m.StatusBar.SetStatusLine("")
			m.StatusBar.SetThinkingForAgent(agentID, false, false, true)
		},
		func(question, operation, tool, target, risk, reason, suggestion string) {
			_, projection := m.sessionProjectionForAgent(agentID)
			if projection == nil || projection.confirmation == nil {
				return
			}
			projection.confirmation.Reset(confirmation.Details{Question: question, Operation: operation, Tool: tool, Target: target, Risk: risk, Reason: reason, Suggestion: suggestion})
			projection.confirmationAgentID = agentID
			projection.confirmationBlockID = agent.PendingBlockID
			m.openSessionConfirmationPanel(projection)
		},
		func(questionsJSON string) {
			_, projection := m.sessionProjectionForAgent(agentID)
			if projection == nil || projection.question == nil {
				return
			}
			n, err := projection.question.ResetWithError(questionsJSON)
			if err != nil || n == 0 {
				m.StatusBar.SetError("Agent sent an invalid question request")
				detail := "question payload could not be rendered"
				if err != nil {
					detail = err.Error()
				}
				m.RejectQuestion(agentID, agent.PendingBlockID, detail)
				return
			}
			projection.questionAgentID = agentID
			projection.questionBlockID = agent.PendingBlockID
			m.openSessionQuestionPanel(projection)
		},
		func() {
			projection := m.activeSessionProjection()
			m.OverlayState.SetBlocking(false)
			if projection != nil && projection.confirmation != nil {
				projection.confirmation.Dismiss()
				m.closeProjectionPanel(projection, sessionConfirmationPanelID)
			}
			if projection != nil && projection.question != nil {
				projection.question.Dismiss()
				m.closeProjectionPanel(projection, sessionQuestionPanelID)
			}
			m.Input.ResetPrompt()
			m.Input.ClearInput()
			m.StatusBar.SetConfirming(false)
			m.StatusBar.SetThinkingForAgent(agentID, true, false, false)
		},
	)
	m.StatusBar.SetActiveForAgent(agentID, agent.IsActive())
	m.syncAgentMessages(agentID, evt.Type == "done" || evt.Type == "error")
	m.syncConversationMessages(thinkingTarget, evt.Type == "done" || evt.Type == "error")

	// Promotion is guarded inside checkThinkingPromotion (no-op once the
	// thinking block already lives in its own pane), so it is safe to call
	// on every thinking event.
	if evt.Type == "thinking" {
		m.checkThinkingPromotion(agent)
	}

	if hasThinking && agent.ThinkingPaneID != "" {
		switch evt.Type {
		case "stream_token", "tool_call", "tool_call_streaming":
			m.handleSubPaneCompletion(agentID)
		}
	}

	if evt.Type == "done" || evt.Type == "error" {
		m.handleSubPaneCompletion(agentID)
		m.StatusBar.ClearAgentState(agentID)
	}

	switch evt.Type {
	case "usage":
		m.handleUsageData(evt.Data, agentID)
	}

	// Sync window tabs after all state mutations to ensure tab state
	// matches agent state. This was previously called in View() which
	// caused flickering by mutating render state every frame.
	m.syncAgentWindowTabs()

	return m.processAgentResults(cmd)
}

func agentEventStringSlice(data any, key string) []string {
	values, _ := data.(map[string]any)
	switch raw := values[key].(type) {
	case []string:
		return raw
	case []any:
		result := make([]string, 0, len(raw))
		for _, value := range raw {
			if text, ok := value.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func (m *App) syncPendingBtwCount() {
	if m.StatusBar == nil || m.AgentMgr == nil {
		return
	}
	count := 0
	for _, node := range m.AgentMgr.AllNodes() {
		if node != nil && node.State != nil && node.State.Messages != nil {
			count += node.State.Messages.PendingBtwCount()
		}
	}
	m.StatusBar.SetBtwCount(count)
}

func (m *App) getThinkingTarget(agentID string, agent *workspace.AgentState) *workspace.Conversation {
	// Once thinking has been promoted to its own pane, keep streaming into
	// that pane instead of spawning a fresh inline block in the chat. The
	// pane check must come first: the main orchestrator also owns a PaneAgent
	// in the manager, so PaneMgr.Has(agentID) alone would never redirect.
	if agent != nil && agent.ThinkingPaneID != "" {
		if sp := m.PaneMgr.Get(agent.ThinkingPaneID); sp != nil {
			return sp.Messages
		}
		// Stale reference (pane closed) — fall through to a real conversation.
		agent.ThinkingPaneID = ""
	}

	if m.PaneMgr.Has(agentID) {
		return m.PaneMgr.Get(agentID).Messages
	}

	sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, agentID)
	return sp.Messages
}

func (m *App) checkThinkingPromotion(agent *workspace.AgentState) {
	if agent == nil || !agent.Thinking || agent.ThinkingPaneID != "" {
		return
	}

	chatH := m.ChatAreaHeight
	threshold := max(chatH/3, 5)

	contentWidth := max(m.width-16, 10)
	pane := m.PaneMgr.Get(agent.ID)
	if pane == nil {
		return
	}
	if !m.subPaneProjections.Has(agent.ID) {
		return
	}
	lineCount := m.subPaneProjections.ActiveThinkingLineCount(agent.ID, contentWidth)

	if lineCount > threshold {
		m.promoteThinkingToPane(agent)
	}
}

func (m *App) promoteThinkingToPane(agent *workspace.AgentState) {
	if agent == nil || agent.Messages == nil {
		return
	}
	paneID := m.nextThinkingPaneID(agent.ID, time.Now())
	// Preserve the original thinking start time so the eventual
	// "thought for Xs" label covers the whole block, not just the
	// post-promotion window.
	start := time.Time{}
	for _, msg := range agent.Messages.Messages() {
		if msg.Role == ttypes.RoleThinking && msg.ThinkingActive {
			start = msg.Time
		}
	}
	content := agent.Messages.RemoveActiveThinking()

	sp := m.PaneMgr.GetOrCreate(paneID, workspace.PaneThinking, "Thinking")
	proj := m.subPaneProjections.Get(paneID, workspace.PaneThinking)
	proj.renderer.SetModelName(m.ModelName)
	proj.renderer.SetUsername(m.Username)

	if content != "" {
		if start.IsZero() {
			sp.Messages.AppendThinking(content)
		} else {
			sp.Messages.AppendMessage(workspace.Message{
				Role:           ttypes.RoleThinking,
				Content:        content,
				Time:           start,
				ThinkingActive: true,
			})
			// The pane's own clock starts at the block start so the header
			// and the collapsed "thought for X" summary cover the whole
			// block, not just the post-promotion window.
			sp.StartTime = start
		}
	}

	agent.ThinkingPaneID = paneID

	agent.Messages.AppendMessage(workspace.Message{
		Role:      ttypes.RoleTool,
		Content:   "Thinking",
		SubPaneID: paneID,
		Time:      time.Now(),
	})
	m.syncAgentMessages(agent.ID, false)
	m.syncPaneMessages(paneID, false)
}

func (m *App) nextThinkingPaneID(agentID string, now time.Time) string {
	base := "thinking-" + agentID + "-" + now.Format("150405")
	paneID := base
	for suffix := 2; m.PaneMgr.Has(paneID) || m.subPaneProjections.Has(paneID); suffix++ {
		paneID = fmt.Sprintf("%s-%d", base, suffix)
	}
	return paneID
}

func (m *App) handleSubPaneCompletion(agentID string) {
	n := m.AgentMgr.Get(agentID)
	if n == nil {
		return
	}
	// Find the parent agent's concrete MessageList for InvalidateSubPane.
	var parentID string
	if n.Parent != nil {
		parentID = n.Parent.ID
	} else if active := m.AgentMgr.ActiveNode(); active != nil {
		parentID = active.ID
	}

	if n.State != nil && n.State.ThinkingPaneID != "" {
		sp := m.PaneMgr.Get(n.State.ThinkingPaneID)
		if sp != nil {
			sp.Collapse()
			if parentID != "" {
				m.subPaneProjections.InvalidateSubPane(parentID, n.State.ThinkingPaneID)
			}
		}
		n.State.ThinkingPaneID = ""
		return
	}

	sp := m.PaneMgr.Get(agentID)
	if sp == nil {
		return
	}
	sp.Collapse()
	if parentID != "" {
		m.subPaneProjections.InvalidateSubPane(parentID, agentID)
	}
}

func (m *App) processAgentResults(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch result := msg.(type) {
		case workspace.AgentDoneResultMsg:
			if result.Reason == "completed" {
				m.StatusBar.SetInfo("✓ done")
			} else if result.Reason == "cancelled" {
				m.StatusBar.SetInfo("Turn stopped")
			} else if result.Reason != "" {
				m.StatusBar.SetError("⚠ " + result.Reason)
			}
		case workspace.AgentErrorResultMsg:
			m.StatusBar.SetError(result.Err)
			m.StatusBar.ClearRetry()
			m.StatusBar.SetStatusLine("")
		case workspace.AgentRetryResultMsg:
			m.StatusBar.SetRetry(result.Msg)
		}
		return nil
	}
}

func (m *App) handleUsageData(data any, agentID string) {
	if pane := m.PaneMgr.Get(agentID); pane != nil {
		pane.TokenCount += usageEventTokens(data)
	}
	usage, ok := m.RecordAgentUsage(agentID, data)
	if !ok || m.AgentMgr.ActiveID() != agentID {
		return
	}
	m.StatusBar.UpdateUsage(usage.Prompt, usage.Completion, usage.Cached, usage.Cost, usage.ContextWindow, usage.ContextUsed, usage.Estimated, usage.CostUpperBound)
}

func usageEventTokens(data any) int {
	read := func(values map[string]any, key string) int {
		switch value := values[key].(type) {
		case int:
			return value
		case int64:
			return int(value)
		case float64:
			return int(value)
		default:
			return 0
		}
	}
	switch values := data.(type) {
	case map[string]any:
		return read(values, "prompt") + read(values, "completion")
	case map[string]int:
		return values["prompt"] + values["completion"]
	default:
		return 0
	}
}

func (m *App) handleSubAgentQuestion(evt workspace.AgentEvent) {
	data, ok := evt.Data.(map[string]string)
	if !ok {
		return
	}
	agentName := data["agent_name"]
	category := data["category"]
	question := data["question"]
	if agentName == "" || question == "" {
		return
	}
	content := fmt.Sprintf("[%s:%s] asks: %s", category, agentName, question)
	if m.AgentMgr.ActiveID() != "" {
		m.appendActiveMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleTool,
			Content: content,
			Time:    time.Now(),
		})
	}
}

func (m *App) handleFileClaim(evt workspace.AgentEvent) {
	data, ok := evt.Data.(map[string]string)
	if !ok {
		return
	}
	_ = data["agent_name"]
	_ = data["category"]
	_ = data["path"]
}

func (m *App) handleSubAgentStarted(evt workspace.AgentEvent) {
	data, ok := evt.Data.(map[string]string)
	if !ok {
		return
	}
	agentID := data["agent_id"]
	agentName := data["agent_name"]
	if agentID == "" || agentName == "" {
		return
	}
	label := agentName
	if n := m.AgentMgr.Get(agentID); n != nil {
		n.Label = label
		if n.State != nil {
			n.State.Status = "running"
		}
	}
	if model := data["model"]; model != "" {
		if n := m.AgentMgr.Get(agentID); n != nil {
			n.Model = model
		}
		if p := m.subPaneProjections.GetExisting(agentID); p != nil {
			p.renderer.SetModelName(model)
		}
	}
	if category := data["category"]; category != "" {
		if n := m.AgentMgr.Get(agentID); n != nil {
			n.Category = category
		}
	}
	_ = data["parent_id"] // parent relationship already set by AddSubAgent
}

func (m *App) handleSubAgentCompleted(evt workspace.AgentEvent) {
	data, ok := evt.Data.(map[string]string)
	if !ok {
		return
	}
	agentID := data["agent_id"]
	status := data["status"]
	if agentID == "" || status == "" {
		return
	}

	if n := m.AgentMgr.Get(agentID); n != nil && n.State != nil {
		n.State.Status = status
	}

	if status == "running" {
		return
	}

	m.completeSubAgent(agentID)
}
