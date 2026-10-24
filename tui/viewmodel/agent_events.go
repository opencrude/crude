package viewmodel

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent"
	"crude/metrics"
	"crude/provider"
	"crude/sessiondb"
	"crude/shellport"
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
	workspace       shellport.Workspace
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
	submitted       time.Time
}

func (m *App) submitPreparedInput(sessionID string, input provider.UserInput, draftRevision uint64) tea.Cmd {
	n := m.ws.AgentMgr().Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	input.ActivePaths = m.memoryContextPaths()
	owner := m.ws
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
	frontend := owner.SessionsByAgent()[sessionID]
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
	submitted := time.Now()
	if draftRevision != 0 {
		m.Input.MarkSending(draftRevision)
	}
	return func() tea.Msg {
		result := owner.CommitAgentInputAdmission(context.Background(), intent)
		return inputAdmissionMsg{workspace: owner, workspaceGen: workspaceGen, session: frontend, intent: intent,
			agentID: sessionID, draftRevision: intent.DraftRevision, draftEpoch: draftEpoch,
			input: input, referenceLabels: labels, references: references, result: result, submitted: submitted}
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
	if !msg.submitted.IsZero() {
		promptAdmission.Since(msg.submitted)
	}
	if msg.composerWindow == nil && msg.draftRevision != 0 {
		m.Input.ClearSending(msg.draftRevision)
	}
	if _, pending := m.pendingInputAdmissions[msg.intent]; !pending || msg.intent == nil {
		return nil
	}
	if msg.workspace == nil || msg.workspace != m.ws || msg.workspaceGen != m.workspaceSwitchGen.Load() ||
		msg.session == nil || m.ws.SessionsByAgent()[msg.agentID] != msg.session {
		delete(m.pendingInputAdmissions, msg.intent)
		return nil
	}
	switch msg.result.Status {
	case workspace.InputAdmissionAccepted, workspace.InputAdmissionPreservationPending, workspace.InputAdmissionQueued, workspace.InputAdmissionSteered:
		receipt := msg.result.Receipt
		queued := msg.result.Status == workspace.InputAdmissionQueued
		steered := msg.result.Status == workspace.InputAdmissionSteered
		if receipt.SessionID != msg.session.SessionID || receipt.AgentID != msg.agentID || receipt.InputID != msg.intent.InputID ||
			receipt.DraftRevision != msg.draftRevision || receipt.OwnerEpoch != msg.intent.OwnerEpoch || receipt.AcceptedAt.IsZero() ||
			// Queued input has no context revision yet; steering instead carries
			// the immutable journal event and exact run that accepted it.
			steered && (receipt.EventID == "" || receipt.RunID == "" || receipt.OwnerEpoch == 0) ||
			!queued && !steered && (receipt.TurnGeneration != msg.intent.TurnGeneration || receipt.ContextRevision == 0) {
			return nil
		}
		delete(m.pendingInputAdmissions, msg.intent)
		if !m.ws.ApplyAgentInputAdmission(msg.intent, msg.result, msg.input, msg.referenceLabels) {
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
		if queued && m.Feedback != nil {
			m.Feedback.NotifyInfo("Queued · sends when the current work finishes")
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
	n := m.ws.AgentMgr().Get(sessionID)
	if n == nil || n.State == nil {
		return nil
	}
	if _, connected := m.ws.(shellport.Connection); connected {
		// This explicit helper still refuses an already-idle target. An Enter
		// gesture on a running replica uses the same admission as ordinary Send;
		// only the core can decide whether that run is still accepting notes.
		if (!n.State.IsActive() || n.State.Completed || n.State.Cancelling) && !m.agentCompacting(sessionID) {
			m.Feedback.NotifyError("Note was not accepted; your draft is kept")
			return nil
		}
		return m.submitPreparedInput(sessionID, provider.InputWithRefs(text, refIDs), m.Input.DraftRevision())
	}
	input := provider.InputWithRefs(text, refIDs)
	input.InternalID = fmt.Sprintf("steering-%016x", rand.Uint64())
	if m.ws == nil {
		return nil
	}
	if !m.ws.SteerAgent(sessionID, input) {
		// A manual compaction has no run to take notes: send it as ordinary
		// input, which the owner queues durably until compaction finishes.
		if m.agentCompacting(sessionID) {
			return m.submitPreparedInput(sessionID, provider.InputWithRefs(text, refIDs), m.Input.DraftRevision())
		}
		if m.Feedback != nil {
			m.Feedback.NotifyError("Your note was not sent: this turn is no longer accepting notes")
		}
		return nil
	}

	m.Input.ClearInputAtRevision(m.Input.DraftRevision())
	m.persistComposerDraftIfChanged()
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

func (m *App) handleAgentEvent(evt coretypes.AgentEventMsg) tea.Cmd {
	return m.handleAgentDomainEvent(workspace.AgentEvent{Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID})
}

func (m *App) handleAgentEnvelope(evt agent.AgentEvent) tea.Cmd {
	return m.handleAgentDomainEvent(workspace.AgentEvent{Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID, Identity: evt.Identity})
}

func (m *App) handleAgentDomainEvent(evt workspace.AgentEvent) tea.Cmd {
	// Service notices do not mutate the conversation. Their presentation stays
	// client-local while all semantic events enter the workspace reducer once.
	if notice, ok := workspace.NoticeFromEvent(evt); ok {
		return m.presentAgentNotice(notice)
	}
	return m.presentAgentReduction(m.ws.ApplyAgentEvent(evt))
}

// presentAgentNotice shows one typed service notice. Local ingress and core
// subscriptions share it, so every attached shell presents notices alike.
func (m *App) presentAgentNotice(notice workspace.AgentNotice) tea.Cmd {
	switch notice.Type {
	case workspace.NoticeAttachmentExcluded:
		if center := m.ws.Status(); center != nil {
			center.Notify(coretypes.NotificationRecord{DedupKey: "attachment:" + notice.AgentID + ":" + notice.Reason,
				Owner: coretypes.ResourceRef{Kind: coretypes.ResourceAgent, ID: notice.AgentID}, Severity: coretypes.SeverityWarning,
				Source: "attachment", Summary: notice.Reason, Detail: notice.Reason})
		}
		return nil
	case workspace.NoticeResponseContinuationUnavailable:
		message := "Responses delta unavailable: the endpoint returned no response ID; full uploads continue"
		if notice.Reason == "response_not_stored" {
			message = "Responses delta unavailable: the endpoint declined server storage; full uploads continue"
		}
		if m.Feedback != nil {
			m.Feedback.NotifyInfo(message)
		}
		return nil
	case workspace.NoticeMemorySaved:
		if notice.Count > 0 && m.Feedback != nil {
			text := fmt.Sprintf("Memory: saved %d from this session", notice.Count)
			if len(notice.Titles) > 0 {
				text = fmt.Sprintf("Memory: saved %q", notice.Titles[0])
				if notice.Count > 1 {
					text += fmt.Sprintf(" +%d more", notice.Count-1)
				}
			}
			m.Feedback.NotifyInfo(text)
		}
		return m.refreshMemoryBrowser()
	case workspace.NoticeAsyncCommandStatus:
		m.updateSessionBackgroundJobs(notice.AgentID, notice.Commands, time.Now())
		m.syncAgentWindowTabs()
	}
	return nil
}

// presentAgentReduction consumes committed domain changes. It may change local
// tabs, geometry, dialogs and feedback, but never replays the semantic event.
func (m *App) presentAgentReduction(change workspace.AgentReduction) tea.Cmd {
	if !change.Accepted {
		return nil
	}
	if change.EvidenceOnly {
		// Delivery settlement changes only existing evidence. It is not an
		// inventory/navigation event and must never activate or hydrate a draft.
		for _, id := range change.ChangedAgents {
			if node := m.ws.AgentMgr().Get(id); node != nil && node.State != nil {
				m.reconcileThinkingPresentation(node.State, m.thinkingOwnerWidth(id))
			}
			m.syncAgentMessages(id, false)
		}
		return nil
	}
	for _, id := range change.ChangedAgents {
		m.ensureAgentProjection(id)
		if node := m.ws.AgentMgr().Get(id); node != nil && node.State != nil {
			m.reconcileThinkingPresentation(node.State, m.thinkingOwnerWidth(id))
		}
		m.syncAgentMessages(id, change.Effects.Done != nil || change.Effects.Error != nil)
	}
	m.applyAgentEffects(change.AgentID, change.Effects)
	if change.ChildCompleted {
		if node := m.ws.AgentMgr().Get(change.AgentID); node != nil {
			m.publishSubAgentTerminalStatus(node.ID, node.Label, node.Category, node.State.Status)
		}
		m.archiveSubAgentTab(change.AgentID)
	}
	if change.Effects.Done != nil || change.Effects.Error != nil {
		m.dismissAgentPromptFor(change.AgentID)
		m.handleSubPaneCompletion(change.AgentID)
	}
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
		if !m.ws.PaneMgr().Has(paneID) && !m.subPaneProjections.Has(paneID) {
			return paneID
		}
	}
}

func (m *App) handleSubPaneCompletion(agentID string) {
	n := m.ws.AgentMgr().Get(agentID)
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
	node := m.ws.AgentMgr().Get(msg.agentID)
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
	if msg.reason == "completed" {
		m.offerPlanImplementation(msg.agentID)
	}
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
	change := m.ws.ApplyAgentEvent(evt)
	if change.Accepted {
		m.presentAgentReduction(change)
	}
}

func (m *App) publishSubAgentTerminalStatus(agentID, name, category, status string) {
	if name == "" {
		if node := m.ws.AgentMgr().Get(agentID); node != nil {
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

// agentCompacting reports a visible context compaction on the exact agent.
func (m *App) agentCompacting(agentID string) bool {
	node := m.ws.AgentMgr().Get(agentID)
	return node != nil && node.State != nil && node.State.ActiveCompaction.Phase != ""
}

var promptAdmission = metrics.NewLatency(metrics.Meta{Name: "ui.prompt", Group: metrics.GroupUI, Title: "Send to accepted",
	Help: "Enter in the composer until the core durably accepted the prompt and the shell applied that receipt.",
	Warn: 150_000, Crit: 500_000})
