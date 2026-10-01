package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/provider"
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
		if m.Feedback != nil {
			m.Feedback.NotifyError("Message was not sent: this agent already has an active turn")
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
	agent.SubmitInput(text)
	m.syncAgentMessages(sessionID, true)
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
		m.restoreFailedBtw(sessionID, []workspace.Message{{Content: text}})
		if m.Feedback != nil {
			m.Feedback.NotifyError("BTW was not queued: this turn is no longer accepting notes")
		}
		return nil
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
	domain := workspace.AgentEvent{
		Type:    evt.Type,
		Data:    evt.Data,
		AgentID: evt.AgentID,
	}
	if m.StaleAgentUpload(domain) {
		return nil
	}
	cmd := m.handleAgentDomainEvent(domain)
	m.syncAgentStatusEvent(evt)
	return cmd
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
			ids := agentEventStringSlice(evt.Data, "internal_ids")
			m.restoreFailedBtw(evt.AgentID, node.State.Messages.RemovePendingBtwMessages(ids))
			m.syncAgentMessages(evt.AgentID, true)
		}
		m.syncPendingBtwCount()
		return nil
	}
	if evt.Type == "memory_saved" {
		// Background extraction saved durable memories. Surface it visibly
		// but without blocking (status line, not a modal or chat message).
		if data, ok := evt.Data.(map[string]any); ok && m.Feedback != nil {
			count, _ := data["count"].(int)
			if count > 0 {
				notice := fmt.Sprintf("Memory: saved %d from this session", count)
				if titles, ok := data["titles"].([]string); ok && len(titles) > 0 {
					notice = fmt.Sprintf("Memory: saved %q", titles[0])
					if count > 1 {
						notice += fmt.Sprintf(" +%d more", count-1)
					}
				}
				m.Feedback.NotifyInfo(notice)
			}
		}
		if m.memoryBrowser != nil {
			m.memoryBrowser.Refresh()
			m.markMemoryBrowserDirty()
		}
		return nil
	}
	if evt.Type == "async_command_status" {
		switch evt.Type {
		case "async_command_status":
			if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
				m.updateSessionBackgroundJobs(evt.AgentID, cmds, time.Now())
			}
			m.syncAgentWindowTabs()
			return nil
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
				proj.renderer.SetLegacyModelFallback(m.ModelName)
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
			// A late state initialization is structural; the selected window
			// tab remains the only authority for the active view.
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
			sp.Status = "running"
			subModel := m.ModelName
			if model, ok := evtData["model"]; ok && model != "" {
				subModel = model
			}
			proj := m.subPaneProjections.Get(agentID, workspace.PaneAgent)
			proj.renderer.SetLegacyModelFallback(subModel)
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
				childLabel := label
				if task := evtData["task"]; task != "" {
					childLabel += " — " + task
				}
				// Without the exact call ID, keep child review in its tab/Agents
				// panel instead of creating a second or incorrectly linked row.
				if parent.State.Messages.AttachSubPaneToTool(evtData["parent_tool_id"], agentID, childLabel) {
					m.syncAgentMessages(parentID, false)
				}
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
	// Permission and question prompts open inline for the selected owner or
	// as a modal for a background owner. Neither requires switching tabs;
	// safety alerts likewise update their owner without changing navigation.

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
		},
		func() {
			m.OverlayState.SetBlocking(true)
			m.Feedback.NotifyInfo("")
		},
		func(question, operation, tool, target, risk, reason, suggestion string) {
			m.enqueueAgentConfirmation(agentPromptRequest{
				kind: agentPromptConfirmation, agentID: agentID, blockID: agent.PendingBlockID,
				question: question, operation: operation, tool: tool, target: target,
				risk: risk, reason: reason, suggestion: suggestion,
			})
		},
		func(questionsJSON string) {
			m.enqueueAgentQuestions(agentPromptRequest{kind: agentPromptQuestions, agentID: agentID, blockID: agent.PendingBlockID, questionsJSON: questionsJSON})
		},
		func() {
			m.OverlayState.SetBlocking(false)
			m.dismissAgentPromptFor(agentID)
			m.Input.ResetPrompt()
			m.Input.ClearInput()
		},
	)
	m.syncAgentMessages(agentID, evt.Type == "done" || evt.Type == "error")
	m.syncConversationMessages(thinkingTarget, evt.Type == "done" || evt.Type == "error")

	// Provider usage may settle an unlabelled stream as raw after the last
	// thinking delta. Give that same active block a pane before tool execution
	// moves on; promotion itself is a no-op once it already has a pane.
	if evt.Type == "thinking" || evt.Type == "reasoning_classified" {
		m.checkThinkingPromotion(agent)
	}

	if hasThinking && agent.ThinkingPaneID != "" {
		if pane := m.PaneMgr.Get(agent.ThinkingPaneID); pane != nil {
			messages := pane.Messages.Messages()
			if len(messages) == 1 && !messages[0].ThinkingActive {
				m.handleSubPaneCompletion(agentID)
			}
		}
	}

	if evt.Type == "done" || evt.Type == "error" {
		m.handleSubPaneCompletion(agentID)
		if m.Status != nil {
			m.Status.RemoveAgent(agentID)
		}
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

// restoreFailedBtw puts transport-rejected/cancelled notes back in the owning
// session's composer. Keep anything the user has typed since the submission.
func (m *App) restoreFailedBtw(agentID string, messages []workspace.Message) {
	if len(messages) == 0 || m.Workspace == nil {
		return
	}
	owner := m.SessionsByAgent[agentID]
	if owner == nil {
		return
	}
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Content != "" {
			texts = append(texts, message.Content)
		}
	}
	if len(texts) == 0 {
		return
	}
	if owner != m.ActiveSession || (owner.DB != nil && m.composerHydratedFor != owner) {
		if m.failedBtwDrafts == nil {
			m.failedBtwDrafts = make(map[*workspace.Session][]string)
		}
		m.failedBtwDrafts[owner] = append(m.failedBtwDrafts[owner], texts...)
		return
	}
	m.Input.SetInput(joinFailedBtwDrafts(texts, m.Input.Value()))
	m.persistComposerDraftIfChanged()
}

