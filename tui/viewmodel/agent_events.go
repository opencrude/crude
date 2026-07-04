package viewmodel

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/provider"
	"crude/tui/workspace"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

func (m *App) submitInput(sessionID, text string, refIDs []string) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	agent := n.State
	agent.MsgList.AppendMessage(ttypes.ChatMessage{
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
	agent.MsgList.AppendMessage(ttypes.ChatMessage{
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
	if evt.Type == "async_command_status" || evt.Type == "retry" {
		switch evt.Type {
		case "async_command_status":
			if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
				m.StatusBar.SetAsyncCommands(cmds)
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
			// Orchestrator: create top-level agent with its own MsgList
			m.launcher = nil
			ml := widgets.NewMessageList(m.Theme)
			ml.SetModelName(m.ModelName)
			ml.SetUsername(m.Username)
			ml.SetSubPaneContentFn(m.renderSubPaneContent)
			agent = workspace.NewAgentState(agentID, ml, m.Logger)
			// Agent node should already exist from AddOrchestrator; update its state
			if existing := m.AgentMgr.Get(agentID); existing != nil {
				existing.State = agent
				agentNode = existing
			} else {
				// Fallback: shouldn't happen in normal flow
				agentNode = m.AgentMgr.AddOrchestrator(agentID, nil, nil, nil, nil, agent)
			}
			m.AgentMgr.SetActive(agentID)
			m.syncAgentWindowTabs()
		} else {
			// Sub-agent: create sub-pane
			sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, agentID, m.Theme)
			subModel := m.ModelName
			if model, ok := evtData["model"]; ok && model != "" {
				subModel = model
			}
			sp.MsgList.SetModelName(subModel)
			sp.MsgList.SetUsername(m.Username)
			agent = workspace.NewAgentState(agentID, sp.MsgList, m.Logger)
			// Get label/model/category from event data
			label := ""
			if l, ok := evtData["agent_name"]; ok {
				label = l
				if len(label) > 16 {
					label = label[:16]
				}
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
			_ = agentNode
			if parent := m.AgentMgr.Get(parentID); parent != nil && parent.State != nil && parent.State.MsgList != nil {
				parent.State.MsgList.AppendMessage(ttypes.ChatMessage{
					Role:      ttypes.RoleTool,
					Content:   agentID,
					SubPaneID: agentID,
					Time:      time.Now(),
				})
			}
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
			if risk != "" {
				m.ActiveSession.Confirm.ResetWithDetails(question, operation, tool, target, risk, reason, suggestion)
			} else {
				m.ActiveSession.Confirm.Reset(question, operation, tool, target)
			}
			m.setMode(ModeConfirm)
		},
		func(questionsJSON string) {
			m.purgeQuestionOverlayTabs()
			n := m.ActiveSession.QuestionForm.Reset(questionsJSON)
			if n == 0 {
				return
			}
			m.Overlay.OpenTab("Question", "", true)
			m.setMode(ModeQuestion)
			m.syncQuestionInputPrompt()
		},
		func() {
			m.OverlayState.SetBlocking(false)
			m.ActiveSession.Confirm.Dismiss()
			m.ActiveSession.QuestionForm.Dismiss()
			m.Input.ResetPrompt()
			m.Input.ClearInput()
			m.StatusBar.SetConfirming(false)
			m.StatusBar.SetThinkingForAgent(agentID, true, false, false)
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

func (m *App) getThinkingTarget(agentID string, agent *workspace.AgentState) *widgets.MessageList {
	if m.PaneMgr.Has(agentID) {
		return m.PaneMgr.Get(agentID).MsgList
	}

	if agent.ThinkingPaneID != "" {
		if sp := m.PaneMgr.Get(agent.ThinkingPaneID); sp != nil {
			return sp.MsgList
		}
		agent.ThinkingPaneID = ""
	}

	sp := m.PaneMgr.GetOrCreate(agentID, workspace.PaneAgent, agentID, m.Theme)
	return sp.MsgList
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
	lineCount := agent.MsgList.ActiveThinkingLineCount(contentWidth)

	if lineCount > threshold {
		m.promoteThinkingToPane(agent)
	}
}

func (m *App) promoteThinkingToPane(agent *workspace.AgentState) {
	if agent == nil || agent.MsgList == nil {
		return
	}
	paneID := "thinking-" + time.Now().Format("150405")
	content := agent.MsgList.RemoveActiveThinking()

	sp := m.PaneMgr.GetOrCreate(paneID, workspace.PaneThinking, "Thinking", m.Theme)
	sp.MsgList.SetModelName(m.ModelName)
	sp.MsgList.SetUsername(m.Username)

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
	n := m.AgentMgr.Get(agentID)
	var parentML *widgets.MessageList
	if n != nil && n.Parent != nil && n.Parent.State != nil {
		parentML = n.Parent.State.MsgList
	}
	if parentML == nil {
		if active := m.AgentMgr.ActiveNode(); active != nil && active.State != nil {
			parentML = active.State.MsgList
		}
	}

	if n.State != nil && n.State.ThinkingPaneID != "" {
		sp := m.PaneMgr.Get(n.State.ThinkingPaneID)
		if sp != nil {
			sp.Collapse()
			if parentML != nil {
				parentML.InvalidateSubPane(n.State.ThinkingPaneID)
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
	s := m.ActiveSession
	if s == nil {
		return
	}
	switch d := data.(type) {
	case map[string]any:
		prompt := toInt(d["prompt"])
		completion := toInt(d["completion"])
		cached := toInt(d["prompt_cached"])
		cost, _ := d["cost"].(float64)
		ctxWindow := toInt(d["context_window"])
		ctxUsed := toInt(d["context_used"])
		estimated, _ := d["estimated"].(bool)
		s.SessionCost += cost
		s.TokenPrompt = int(prompt)
		s.TokenCompletion = int(completion)
		s.TokenCached = int(cached)
		s.TokenEstimated = estimated
		if int(ctxWindow) > 0 {
			s.ContextWindow = int(ctxWindow)
			if int(ctxUsed) > 0 {
				s.ContextUsed = int(ctxUsed)
			} else {
				s.ContextUsed = int(prompt)
			}
		}
		m.StatusBar.UpdateUsage(s.TokenPrompt, s.TokenCompletion, s.TokenCached, s.SessionCost, s.ContextWindow, s.ContextUsed, s.TokenEstimated)
	case map[string]int:
		s.TokenCached = d["prompt_cached"]
		s.TokenCompletion = d["completion"]
		s.TokenPrompt = d["prompt"]
		if cw := d["context_window"]; cw > 0 {
			s.ContextWindow = cw
			s.ContextUsed = d["prompt"]
		}
		m.StatusBar.UpdateUsage(s.TokenPrompt, s.TokenCompletion, s.TokenCached, s.SessionCost, s.ContextWindow, s.ContextUsed, false)
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
		if sp := m.PaneMgr.Get(agentID); sp != nil {
			sp.MsgList.SetModelName(model)
		}
	}
	if category := data["category"]; category != "" {
		if n := m.AgentMgr.Get(agentID); n != nil {
			n.Category = category
		}
	}
	_ = data["parent_id"] // parent relationship already set by AddSubAgent
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

	if n := m.AgentMgr.Get(agentID); n != nil && n.State != nil {
		n.State.Status = status
	}

	if status == "running" {
		return
	}

	m.completeSubAgent(agentID)
}
