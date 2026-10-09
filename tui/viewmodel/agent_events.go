package viewmodel

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent"
	"crude/fileio"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/wm"
	coretypes "crude/types"
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
	return m.submitPreparedInput(sessionID, provider.InputWithRefs(text, refIDs), m.Input.DraftRevision())
}

type inputAdmissionMsg struct {
	workspace       *workspace.Workspace
	workspaceGen    uint64
	session         *workspace.Session
	intent          *workspace.InputAdmissionIntent
	agentID         string
	draftRevision   uint64
	draftEpoch      uint64
	input           provider.UserInput
	referenceLabels []string
	references      []sessiondb.Reference
	result          workspace.InputAdmissionResult
	composerWindow  *wm.FloatingWindow
	composerEditor  *ui.TextArea
	composerState   ui.TextAreaState
}

func (m *App) submitPreparedInput(sessionID string, input provider.UserInput, draftRevision uint64) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	input.ActivePaths = m.memoryContextPaths()
	owner := m.Workspace
	if owner == nil {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Message was not sent; your draft is kept")
		}
		return nil
	}
	intent, err := owner.PrepareAgentInputAdmission(sessionID, input, draftRevision)
	if err != nil {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Message was not sent; your draft is kept")
		}
		return nil
	}
	frontend := owner.SessionsByAgent[sessionID]
	labels := m.referenceLabels(input.ReferenceIDs)
	var references []sessiondb.Reference
	if frontend != nil && frontend.RefStore != nil {
		for _, id := range input.ReferenceIDs {
			if ref, ok := frontend.RefStore.GetReference(id); ok {
				references = append(references, ref)
			}
		}
	}
	if m.pendingInputAdmissions == nil {
		m.pendingInputAdmissions = make(map[*workspace.InputAdmissionIntent]struct{})
	}
	m.pendingInputAdmissions[intent] = struct{}{}
	workspaceGen := m.workspaceSwitchGen.Load()
	draftEpoch := m.Input.DraftEpoch()
	return func() tea.Msg {
		result := owner.CommitAgentInputAdmission(context.Background(), intent)
		return inputAdmissionMsg{workspace: owner, workspaceGen: workspaceGen, session: frontend, intent: intent,
			agentID: sessionID, draftRevision: intent.DraftRevision, draftEpoch: draftEpoch,
			input: input, referenceLabels: labels, references: references, result: result}
	}
}

func (m *App) submitComposerInput(sessionID, text string) tea.Cmd {
	screen := m.sessionComposer()
	window := m.overlayHost.Window(ui.OverlayComposer)
	if screen == nil || window == nil {
		return nil
	}
	var editor *ui.TextArea
	for _, child := range screen.Children() {
		if provider, ok := child.(ui.TextEditorProvider); ok {
			editor, _ = provider.TextEditor().(*ui.TextArea)
			break
		}
	}
	if editor == nil {
		return nil
	}
	state := editor.EditState()
	command := m.submitPreparedInput(sessionID, provider.PlainInput(text), 0)
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		message := command()
		if admission, ok := message.(inputAdmissionMsg); ok {
			admission.composerWindow, admission.composerEditor, admission.composerState = window, editor, state
			return admission
		}
		return message
	}
}

func (m *App) applyInputAdmission(msg inputAdmissionMsg) tea.Cmd {
	if _, pending := m.pendingInputAdmissions[msg.intent]; !pending || msg.intent == nil {
		return nil
	}
	if msg.workspace == nil || msg.workspace != m.Workspace || msg.workspaceGen != m.workspaceSwitchGen.Load() ||
		msg.session == nil || m.SessionsByAgent[msg.agentID] != msg.session {
		delete(m.pendingInputAdmissions, msg.intent)
		return nil
	}
	switch msg.result.Status {
	case workspace.InputAdmissionAccepted, workspace.InputAdmissionPreservationPending:
		receipt := msg.result.Receipt
		if receipt.SessionID != msg.session.SessionID || receipt.AgentID != msg.agentID || receipt.InputID != msg.intent.InputID ||
			receipt.DraftRevision != msg.draftRevision || receipt.OwnerEpoch != msg.intent.OwnerEpoch ||
			receipt.TurnGeneration != msg.intent.TurnGeneration || receipt.ContextRevision == 0 || receipt.AcceptedAt.IsZero() {
			return nil
		}
		delete(m.pendingInputAdmissions, msg.intent)
		if !m.ApplyAgentInputAdmission(msg.intent, msg.result, msg.input, msg.referenceLabels) {
			return nil
		}
		m.syncAgentMessages(msg.agentID, true)
		if msg.composerWindow == nil && m.ActiveSession == msg.session && m.AgentView.ActiveID() == msg.agentID &&
			m.Input.DraftEpoch() == msg.draftEpoch && m.Input.ClearInputAtRevision(msg.draftRevision) {
			m.persistComposerDraftIfChanged()
		}
		if msg.composerWindow != nil && m.overlayHost.Window(ui.OverlayComposer) == msg.composerWindow &&
			msg.composerEditor.EditState() == msg.composerState {
			m.overlayHost.Close(ui.OverlayComposer)
		}
		historyCmd := m.recordPromptWithReferences(msg.input.Text, msg.references)
		if msg.result.Status == workspace.InputAdmissionPreservationPending && m.Feedback != nil {
			m.Feedback.NotifyInfo("Message saved to context; execution is not available")
		}
		return historyCmd
	case workspace.InputAdmissionRejected:
		delete(m.pendingInputAdmissions, msg.intent)
		if m.Feedback != nil {
			m.Feedback.NotifyError("Message was not sent; your draft is kept")
		}
	}
	return nil
}

