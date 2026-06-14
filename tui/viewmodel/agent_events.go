package viewmodel

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/provider"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

func (m *App) submitInput(sessionID, text string, refIDs []string) tea.Cmd {
	agent, ok := m.agents[sessionID]
	if !ok {
		return nil
	}
	agent.MsgList.AppendMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleUser,
		Content: text,
		Time:    time.Now(),
	})
	if len(m.history) == 0 || m.history[len(m.history)-1] != text {
		m.history = append(m.history, text)
	}
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
	if OnHistorySave != nil {
		OnHistorySave(m.history)
	}
	agent.SubmitInput(text)
	m.statusbar.SetThinking(true, false, false)
	ch := m.sessionInputChans[sessionID]
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
	agent, ok := m.agents[sessionID]
	if !ok {
		return nil
	}
	agent.MsgList.AppendMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleUser,
		Content: text,
		Time:    time.Now(),
	})
	if len(m.history) == 0 || m.history[len(m.history)-1] != text {
		m.history = append(m.history, text)
	}
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
	if OnHistorySave != nil {
		OnHistorySave(m.history)
	}
	m.statusbar.SetBtwCount(m.statusbar.BtwCount() + 1)
	ch := m.sessionBtwChans[sessionID]
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
	if evt.Type == "async_command_status" || evt.Type == "retry" {
		switch evt.Type {
		case "async_command_status":
			if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
				m.statusbar.SetAsyncCommands(cmds)
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
				m.statusbar.SetRetry(fmt.Sprintf("⚠ retrying in %.0fs... (attempt %d)", backoff, attempt))
			case string:
				m.statusbar.SetRetry(d)
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

	agent, ok := m.agents[agentID]
	if !ok {
		// Check if this is an orchestrator (no parent) or a sub-agent
		evtData, _ := evt.Data.(map[string]string)
		parentID := evtData["parent_id"]

		if parentID == "" {
			// Orchestrator: create top-level agent with its own MsgList
			m.launcher = nil
			ml := widgets.NewMessageList(m.theme)
			ml.SetModelName(m.modelName)
			ml.SetUsername(m.username)
			ml.SetSubPaneContentFn(m.renderSubPaneContent)
			agent = NewAgentState(agentID, ml, m.logger)
			m.agents[agentID] = agent
			m.activeAgentView = agentID
			m.syncAgentWindowTabs()
		} else {
			// Sub-agent: create sub-pane
			sp := m.paneMgr.GetOrCreate(agentID, PaneAgent, agentID, m.theme)
			subModel := m.modelName
			if am, ok := m.agentModels[agentID]; ok && am != "" {
				subModel = am
			}
			sp.MsgList.SetModelName(subModel)
			sp.MsgList.SetUsername(m.username)
			agent = NewAgentState(agentID, sp.MsgList, m.logger)
			if parent, ok := m.agents[parentID]; ok && parent.MsgList != nil {
				parent.MsgList.AppendMessage(ttypes.ChatMessage{
					Role:      ttypes.RoleTool,
					Content:   agentID,
					SubPaneID: agentID,
					Time:      time.Now(),
				})
			}
			m.agents[agentID] = agent
		}
	} else if agent.Completed {
		if evt.Type == "done" || evt.Type == "error" {
			m.handleSubPaneCompletion(agentID)
		}
		m.syncAgentWindowTabs()
		return nil
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
		m.statusbar.RecordActivityForAgent(agentID)
	case "phase":
		m.statusbar.RecordActivityForAgent(agentID)
		if phase, ok := evt.Data.(string); ok {
			m.statusbar.SetPhaseForAgent(agentID, phase)
		}
	}

	thinkingTarget := m.getThinkingTarget(agentID, agent)

	hasThinking := agent != nil && agent.ThinkingPaneID != ""

	cmd := agent.HandleEvent(evt, thinkingTarget,
		func(statusLine string) {
			m.statusbar.SetStatusLineForAgent(agentID, statusLine)
		},
		func(thinking, streaming, blocking bool) {
			m.statusbar.SetThinkingForAgent(agentID, thinking, streaming, blocking)
		},
		func() {
			m.blocking = true
			m.statusbar.SetStatusLine("")
		},
		func(question, operation, tool, target, risk, reason, suggestion string) {
			if risk != "" {
				m.confirm.ResetWithDetails(question, operation, tool, target, risk, reason, suggestion)
			} else {
				m.confirm.Reset(question, operation, tool, target)
			}
			m.setMode(ModeConfirm)
		},
		func(questionsJSON string) {
			m.purgeQuestionOverlayTabs()
			n := m.questionForm.Reset(questionsJSON)
			if n == 0 {
				return
			}
			m.overlay.OpenTab("Question", "", true)
			m.setMode(ModeQuestion)
			m.syncQuestionInputPrompt()
		},
		func() {
			m.blocking = false
			m.confirm.Dismiss()
			m.questionForm.Dismiss()
			m.input.ResetPrompt()
			m.input.ClearInput()
			m.statusbar.SetConfirming(false)
			m.statusbar.SetThinking(true, false, false)
			m.closeOverlayTab("Confirm")
			m.closeOverlayTab("Question")
			m.closeAllOverlayTabs()
		},
	)

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
		m.statusbar.ClearAgentState(agentID)
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

func (m *App) getThinkingTarget(agentID string, agent *AgentState) *widgets.MessageList {
	if m.paneMgr.Has(agentID) {
		return m.paneMgr.Get(agentID).MsgList
	}

	if agent.ThinkingPaneID != "" {
		if sp := m.paneMgr.Get(agent.ThinkingPaneID); sp != nil {
			return sp.MsgList
		}
		agent.ThinkingPaneID = ""
	}

	sp := m.paneMgr.GetOrCreate(agentID, PaneAgent, agentID, m.theme)
	return sp.MsgList
}

func (m *App) checkThinkingPromotion(agent *AgentState) {
	if agent == nil || !agent.Thinking || agent.ThinkingPaneID != "" {
		return
	}

	chatH := m.chatAreaHeight
	threshold := chatH / 3
	if threshold < 5 {
		threshold = 5
	}

	contentWidth := m.width - 16
	if contentWidth < 10 {
		contentWidth = 10
	}
	lineCount := agent.MsgList.ActiveThinkingLineCount(contentWidth)

	if lineCount > threshold {
		m.promoteThinkingToPane(agent)
	}
}

func (m *App) promoteThinkingToPane(agent *AgentState) {
	if agent == nil || agent.MsgList == nil {
		return
	}
	paneID := "thinking-" + time.Now().Format("150405")
	content := agent.MsgList.RemoveActiveThinking()

	sp := m.paneMgr.GetOrCreate(paneID, PaneThinking, "Thinking", m.theme)
	sp.MsgList.SetModelName(m.modelName)
	sp.MsgList.SetUsername(m.username)

	if content != "" {
		sp.MsgList.AppendThinking(content)
		sp.MsgList.Messages()[len(sp.MsgList.Messages())-1].ThinkingActive = true
	}

	agent.ThinkingPaneID = paneID

	agent.MsgList.AppendMessage(ttypes.ChatMessage{
		Role:      ttypes.RoleTool,
		Content:   "Thinking",
		SubPaneID: paneID,
		Time:      time.Now(),
	})
}

func (m *App) handleSubPaneCompletion(agentID string) {
	parentID := m.subAgentParents[agentID]
	var parentML *widgets.MessageList
	if parentID != "" {
		if parent, ok := m.agents[parentID]; ok && parent.MsgList != nil {
			parentML = parent.MsgList
		}
	}
	if parentML == nil {
		if a, ok := m.agents[m.activeAgentView]; ok && a.MsgList != nil {
			parentML = a.MsgList
		}
	}

	agent, ok := m.agents[agentID]
	if ok && agent.ThinkingPaneID != "" {
		sp := m.paneMgr.Get(agent.ThinkingPaneID)
		if sp != nil {
			sp.Collapse()
			if parentML != nil {
				parentML.InvalidateSubPane(agent.ThinkingPaneID)
			}
		}
		agent.ThinkingPaneID = ""
		return
	}

	sp := m.paneMgr.Get(agentID)
	if sp == nil {
		return
	}
	sp.Collapse()
	if parentML != nil {
		parentML.InvalidateSubPane(agentID)
	}
}

func (m *App) processAgentResults(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch result := msg.(type) {
		case agentDoneResultMsg:
			if result.Reason == "completed" {
				m.statusbar.SetInfo("✓ done")
			} else if result.Reason != "" {
				m.statusbar.SetError("⚠ " + result.Reason)
			}
		case agentErrorResultMsg:
			m.statusbar.SetError(result.Err)
			m.statusbar.ClearRetry()
			m.statusbar.SetStatusLine("")
		case agentRetryResultMsg:
			m.statusbar.SetRetry(result.Msg)
		}
		return nil
	}
}

func (m *App) handleUsageData(data any, agentID string) {
	switch d := data.(type) {
	case map[string]any:
		prompt := toInt(d["prompt"])
		completion := toInt(d["completion"])
		cached := toInt(d["prompt_cached"])
		cost, _ := d["cost"].(float64)
		ctxWindow := toInt(d["context_window"])
		ctxUsed := toInt(d["context_used"])
		estimated, _ := d["estimated"].(bool)
		m.sessionCost += cost
		m.tokenPrompt = int(prompt)
		m.tokenCompletion = int(completion)
		m.tokenCached = int(cached)
		m.tokenEstimated = estimated
		if int(ctxWindow) > 0 {
			m.contextWindow = int(ctxWindow)
			if int(ctxUsed) > 0 {
				m.contextUsed = int(ctxUsed)
			} else {
				m.contextUsed = int(prompt)
			}
		}
		m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed, m.tokenEstimated)
	case map[string]int:
		m.sessionCost += 0
		m.tokenCached = d["prompt_cached"]
		m.tokenCompletion = d["completion"]
		m.tokenPrompt = d["prompt"]
		if cw := d["context_window"]; cw > 0 {
			m.contextWindow = cw
			m.contextUsed = d["prompt"]
		}
		m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed, false)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}