func joinFailedBtwDrafts(failed []string, current string) string {
	if len(failed) == 0 {
		return current
	}
	text := strings.Join(failed, "\n\n")
	if current != "" {
		text += "\n\n" + current
	}
	return text
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
	if m.Feedback == nil || m.AgentMgr == nil {
		return
	}
	count := 0
	for _, node := range m.AgentMgr.AllNodes() {
		if node != nil && node.State != nil && node.State.Messages != nil {
			count += node.State.Messages.PendingBtwCount()
		}
	}
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
	thought, ok := agent.Messages.PromoteThinking(paneID)
	if !ok {
		return
	}

	sp := m.PaneMgr.GetOrCreate(paneID, workspace.PaneThinking, "Thinking")
	proj := m.subPaneProjections.Get(paneID, workspace.PaneThinking)
	proj.renderer.SetLegacyModelFallback(m.ModelName)
	proj.renderer.SetUsername(m.Username)

	sp.Messages.AppendMessage(thought)
	sp.StartTime = thought.Time

	agent.ThinkingPaneID = paneID

	m.syncAgentMessages(agent.ID, false)
	m.syncPaneMessages(paneID, false)
}

func (m *App) nextThinkingPaneID(agentID string, now time.Time) string {
	base := "thinking-" + agentID + "-" + now.Format("150405.000000000")
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
	if n.State != nil && n.State.ThinkingPaneID != "" {
		paneID := n.State.ThinkingPaneID
		sp := m.PaneMgr.Get(paneID)
		if sp != nil {
			messages := sp.Messages.Messages()
			if len(messages) != 1 || !n.State.Messages.RestoreThinking(paneID, messages[0]) {
				return
			}
			m.PaneMgr.Remove(paneID)
			m.subPaneProjections.Remove(paneID)
		}
		n.State.ThinkingPaneID = ""
		m.syncAgentMessages(agentID, false)
		return
	}

	sp := m.PaneMgr.Get(agentID)
	if sp == nil {
		return
	}
	sp.Collapse()
	if n.Parent != nil {
		m.subPaneProjections.InvalidateSubPane(n.Parent.ID, agentID)
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
				m.Feedback.NotifyInfo("✓ done")
			} else if result.Reason != "" && result.Reason != "cancelled" {
				m.Feedback.NotifyError("⚠ " + result.Reason)
			}
		case workspace.AgentErrorResultMsg:
			// The canonical error row and syncAgentStatusEvent already publish
			// the diagnostic and its owner-scoped Notice. Do not echo it through
			// foreground feedback from this asynchronous completion command.
		}
		return nil
	}
}

func (m *App) handleUsageData(data any, agentID string) {
	if pane := m.PaneMgr.Get(agentID); pane != nil {
		pane.TokenCount += usageEventTokens(data)
	}
	m.RecordAgentUsage(agentID, data)
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
			p.renderer.SetLegacyModelFallback(model)
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
	if pane := m.PaneMgr.Get(agentID); pane != nil {
		pane.Status = status
	}
	m.completeSubAgent(agentID)
}