func (m *App) submitSteering(sessionID, text string, refIDs []string) tea.Cmd {
	n := m.AgentMgr.Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	input := provider.InputWithRefs(text, refIDs)
	input.InternalID = fmt.Sprintf("steering-%016x", rand.Uint64())
	if m.Workspace == nil || !m.SteerAgent(sessionID, input) {
		if m.Feedback != nil {
			m.Feedback.NotifyError("human steering was not queued: this turn is no longer accepting notes")
		}
		return nil
	}

	m.Input.ClearInputAtRevision(m.Input.DraftRevision())
	m.persistComposerDraftIfChanged()
	m.syncPendingSteeringCount()
	m.syncAgentMessages(sessionID, true)
	return m.recordPrompt(text, refIDs...)
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
	return m.handleAgentDomainEvent(workspace.AgentEvent{Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID})
}

func (m *App) handleAgentEnvelope(evt agent.AgentEvent) tea.Cmd {
	return m.handleAgentDomainEvent(workspace.AgentEvent{Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID, Identity: evt.Identity})
}

func (m *App) handleAgentDomainEvent(evt workspace.AgentEvent) tea.Cmd {
	// Service notices do not mutate the conversation. Their presentation stays
	// client-local while all semantic events enter the workspace reducer once.
	switch evt.Type {
	case "response_continuation_unavailable":
		data, _ := evt.Data.(map[string]any)
		reason, _ := data["reason"].(string)
		message := "Responses delta unavailable: the endpoint returned no response ID; full uploads continue"
		if reason == "response_not_stored" {
			message = "Responses delta unavailable: the endpoint declined server storage; full uploads continue"
		}
		if m.Feedback != nil {
			m.Feedback.NotifyInfo(message)
		}
		return nil
	case "memory_saved":
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
		return m.refreshMemoryBrowser()
	case "async_command_status":
		if commands, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
			m.updateSessionBackgroundJobs(evt.AgentID, commands, time.Now())
		}
		m.syncAgentWindowTabs()
		return nil
	}
	change := m.ApplyAgentEvent(evt)
	if !change.Accepted {
		return nil
	}
	if evt.Type == "collaboration_delivery_admitted" || evt.Type == "collaboration_delivery_rejected" {
		// Delivery settlement changes only existing evidence. It is not an
		// inventory/navigation event and must never activate or hydrate a draft.
		for _, id := range change.ChangedAgents {
			m.syncAgentMessages(id, false)
		}
		m.syncPendingSteeringCount()
		return nil
	}
	return m.presentAgentReduction(change)
}

// presentAgentReduction consumes committed domain changes. It may change local
// tabs, geometry, dialogs and feedback, but never replays the semantic event.
func (m *App) presentAgentReduction(change workspace.AgentReduction) tea.Cmd {
	for _, id := range change.ChangedAgents {
		m.ensureAgentProjection(id)
		if node := m.AgentMgr.Get(id); node != nil && node.State != nil {
			m.reconcileThinkingPresentation(node.State, m.thinkingOwnerWidth(id))
		}
		m.syncAgentMessages(id, change.Effects.Done != nil || change.Effects.Error != nil)
	}
	m.applyAgentEffects(change.AgentID, change.Effects)
	if change.ChildCompleted {
		if node := m.AgentMgr.Get(change.AgentID); node != nil {
			m.publishSubAgentTerminalStatus(node.ID, node.Label, node.Category, node.State.Status)
		}
		m.archiveSubAgentTab(change.AgentID)
	}
	if change.Effects.Done != nil || change.Effects.Error != nil {
		m.dismissAgentPromptFor(change.AgentID)
		m.handleSubPaneCompletion(change.AgentID)
	}
	m.syncPendingSteeringCount()
	m.syncAgentWindowTabs()
	if done := change.Effects.Done; done != nil {
		return func() tea.Msg { return agentResultFeedbackMsg{agentID: done.AgentID, reason: done.Reason} }
	}
	return nil
}