func (m *App) handleSubAgentQuestion(evt ttypes.AgentEventMsg) {
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
	if ml := m.activeMsgList(); ml != nil {
		ml.AppendMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleTool,
			Content: content,
			Time:    time.Now(),
		})
	}
}

func (m *App) handleFileClaim(evt ttypes.AgentEventMsg) {
	data, ok := evt.Data.(map[string]string)
	if !ok {
		return
	}
	_ = data["agent_name"]
	_ = data["category"]
	_ = data["path"]
}

func (m *App) handleSubAgentStarted(evt ttypes.AgentEventMsg) {
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
	if len(label) > 16 {
		label = label[:16]
	}
	m.agentLabels[agentID] = label
	if agent, ok := m.agents[agentID]; ok {
		agent.Status = "running"
	}
	if model := data["model"]; model != "" {
		m.agentModels[agentID] = model
		if sp := m.paneMgr.Get(agentID); sp != nil {
			sp.MsgList.SetModelName(model)
		}
	}
	if category := data["category"]; category != "" {
		m.agentCategories[agentID] = category
	}
	if parentID := data["parent_id"]; parentID != "" {
		m.subAgentParents[agentID] = parentID
	}
}

func (m *App) handleSubAgentCompleted(evt ttypes.AgentEventMsg) {
	data, ok := evt.Data.(map[string]string)
	if !ok {
		return
	}
	agentID := data["agent_id"]
	status := data["status"]
	if agentID == "" || status == "" {
		return
	}

	if agent, aok := m.agents[agentID]; aok {
		agent.Status = status
	}

	if status == "running" {
		return
	}

	m.completeSubAgent(agentID)
}
