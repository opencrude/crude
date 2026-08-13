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

func (m *App) submitInput(sessionID, text string, refIDs []string) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	agent := n.State
	agent.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleUser,
		Content: text,
		Time:    time.Now(),
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
	ch := n.InputChan
	return func() tea.Msg {
		select {
		case ch <- provider.InputWithRefs(text, refIDs):
			return nil
		default:
			return ttypes.ErrorMsg("input channel blocked - please try again")
		}
	}
}

func (m *App) submitBtw(sessionID, text string, refIDs []string) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	agent := n.State
	agent.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleUser,
		Content: text,
		Time:    time.Now(),
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
	if m.StatusBar != nil {
		m.StatusBar.SetBtwCount(m.StatusBar.BtwCount() + 1)
	}
	m.syncAgentMessages(sessionID, true)
	ch := n.BtwChan
	return func() tea.Msg {
		select {
		case ch <- provider.InputWithRefs(text, refIDs):
			return nil
		default:
		}
		return nil
	}
}

func (m *App) handleAgentEvent(evt ttypes.AgentEventMsg) tea.Cmd {
	return m.handleAgentDomainEvent(workspace.AgentEvent{
		Type:    evt.Type,
		Data:    evt.Data,
		AgentID: evt.AgentID,
	})
}

func (m *App) handleAgentDomainEvent(evt workspace.AgentEvent) tea.Cmd {
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
			sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, agentID)
			subModel := m.ModelName
			if model, ok := evtData["model"]; ok && model != "" {
				subModel = model
			}
			proj := m.subPaneProjections.Get(agentID, workspace.PaneAgent)
			proj.renderer.SetModelName(subModel)
			proj.renderer.SetUsername(m.Username)
			agent = workspace.NewAgentState(agentID, sp.Messages, m.Logger)
			// Get label/model/category from event data
			label := ""
			if l, ok := evtData["agent_name"]; ok {
				label = workspace.TruncateLine(l, 16)
			}
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
				parent.State.Messages.AppendMessage(workspace.Message{
					Role:      ttypes.RoleTool,
					Content:   agentID,
					SubPaneID: agentID,
					Time:      time.Now(),
				})
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
	if evt.Type == "blocking" && m.AgentMgr.ActiveID() != agentID {
		m.switchToAgentView(agentID)
	}

	switch evt.Type {
	case "sub_agent_started":
		m.handleSubAgentStarted(evt)
	case "sub_agent_completed":
		m.handleSubAgentCompleted(evt)
	case "sub_agent_question":
		m.handleSubAgentQuestion(evt)
	case "file_claim":
		m.handleFileClaim(evt)
	case "stream_token", "thinking", "tool_call", "tool_call_streaming", "tool_result", "tool_progress":
		m.StatusBar.RecordActivityForAgent(agentID)
	case "phase":
		m.StatusBar.RecordActivityForAgent(agentID)
		if phase, ok := evt.Data.(string); ok {
			m.StatusBar.SetPhaseForAgent(agentID, phase)
		}
	}

	thinkingTarget := m.getThinkingTarget(agentID, agent)

	hasThinking := agent != nil && agent.ThinkingPaneID != ""

	cmd := agent.HandleEvent(evt, thinkingTarget,
		func(statusLine string) {
			m.StatusBar.SetStatusLineForAgent(agentID, statusLine)
		},
		func(thinking, streaming, blocking bool) {
			m.StatusBar.SetThinkingForAgent(agentID, thinking, streaming, blocking)
		},
		func() {
			m.OverlayState.SetBlocking(true)
			m.StatusBar.SetStatusLine("")
		},
		func(question, operation, tool, target, risk, reason, suggestion string) {
			projection := m.activeSessionProjection()
			if projection == nil || projection.confirmation == nil {
				return
			}
			projection.confirmation.Reset(confirmation.Details{Question: question, Operation: operation, Tool: tool, Target: target, Risk: risk, Reason: reason, Suggestion: suggestion})
			projection.confirmationAgentID = agentID
			m.openSessionConfirmationPanel(projection)
		},
		func(questionsJSON string) {
			projection := m.activeSessionProjection()
			if projection == nil || projection.question == nil {
				return
			}
			n := projection.question.Reset(questionsJSON)
			if n == 0 {
				m.StatusBar.SetError("Agent sent an invalid question request")
				m.ResolveQuestion(agentID, nil, nil, true)
				return
			}
			projection.questionAgentID = agentID
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
	m.syncAgentMessages(agentID, evt.Type == "done" || evt.Type == "error")
	m.syncConversationMessages(thinkingTarget, evt.Type == "done" || evt.Type == "error")

	if evt.Type == "thinking" && hasThinking {
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

func (m *App) getThinkingTarget(agentID string, agent *workspace.AgentState) *workspace.Conversation {
	if m.PaneMgr.Has(agentID) {
		return m.PaneMgr.Get(agentID).Messages
	}

	if agent.ThinkingPaneID != "" {
		if sp := m.PaneMgr.Get(agent.ThinkingPaneID); sp != nil {
			return sp.Messages
		}
		agent.ThinkingPaneID = ""
	}

	sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, agentID)
	return sp.Messages
}

func (m *App) checkThinkingPromotion(agent *workspace.AgentState) {
	if agent == nil || !agent.Thinking || agent.ThinkingPaneID != "" {
		return
	}

	chatH := m.ChatAreaHeight
	threshold := chatH / 3
	if threshold < 5 {
		threshold = 5
	}

	contentWidth := m.width - 16
	if contentWidth < 10 {
		contentWidth = 10
	}
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
	paneID := "thinking-" + time.Now().Format("150405")
	content := agent.Messages.RemoveActiveThinking()

	sp := m.PaneMgr.GetOrCreate(paneID, workspace.PaneThinking, "Thinking")
	proj := m.subPaneProjections.Get(paneID, workspace.PaneThinking)
	proj.renderer.SetModelName(m.ModelName)
	proj.renderer.SetUsername(m.Username)

	if content != "" {
		sp.Messages.AppendThinking(content)
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
	usage, ok := m.RecordAgentUsage(agentID, data)
	if !ok || m.AgentMgr.ActiveID() != agentID {
		return
	}
	m.StatusBar.UpdateUsage(usage.Prompt, usage.Completion, usage.Cached, usage.Cost, usage.ContextWindow, usage.ContextUsed, usage.Estimated)
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
	if label != "" {
		label = workspace.TruncateLine(label, 16)
	}
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