// applyAgentEffects projects neutral reducer output into this shell. The
// reducer owns state transitions; it never opens a UI or calls back into it.
func (m *App) applyAgentEffects(agentID string, effects workspace.AgentEffects) {
	if effects.Blocking {
		m.OverlayState.SetBlocking(true)
		m.Feedback.NotifyInfo("")
	}
	if prompt := effects.Prompt; prompt != nil {
		request := agentPromptRequest{agentID: agentID, blockID: prompt.BlockID,
			question: prompt.Question, operation: prompt.Operation, tool: prompt.Tool,
			target: prompt.Target, risk: prompt.Risk, reason: prompt.Reason,
			suggestion: prompt.Suggestion, questionsJSON: prompt.QuestionsJSON}
		if prompt.IsQuestion {
			request.kind = agentPromptQuestions
			m.enqueueAgentQuestions(request)
		} else {
			request.kind = agentPromptConfirmation
			m.enqueueAgentConfirmation(request)
		}
	}
	if effects.DismissPrompt {
		m.dismissAgentPromptBlock(agentID, effects.DismissBlockID)
	}
}

func (m *App) syncPendingSteeringCount() {
	if m.Feedback == nil || m.AgentMgr == nil {
		return
	}
	count := 0
	for _, node := range m.AgentMgr.AllNodes() {
		if node != nil && node.State != nil && node.State.Messages != nil {
			count += node.State.Messages.PendingSteeringCount()
		}
	}
}

// ThinkingOverflowInnerW is the focused chat width, for shell geometry callers.
func (m *App) ThinkingOverflowInnerW() int { return m.thinkingOwnerWidth(m.AgentView.ActiveID()) }

func (m *App) thinkingOverflowLatched() bool { return m.thinkingLatch[m.ActiveSession] }
func (m *App) clearThinkingOverflowLatch(session *workspace.Session) {
	delete(m.thinkingLatch, session)
}

func (m *App) promoteThinkingToPane(agent *workspace.AgentState) {
	m.installThinkingPresentation(agent)
	m.syncAgentMessages(agent.ID, false)
}

func (m *App) nextThinkingPaneID(agentID string) string {
	for {
		m.thinkingPaneSequence++
		paneID := fmt.Sprintf("thinking-%s-%d", agentID, m.thinkingPaneSequence)
		if !m.PaneMgr.Has(paneID) && !m.subPaneProjections.Has(paneID) {
			return paneID
		}
	}
}

func (m *App) handleSubPaneCompletion(agentID string) {
	n := m.AgentMgr.Get(agentID)
	if n == nil {
		return
	}
	if n.State != nil && m.thinkingPaneID(n.State) != "" {
		m.dropThinkingPresentation(n.State)
		m.syncAgentMessages(agentID, false)
		return
	}

	if n.Parent != nil {
		m.subPaneProjections.InvalidateSubPane(n.Parent.ID, agentID)
	}
}

type agentResultFeedbackMsg struct {
	agentID string
	reason  string
}

func (m *App) handleAgentResultFeedback(msg agentResultFeedbackMsg) {
	node := m.AgentMgr.Get(msg.agentID)
	if node == nil || node.Parent != nil || msg.reason == "" || msg.reason == "cancelled" {
		return // child terminal feedback is richer in sub_agent_completed
	}
	label := workspace.AgentDisplayLabel(node)
	if label == "" {
		label = "Main Agent"
	}
	severity := coretypes.SeverityInfo
	message := "✓ " + label + " completed"
	if msg.reason != "completed" {
		severity, message = coretypes.SeverityError, "✕ "+label+" "+msg.reason
	}
	m.Feedback.NotifyAgentTransient(msg.agentID, message, severity)
	m.forceFrame()
}

// handleSubAgentCompleted is also used by local lifecycle fixtures. Production
// events use handleAgentDomainEvent and the same exact domain reducer.
func (m *App) handleSubAgentCompleted(evt workspace.AgentEvent) {
	if evt.AgentID == "" {
		if data, ok := evt.Data.(map[string]string); ok {
			evt.AgentID = data["agent_id"]
		}
	}
	change := m.ApplyAgentEvent(evt)
	if change.Accepted {
		m.presentAgentReduction(change)
	}
}

func (m *App) publishSubAgentTerminalStatus(agentID, name, category, status string) {
	if name == "" {
		if node := m.AgentMgr.Get(agentID); node != nil {
			name = workspace.AgentDisplayLabel(node)
		}
	}
	if name == "" {
		name = agentID
	}
	identity := name
	if role := workspace.CategoryDisplayName(category); role != "" {
		identity += " · " + role
	}
	marker, outcome, severity := "✕", "failed", coretypes.SeverityError
	switch status {
	case "completed":
		marker, outcome, severity = "✓", "completed", coretypes.SeverityInfo
	case "cancelled":
		marker, outcome, severity = "○", "cancelled", coretypes.SeverityInfo
	case "crashed":
		outcome = "crashed"
	case "failed_timeout":
		outcome = "timed out"
	case "failed_quota":
		outcome = "quota exceeded"
	case "failed_policy":
		outcome = "blocked by policy"
	}
	m.Feedback.NotifyAgentTransient(agentID, marker+" "+identity+" "+outcome, severity)
	m.forceFrame()
}
