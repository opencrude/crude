package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/fileio"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

func (m *App) submitInput(text string) tea.Cmd {
	ma := m.mainAgent()
	ma.MsgList.AppendMessage(ttypes.ChatMessage{
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
	ma.SubmitInput(text)
	m.statusbar.SetThinking(true, false, false)
	ch := m.inputCh
	return func() tea.Msg {
		select {
		case ch <- text:
			return nil
		default:
			return ttypes.ErrorMsg("input channel blocked - please try again")
		}
	}
}

func (m *App) handleAgentEvent(evt ttypes.AgentEventMsg) tea.Cmd {
	agentID := evt.AgentID
	if agentID == "" {
		agentID = MainAgentID
	}

	agent, ok := m.agents[agentID]
	if !ok {
		if agentID == MainAgentID {
			agent = NewAgentState(agentID, m.mainMsgList)
		} else {
			sp := m.paneMgr.GetOrCreate(agentID, PaneAgent, agentID, m.theme)
			sp.MsgList.SetModelName(m.modelName)
			sp.MsgList.SetUsername(m.username)
			agent = NewAgentState(agentID, sp.MsgList)
			m.mainMsgList.AppendMessage(ttypes.ChatMessage{
				Role:      ttypes.RoleTool,
				Content:   agentID,
				SubPaneID: agentID,
				Time:      time.Now(),
			})
		}
		m.agents[agentID] = agent
	}

	thinkingTarget := m.getThinkingTarget(agentID, agent)

	cmd := agent.HandleEvent(evt, thinkingTarget,
		func(statusLine string) {
			m.statusbar.SetStatusLine(statusLine)
		},
		func(thinking, streaming, blocking bool) {
			m.statusbar.SetThinking(thinking, streaming, blocking)
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
		func() {
			m.blocking = false
			m.confirm.Dismiss()
			m.statusbar.SetConfirming(false)
			m.statusbar.SetThinking(false, false, false)
			if tab := m.win.TabByTitle("Confirm"); tab != nil {
				m.win.CloseTabByID(tab.ID)
			}
			if !m.win.IsOpen() || !m.win.HasClosableTabs() {
				m.setMode(ModeChat)
			} else {
				m.syncActiveTabMode()
			}
		},
	)

	if evt.Type == "thinking" && agentID == MainAgentID {
		m.checkThinkingPromotion()
	}

	if agentID == MainAgentID && m.mainAgent().ThinkingPaneID != "" {
		switch evt.Type {
		case "stream_token", "tool_call", "tool_call_streaming":
			m.handleSubPaneCompletion(agentID)
		}
	}

	if evt.Type == "done" || evt.Type == "error" {
		m.handleSubPaneCompletion(agentID)
	}

	switch evt.Type {
	case "usage":
		m.handleUsageData(evt.Data)
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
	case "async_command_status":
		if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
			m.statusbar.SetAsyncCommands(cmds)
		}
	}

	return m.processAgentResults(cmd)
}

func (m *App) getThinkingTarget(agentID string, agent *AgentState) *widgets.MessageList {
	if agentID != MainAgentID {
		if m.paneMgr.Has(agentID) {
			return m.paneMgr.Get(agentID).MsgList
		}
		sp := m.paneMgr.GetOrCreate(agentID, PaneAgent, agentID, m.theme)
		return sp.MsgList
	}

	if agent.ThinkingPaneID != "" {
		if sp := m.paneMgr.Get(agent.ThinkingPaneID); sp != nil {
			return sp.MsgList
		}
		agent.ThinkingPaneID = ""
	}
	return nil
}

func (m *App) checkThinkingPromotion() {
	ma := m.mainAgent()
	if !ma.Thinking || ma.ThinkingPaneID != "" {
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
	lineCount := m.mainMsgList.ActiveThinkingLineCount(contentWidth)

	if lineCount > threshold {
		m.promoteThinkingToPane(ma)
	}
}

func (m *App) promoteThinkingToPane(agent *AgentState) {
	paneID := "thinking-" + time.Now().Format("150405")
	content := m.mainMsgList.RemoveActiveThinking()

	sp := m.paneMgr.GetOrCreate(paneID, PaneThinking, "Thinking", m.theme)
	sp.MsgList.SetModelName(m.modelName)
	sp.MsgList.SetUsername(m.username)

	if content != "" {
		sp.MsgList.AppendThinking(content)
		sp.MsgList.Messages()[len(sp.MsgList.Messages())-1].ThinkingActive = true
	}

	agent.ThinkingPaneID = paneID

	m.mainMsgList.AppendMessage(ttypes.ChatMessage{
		Role:      ttypes.RoleTool,
		Content:   "Thinking",
		SubPaneID: paneID,
		Time:      time.Now(),
	})
}

func (m *App) handleSubPaneCompletion(agentID string) {
	if agentID == MainAgentID {
		ma := m.mainAgent()
		if ma.ThinkingPaneID != "" {
			sp := m.paneMgr.Get(ma.ThinkingPaneID)
			if sp != nil {
				sp.Collapse()
				m.mainMsgList.InvalidateSubPane(ma.ThinkingPaneID)
			}
			ma.ThinkingPaneID = ""
		}
		return
	}

	sp := m.paneMgr.Get(agentID)
	if sp == nil {
		return
	}
	sp.Collapse()
	m.mainMsgList.InvalidateSubPane(agentID)
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

func (m *App) handleUsageData(data any) {
	switch d := data.(type) {
	case map[string]any:
		prompt := toInt(d["prompt"])
		completion := toInt(d["completion"])
		cached := toInt(d["prompt_cached"])
		cost, _ := d["cost"].(float64)
		ctxWindow := toInt(d["context_window"])
		ctxUsed := toInt(d["context_used"])
		estimated, _ := d["estimated"].(bool)
		m.tokenPrompt = int(prompt)
		m.tokenCompletion = int(completion)
		m.tokenCached = int(cached)
		m.sessionCost += cost
		m.tokenEstimated = estimated
		if int(ctxWindow) > 0 {
			m.contextWindow = int(ctxWindow)
			if int(ctxUsed) > 0 {
				m.contextUsed = int(ctxUsed)
			} else {
				// context_used not always sent; use prompt tokens as proxy
				m.contextUsed = int(prompt)
			}
		}
		m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed, m.tokenEstimated)
	case map[string]int:
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
