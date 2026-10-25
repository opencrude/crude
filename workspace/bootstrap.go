package workspace

// bootstrap.go handles the backend session lifecycle for a workspace.
//
// The workspace owns all sessions for one project. This file provides:
// - Backend session creation and resumption
// - Conversation history hydration into the TUI
// - Message format conversion (sessiondb → TUI display format)
// - Workspace-scoped session listing and deletion
//
// The app layer creates a workspace and starts the TUI. It never touches
// session internals directly.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
	"crude/types/attachment"
)

// Bootstrap creates or resumes a backend session for this workspace.
// When resumeID is empty, a new session is created. Prefer leaving the
// launcher to call CreateSession/ResumeSession so multi-session works
// without a dummy session at startup.
func (w *Workspace) Bootstrap(cfg *session.Config, homeDir string, resumeID string) (*Session, error) {
	var fs *Session
	var err error

	if resumeID != "" {
		fs, err = w.ResumeSession(resumeID, cfg, homeDir)
		if err != nil {
			w.Logger.Warn("WORKSPACE", "resume_failed", "id", resumeID, "error", err.Error())
			return nil, fmt.Errorf("resume session: %w", err)
		}
	} else {
		fs, err = w.CreateSession(cfg, homeDir)
	}

	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return fs, nil
}

// CreateSession creates a new AI conversation in this workspace.
// The session DB, logs, and reports are stored under the workspace folder.
func (w *Workspace) CreateSession(cfg *session.Config, homeDir string) (*Session, error) {
	op, err := w.prepareSessionLifecycle("", cfg, homeDir, false)
	if err != nil {
		return nil, err
	}
	result := op.Run(context.Background())
	if result.Err != nil {
		return nil, result.Err
	}
	w.installSessionLifecycle(result)
	return result.owner, nil
}

// ResumeSession loads an existing workspace-scoped session by ID.
func (w *Workspace) ResumeSession(sessionID string, cfg *session.Config, homeDir string) (*Session, error) {
	op, err := w.prepareSessionLifecycle(sessionID, cfg, homeDir, false)
	if err != nil {
		return nil, err
	}
	result := op.Run(context.Background())
	if result.Err != nil {
		return nil, result.Err
	}
	w.installSessionLifecycle(result)
	return result.owner, nil
}

// addSession registers a frontend session in the canonical collections.
func (w *Workspace) addSession(fs *Session) {
	w.Sessions = append(w.Sessions, fs)
	w.SessionsByID[fs.SessionID] = fs
}

// HydrateSession loads conversation history into the explicitly owned frontend
// session. Hydration restores saved state without reviving execution.
func (w *Workspace) HydrateSession(sessionID string) {
	frontend := w.SessionsByID[sessionID]
	if frontend == nil || frontend.backend == nil || frontend.hydrated || frontend.AgentID == "" {
		return
	}
	views := w.prepareSessionHydration(frontend)
	w.installSessionHydration(frontend, views)
}

func (w *Workspace) prepareSessionHydration(frontend *Session) []ChatMessageView {
	msgs := frontend.backend.MessagesWithMeta()
	views := ConvertMessagesForDisplay(msgs)
	views = w.hydrateRecoveryViews(views, frontend)
	for _, record := range frontend.backend.CompactionRecords("") {
		views = append(views, compactionRecordView(record))
	}
	for _, row := range frontend.backend.StatusRows() {
		views = append(views, statusRowView(row))
	}

	if db := frontend.DB; db != nil {
		if records, err := sessiondb.ListUsageRecords(db); err == nil {
			frontend.usageMu.Lock()
			frontend.usageRecords = make(map[string]sessiondb.UsageRecord, len(records))
			frontend.usageCache = make(map[usageCacheKey]ttypes.UsageSummary)
			frontend.usageVersion++
			for _, record := range records {
				frontend.usageRecords[record.RequestID] = record
			}
			frontend.usageMu.Unlock()
		} else {
			w.Logger.Warn("WORKSPACE", "usage_hydrate_failed", "error", err.Error())
		}
		if refs, err := sessiondb.LoadPromptReferences(db); err == nil && frontend.RefStore != nil {
			for _, ref := range refs {
				frontend.RefStore.AddExisting(ref)
			}
		}
	}
	slices.SortStableFunc(views, func(left, right ChatMessageView) int { return left.Time.Compare(right.Time) })
	return views
}

func (w *Workspace) installSessionHydration(frontend *Session, views []ChatMessageView) {
	node := w.RegisterAgentRoot(frontend.AgentID)
	if node == nil || node.State == nil {
		return
	}
	if len(views) > 0 {
		for _, view := range views {
			node.State.Messages.AppendMessage(messageFromView(view))
		}
		node.State.Messages.MarkStreamingToolsCancelled()
		node.State.Messages.MarkAllThinkingInterrupted()
	}
	// Prompts queued behind a compaction survive restart as pending rows; a
	// passive resume never runs them until the owner next admits input.
	if frontend.backend != nil {
		for _, queued := range frontend.backend.QueuedInputs() {
			node.State.Messages.AppendMessageIfAbsent(queuedInputMessage(queued.Input.InternalID, queued.Input.Text, queued.Input.DisplayText, queued.QueuedAt))
		}
	}
	frontend.hydrated = true
	w.agentRevision++
}

// HydrateSubAgents rebuilds completed review and unfinished recovery surfaces
// under the root. Hydration never revives execution.
func (w *Workspace) HydrateSubAgents(parentID string) []string {
	if w == nil || w.AgentMgr == nil || w.PaneMgr == nil || w.AgentMgr.Get(parentID) == nil {
		return nil
	}
	frontendSession := w.sessionForAgent(parentID)
	if frontendSession == nil || frontendSession.backend == nil {
		return nil
	}
	snapshots := frontendSession.backend.CompletedSubAgentSnapshots()
	snapshots = append(snapshots, frontendSession.backend.PendingSubAgentSnapshots()...)
	return w.installPreparedSubAgents(parentID, frontendSession, prepareSubAgentViews(snapshots))
}

type preparedSubAgent struct {
	snapshot session.SubAgentSnapshot
	views    []ChatMessageView
}

func prepareSubAgentViews(snapshots []session.SubAgentSnapshot) []preparedSubAgent {
	prepared := make([]preparedSubAgent, 0, len(snapshots))
	for _, snapshot := range snapshots {
		views := ConvertMessagesForDisplay(snapshot.Messages)
		for _, record := range snapshot.Compactions {
			views = append(views, compactionRecordView(record))
		}
		slices.SortStableFunc(views, func(left, right ChatMessageView) int { return left.Time.Compare(right.Time) })
		prepared = append(prepared, preparedSubAgent{snapshot: snapshot, views: views})
	}
	return prepared
}

func (w *Workspace) installPreparedSubAgents(parentID string, frontendSession *Session, prepared []preparedSubAgent) []string {
	var hydrated []string
	for _, item := range prepared {
		snapshot := item.snapshot

		result := snapshot.Result
		id := string(result.AgentID)
		if id == "" || w.AgentMgr.Get(id) != nil {
			continue
		}
		label := result.Name
		if label == "" {
			label = CategoryDisplayName(result.Category)
		}
		if label == "" {
			label = id
		}
		pane := w.PaneMgr.GetOrCreate(id, PaneAgent, label)
		paneViews := item.views
		for _, view := range paneViews {
			pane.Messages.AppendMessage(messageFromView(view))
		}
		if len(pane.Messages.Messages()) == 0 && (result.SummaryGist != "" || result.ReportID != "") {
			content := result.SummaryGist
			if result.ReportID != "" {
				if content != "" {
					content += "\n\n"
				}
				content += fmt.Sprintf("Report: %s · revision %d", result.ReportID, result.ReportRevision)
			}
			pane.Messages.AppendMessage(Message{Role: MessageRoleAssistant, Content: content, Time: time.Now()})
		}
		if result.DurationMs > 0 {
			pane.StartTime = time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		}
		pane.TokenCount = result.PromptTokens + result.CompTokens
		pane.Status = string(result.Status)
		pane.Collapse()

		state := NewAgentState(id, pane.Messages, w.Logger)
		state.Completed = true
		state.Status = string(result.Status)
		if state.Status == "" {
			state.Status = "completed"
		}
		if snapshot.Recovery != nil {
			state.Completed = false
			state.Recovery = *snapshot.Recovery
			state.Status = string(snapshot.Recovery.State)
			pane.Status = state.Status
			pane.Messages.UpdateRecovery(*snapshot.Recovery)
		}
		model := snapshot.Model
		if model == "" {
			model = frontendSession.backend.ModelName()
		}
		node := w.AgentMgr.AddSubAgent(id, parentID, label, model, result.Category, state)
		if node == nil {
			w.PaneMgr.Remove(id)
			continue
		}
		hydrated = append(hydrated, id)
	}
	return hydrated
}

// ── Backend Session Accessors

// BackendSession returns the underlying backend for the specified session.
func (w *Workspace) BackendSession(sessionID string) *session.Session {
	if frontend := w.SessionsByID[sessionID]; frontend != nil {
		return frontend.backend
	}
	return nil
}

// HandleSettingsSaveForSession is the serialized, synchronous domain path.
// UI callers prepare on their owner thread, run off-thread, then apply the result.
func (w *Workspace) HandleSettingsSaveForSession(sessionID string, settings ttypes.Settings) error {
	operation, err := w.PrepareSessionSettingsSave(sessionID, settings)
	if err != nil {
		return err
	}
	result := operation.Run()
	w.ApplySessionSettingsResult(result)
	return result.Err
}

// WorkspaceAgentDefaults returns the effective defaults shown by the
// persistent settings dialog.
func (w *Workspace) WorkspaceAgentDefaults() ttypes.AgentsSettings {
	if w.sessionCfg == nil {
		return ttypes.AgentsSettings{}
	}
	return session.ConfigToSettings(w.sessionCfg).Agents
}

// applyProviderSettings installs provider connection defaults without changing any
// existing session's selected account. Persistence belongs to SaveAppSettings.
func (w *Workspace) applyProviderSettings(settings ttypes.AppSettings) {
	if w.sessionCfg == nil {
		return
	}
	w.sessionCfg.Accounts = cloneAccounts(settings.Accounts)
}

// HandleFetchModels lists models for an explicitly selected account. It never
// borrows the active tab's credentials or another account's default.
func (w *Workspace) HandleFetchModels(providerID, accountID, baseURL, standardID string) ([]ttypes.ModelEntry, error) {
	return w.PrepareAccountModels(providerID, accountID, baseURL, standardID)()
}

// HandleCompactForAgent compacts only the session captured when the slash
// command was submitted.
func (w *Workspace) HandleCompactForAgent(agentID string) ttypes.CompactResultMsg {
	prepared, result := w.PrepareCompactForAgent(agentID)
	if prepared.Run == nil {
		return result
	}
	return prepared.Run()
}

// PrepareCompactForAgent registers lifecycle ownership synchronously so the
// TUI can paint and cancel the operation before its tea.Cmd begins I/O.
func (w *Workspace) PrepareCompactForAgent(agentID string) (session.PreparedCompaction, ttypes.CompactResultMsg) {
	if w == nil || w.AgentMgr == nil {
		return session.PreparedCompaction{}, ttypes.CompactResultMsg{Error: "no active session"}
	}
	node := w.AgentMgr.Get(agentID)
	if node == nil {
		return session.PreparedCompaction{}, ttypes.CompactResultMsg{Error: "no active session"}
	}
	if node.Parent != nil {
		return session.PreparedCompaction{}, ttypes.CompactResultMsg{Error: "Child contexts compact automatically while their delegated work is active"}
	}
	if sess := w.sessionForAgent(agentID); sess != nil && sess.backend != nil {
		return sess.backend.PrepareCompaction(agentID)
	}
	return session.PreparedCompaction{}, ttypes.CompactResultMsg{Error: "no active session"}
}

// humanName is how the human appears to people and agents: their username.
// The event's OriginHuman, not the name, tells models it is the user.
func (w *Workspace) humanName() string {
	if w.Username != "" {
		return w.Username
	}
	return "user"
}

// PlanModeForAgent reports Plan mode of the session that owns agentID.
func (w *Workspace) PlanModeForAgent(agentID string) bool {
	sess := w.sessionForAgent(agentID)
	return sess != nil && sess.backend != nil && sess.backend.PlanMode()
}

// PrepareSetPlanMode resolves the owning session on the workspace owner; the
// returned persistence runs off it. Plan mode applies from the next turn.
func (w *Workspace) PrepareSetPlanMode(agentID string, enabled bool) (func() error, error) {
	sess := w.sessionForAgent(agentID)
	if sess == nil || sess.backend == nil {
		return nil, errors.New("no session owns this agent")
	}
	backend := sess.backend
	return func() error { return backend.SetPlanMode(enabled) }, nil
}

// SetPlanModeForAgent is PrepareSetPlanMode run in place, for callers already
// off the owner thread.
func (w *Workspace) SetPlanModeForAgent(agentID string, enabled bool) error {
	run, err := w.PrepareSetPlanMode(agentID, enabled)
	if err != nil {
		return err
	}
	return run()
}

// SetYoloForAgent updates the owning session's live safety policy.
func (w *Workspace) SetYoloForAgent(agentID string, enabled bool) bool {
	if sess := w.sessionForAgent(agentID); sess != nil && sess.backend != nil {
		sess.backend.SetYolo(enabled)
		return true
	}
	return false
}

// YoloForAgent reports the owning session's current live safety policy.
func (w *Workspace) YoloForAgent(agentID string) bool {
	if sess := w.sessionForAgent(agentID); sess != nil && sess.backend != nil {
		return sess.backend.YoloEnabled()
	}
	return false
}

// AgentTurnActive reports the backend lifecycle for the session owning agentID.
func (w *Workspace) AgentTurnActive(agentID string) bool {
	if sess := w.sessionForAgent(agentID); sess != nil && sess.backend != nil {
		return sess.backend.IsTurnActive()
	}
	if w.AgentMgr != nil {
		if node := w.AgentMgr.Get(agentID); node != nil && node.State != nil {
			return node.State.IsActive()
		}
	}
	return false
}

// CancelSubAgent cancels a specific sub-agent by ID.
func (w *Workspace) CancelSubAgent(agentID string) {
	sess := w.sessionForAgent(agentID)
	if sess == nil || sess.backend == nil {
		return
	}
	runner := sess.backend.SubAgentRunner()
	if runner != nil {
		if handle, ok := runner.Manager().GetActive(agent.AgentID(agentID)); ok {
			handle.Cancel()
		}
	}
}

// CancelAgent requests cancellation for one exact runtime generation. Submission
// is not proof the control applied; the owner publishes the authoritative outcome.
func (w *Workspace) CancelAgent(agentID string) {
	if w == nil || agentID == "" {
		return
	}
	// The backend generation is authoritative for execution, including the
	// prepared-before-collaboration window. Stop it first; collaboration records
	// the attributed control when a run already exists.
	if frontend := w.SessionsByAgent[agentID]; frontend != nil && frontend.backend != nil {
		frontend.backend.CancelCompaction(agentID)
		frontend.backend.CancelOrchestrator(agentID)
	}
	if w.Collaboration == nil {
		return
	}
	participant, err := w.Collaboration.Agent(context.Background(), agentID)
	if err != nil || participant.CurrentRunID == "" {
		return
	}
	run, err := w.Collaboration.Run(context.Background(), participant.CurrentRunID)
	if err != nil || run.AgentID != agentID || run.State.Terminal() {
		return
	}
	_, _ = w.Collaboration.Publish(context.Background(), collaboration.PublishRequest{
		SenderID:       "human",
		SenderName:     w.humanName(),
		IdempotencyKey: fmt.Sprintf("human-control:%s:cancel:%d", run.ID, run.Version),
		RecipientIDs:   []string{agentID},
		Origin:         collaboration.OriginHuman,
		Kind:           collaboration.EventLifecycleControl,
		Body:           "Cancel requested by the user.",
		RunID:          run.ID,
		References: []collaboration.Reference{{
			Kind: "control", ID: "cancel", Revision: int(run.Version),
		}},
	})
}

// SubmitAgentInput registers a cancellable turn with the owning backend before
// the TUI projects the optimistic user message.
func (w *Workspace) SubmitAgentInput(agentID string, input provider.UserInput) bool {
	if w == nil || w.Collaboration == nil || agentID == "" {
		return false
	}
	participant, err := w.Collaboration.Agent(context.Background(), agentID)
	if err != nil || participant.ParentID != "" || participant.CurrentRunID != "" ||
		participant.State != collaboration.AgentAvailable ||
		participant.Presence != collaboration.RuntimeAttached {
		return false
	}
	if sess := w.SessionsByID[participant.SessionID]; sess != nil && sess.backend != nil {
		return sess.backend.SubmitInput(agentID, input)
	}
	return false
}

type InputAdmissionStatus = session.InputAdmissionStatus
type InputAdmissionErrorKind = session.InputAdmissionErrorKind
type InputAdmissionIntent = session.InputAdmissionIntent
type InputAdmissionReceipt = session.InputAdmissionReceipt
type InputAdmissionResult = session.InputAdmissionResult

const (
	InputAdmissionAccepted            = session.InputAdmissionAccepted
	InputAdmissionRejected            = session.InputAdmissionRejected
	InputAdmissionPreservationPending = session.InputAdmissionPreservationPending
	InputAdmissionQueued              = session.InputAdmissionQueued
	InputAdmissionSteered             = session.InputAdmissionSteered
)

// PrepareAgentInputAdmission reserves an exact owner/generation without I/O.
// Frontends call it while handling the submit gesture, then run the matching
// commit in a command before projecting or clearing the accepted draft.
func (w *Workspace) PrepareAgentInputAdmission(agentID string, input provider.UserInput, draftRevision uint64) (*InputAdmissionIntent, error) {
	if w == nil || agentID == "" {
		return nil, fmt.Errorf("input owner is unavailable")
	}
	frontend := w.SessionsByAgent[agentID]
	if frontend == nil || frontend.backend == nil {
		return nil, fmt.Errorf("input owner is unavailable")
	}
	return frontend.backend.PrepareInputAdmission(agentID, input, draftRevision)
}

// CommitAgentInputAdmission performs the durable publication off the UI thread.
func (w *Workspace) CommitAgentInputAdmission(ctx context.Context, intent *InputAdmissionIntent) InputAdmissionResult {
	if w == nil || intent == nil {
		return InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: session.InputAdmissionPreLocalRejection,
			Err: fmt.Errorf("input admission intent is unavailable")}
	}
	frontend := w.SessionsByAgent[intent.AgentID]
	if frontend == nil || frontend.backend == nil {
		return InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: session.InputAdmissionPreLocalRejection,
			Err: fmt.Errorf("input owner is unavailable")}
	}
	return frontend.backend.CommitInputAdmission(ctx, intent)
}

// SteerAgent publishes genuine human steering to one exact active run. Durable
// delivery, epoch fencing and context admission are owned by Collaboration.
func (w *Workspace) SteerAgent(agentID string, input provider.UserInput) bool {
	_, accepted, _ := w.AdmitAgentSteering(agentID, input)
	return accepted
}

// AdmitAgentSteering distinguishes a confirmed no-effect refusal from an
// unavailable/uncertain persistence result. Only the former permits the shell
// to route an unchanged send gesture through ordinary input admission.
func (w *Workspace) AdmitAgentSteering(agentID string, input provider.UserInput) (InputAdmissionReceipt, bool, error) {
	if w == nil || w.Collaboration == nil || agentID == "" || input.InternalID == "" {
		return InputAdmissionReceipt{}, false, nil
	}
	participant, err := w.Collaboration.Agent(context.Background(), agentID)
	if err != nil {
		if errors.Is(err, collaboration.ErrNotFound) {
			return InputAdmissionReceipt{}, false, nil
		}
		return InputAdmissionReceipt{}, false, err
	}
	if participant.State != collaboration.AgentAvailable ||
		participant.Presence != collaboration.RuntimeAttached ||
		participant.CurrentRunID == "" {
		return InputAdmissionReceipt{}, false, nil
	}
	run, err := w.Collaboration.Run(context.Background(), participant.CurrentRunID)
	if err != nil {
		return InputAdmissionReceipt{}, false, err
	}
	if run.AgentID != agentID || run.RuntimeEpoch != participant.RuntimeEpoch || run.State.Terminal() {
		return InputAdmissionReceipt{}, false, nil
	}
	references := []collaboration.Reference{{Kind: "history_message", ID: input.InternalID}}
	for _, id := range input.ReferenceIDs {
		if id != "" {
			references = append(references, collaboration.Reference{Kind: "prompt_reference", ID: id})
		}
	}
	owner := w.sessionForAgent(agentID)
	if owner == nil || owner.backend == nil {
		return InputAdmissionReceipt{}, false, nil
	}
	published, err := owner.backend.PublishHumanSteering(context.Background(), agentID, collaboration.PublishRequest{
		SenderID:       "human",
		SenderName:     w.humanName(),
		IdempotencyKey: "human-steering:" + agentID + ":" + input.InternalID,
		RecipientIDs:   []string{agentID},
		Origin:         collaboration.OriginHuman,
		Kind:           collaboration.EventHumanSteering,
		Body:           input.Text,
		RequestID:      participant.CurrentRequestID,
		RunID:          participant.CurrentRunID,
		References:     references,
	})
	if err != nil {
		if errors.Is(err, collaboration.ErrUnavailable) {
			return InputAdmissionReceipt{}, false, nil
		}
		return InputAdmissionReceipt{}, false, err
	}
	if node := w.AgentMgr.Get(agentID); node != nil && node.State != nil {
		var labels []string
		if owner := w.SessionForAgent(agentID); owner != nil && owner.RefStore != nil {
			for _, id := range input.ReferenceIDs {
				if ref, ok := owner.RefStore.GetReference(id); ok && ref.Active() {
					labels = append(labels, ref.DisplayName())
				}
			}
		}
		node.State.Messages.AppendMessageIfAbsent(Message{InternalID: input.InternalID,
			Role: MessageRoleUser, Origin: provider.OriginHumanSteering, Content: input.Text,
			ReferenceLabels: labels, Attachments: inputAttachmentTargets(owner, input), Time: time.Now(), PendingSteering: true})
		w.agentRevision++
	}
	return InputAdmissionReceipt{SessionID: owner.SessionID, AgentID: agentID, InputID: input.InternalID,
		OwnerEpoch: participant.RuntimeEpoch, AcceptedAt: published.Event.CreatedAt,
		EventID: published.Event.ID, RunID: published.Event.RunID}, true, nil
}

// CloseAgent stops backend work for agentID before the TUI removes its state.
// For root agents it also persists a recoverable registry record (Register +
// SyncSessionMeta + CloseSession) so the closed session remains listable and
// resumable. The session's own DB file is left intact.
func (w *Workspace) CloseAgent(agentID string) {
	node := w.AgentMgr.Get(agentID)
	if node == nil {
		return
	}
	if node.Parent != nil {
		w.CancelSubAgent(agentID)
		return
	}

	w.CancelAgent(agentID)
	sess := w.sessionForAgent(agentID)
	if sess != nil && sess.backend != nil {
		sess.backend.CloseOrchestrator(agentID)
	}
	// Persist registry metadata while we still have the Session reference and
	// (usually) an open session DB handle for SyncSessionMeta.
	if sess != nil {
		w.persistSessionRegistry(sess, sess.DB)
	}
	delete(w.SessionsByAgent, agentID)
	if sess != nil && sess.AgentID == agentID {
		sess.AgentID = ""
	}
}

// ResolveConfirmation sends a request-correlated permission response to the
// owning orchestrator.
func (w *Workspace) ResolveConfirmation(agentID, blockID, choice string) bool {
	switch choice {
	case "allow", "always", "deny":
	default:
		choice = "deny"
	}
	return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
		RequestID: blockID,
		Outcome:   ttypes.BlockAnswered,
		Value:     choice,
	})
}

// ResolveQuestion sends formatted question answers to the owning orchestrator.
// The human may skip any question, so an unanswered entry is reported as such
// rather than failing the whole response.
func (w *Workspace) ResolveQuestion(agentID, blockID string, answers, questions map[string]string, cancelled bool) bool {
	if cancelled {
		return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
			RequestID: blockID,
			Outcome:   ttypes.BlockUserCancelled,
		})
	}
	text, ok := formatQuestionAnswers(answers, questions)
	if !ok {
		return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
			RequestID: blockID,
			Outcome:   ttypes.BlockValidationError,
			Detail:    "no questions were answered",
		})
	}
	return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
		RequestID: blockID,
		Outcome:   ttypes.BlockAnswered,
		Value:     text,
	})
}

// formatQuestionAnswers renders the model-visible question result. Questions
// come back in payload order, and one the human left unanswered reads as
// "(no answer)" so the model can tell a deliberate skip from a missing entry.
func formatQuestionAnswers(answers, questions map[string]string) (string, bool) {
	if len(answers) == 0 {
		return "", false
	}
	var response strings.Builder
	for i := 1; i <= len(answers); i++ {
		key := fmt.Sprintf("%d", i)
		answer := strings.TrimSpace(answers[key])
		if answer == "" {
			answer = "(no answer)"
		}
		if i > 1 {
			response.WriteString("\n\n")
		}
		if question := questions[key]; question != "" {
			fmt.Fprintf(&response, "Q%d: %s\nA%d: %s", i, question, i, answer)
		} else {
			fmt.Fprintf(&response, "A%d: %s", i, answer)
		}
	}
	return response.String(), true
}

// RejectQuestion reports a frontend validation failure without attributing it
// to the user. The model receives actionable feedback and can retry the tool.
func (w *Workspace) RejectQuestion(agentID, blockID, detail string) bool {
	return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
		RequestID: blockID,
		Outcome:   ttypes.BlockValidationError,
		Detail:    detail,
	})
}

func (w *Workspace) resolveAgentBlock(agentID string, response ttypes.BlockResponse) bool {
	if w == nil || w.AgentMgr == nil || response.RequestID == "" {
		return false
	}
	root := w.AgentMgr.OrchestratorFor(agentID)
	if root == nil {
		return false
	}
	sess := w.sessionForAgent(root.ID)
	if sess == nil || sess.backend == nil {
		return false
	}
	if !sess.backend.ResolveBlock(agentID, response) {
		return false
	}
	// The exact backend decision is already accepted. Publish its semantic
	// acknowledgement now; a later event cannot clear a replacement block.
	w.ApplyAgentEvent(AgentEvent{AgentID: agentID, Type: "blocking_complete", Data: map[string]string{"block_id": response.RequestID}})
	return true
}

// SessionForAgent returns the session that owns the exact agent or its root.
func (w *Workspace) SessionForAgent(agentID string) *Session {
	return w.sessionForAgent(agentID)
}

func (w *Workspace) sessionForAgent(agentID string) *Session {
	if w == nil || agentID == "" {
		return nil
	}
	if w.AgentMgr != nil {
		if root := w.AgentMgr.OrchestratorFor(agentID); root != nil {
			agentID = root.ID
		}
	}
	if sess := w.SessionsByAgent[agentID]; sess != nil {
		return sess
	}
	for _, sess := range w.Sessions {
		if sess != nil && sess.AgentID == agentID {
			return sess
		}
	}
	return nil
}

// RecordAgentUsage applies a request-keyed provider usage event to its owner.
func (w *Workspace) RecordAgentUsage(agentID string, data any) (ttypes.UsageSummary, bool) {
	return w.recordAgentUsage(agentID, data)
}

func (w *Workspace) AgentUsage(agentID string) (ttypes.UsageSummary, bool) {
	return w.UsageSummary(agentID, ttypes.UsageThisAgent)
}

// ── Session List / Delete (workspace-scoped)

// ListSessions returns session metadata for this workspace only.
func (w *Workspace) ListSessions() []sessiondb.SessionMeta {
	if w.WorkspaceDB == nil {
		return nil
	}
	registered, err := w.WorkspaceDB.ListSessions()
	if err != nil {
		w.Logger.Warn("WORKSPACE", "list_sessions_failed", "error", err.Error())
		return nil
	}

	// Enrich registry entries with live metadata from each session DB.
	metas := make([]sessiondb.SessionMeta, 0, len(registered))
	for _, r := range registered {
		m := w.enrichSessionMeta(r)
		if m.ID != "" {
			metas = append(metas, m)
		}
	}
	return metas
}

// enrichSessionMeta fills in fields that live in the workspace session database.
//
// When neither path has a session DB, the registry row is marked db_missing
// (not DELETE'd) so a path-construction bug or transient absence cannot
// destroy the user's index. Reappearance clears the flag on a later enrich.
func (w *Workspace) enrichSessionMeta(r sessiondb.SessionMeta) sessiondb.SessionMeta {
	dbPath, err := SessionDBPath(w.RootPath, r.ID)
	if err != nil {
		return r
	}
	if _, err := os.Stat(dbPath); err != nil {
		_ = w.WorkspaceDB.MarkSessionDBMissing(r.ID, true)
		w.Logger.Warn("WORKSPACE", "session_db_missing", "id", r.ID, "path", dbPath)
		return sessiondb.SessionMeta{}
	}

	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return r
	}
	defer func() { _ = db.Close() }()

	m, err := sessiondb.GetMeta(db)
	if err != nil {
		return r
	}
	totalCost, _ := sessiondb.GetCumulativeCost(db)
	m.RoundCount, _ = sessiondb.CountAssistantRounds(db)
	// Preserve the model/provider from the registry if the DB is empty.
	if m.Model == "" {
		m.Model = r.Model
	}
	if m.Provider == "" {
		m.Provider = r.Provider
	}
	// DB is present; clear any prior missing mark (reversible).
	_ = w.WorkspaceDB.MarkSessionDBMissing(r.ID, false)
	// Write enriched metadata back to workspace_sessions so the registry
	// stays self-consistent without requiring enrichment on every list.
	_ = w.WorkspaceDB.UpdateSessionMeta(r.ID, m.TurnCount, m.MessageCount,
		nullStr(&m.FirstPrompt), nullStr(&m.LastPrompt), totalCost)
	return m
}

// DeleteSession removes a session from this workspace.
func (w *Workspace) DeleteSession(sessionID string) {
	if w.WorkspaceDB == nil {
		return
	}
	if _, err := w.WorkspaceDB.DB.Exec(`DELETE FROM workspace_sessions WHERE session_id = ?`, sessionID); err != nil {
		w.Logger.Warn("WORKSPACE", "delete_registry_failed", "id", sessionID, "error", err.Error())
	}

	dbPath, err := SessionDBPath(w.RootPath, sessionID)
	if err == nil {
		_ = os.Remove(dbPath)
	}
	sdir, err := SessionDir(w.RootPath, sessionID)
	if err == nil {
		_ = os.RemoveAll(sdir)
	}
}

// StartOrchestrator starts the backend orchestrator for the specified session.
// Returns the orchestrator's channels, or an error.
func (w *Workspace) StartOrchestrator(sessionID string, sender session.EventSender) (*session.Orchestrator, error) {
	frontend := w.SessionsByID[sessionID]
	if frontend == nil || frontend.backend == nil {
		return nil, fmt.Errorf("session %q has no backend", sessionID)
	}
	orch, err := frontend.backend.NewOrchestrator(sender)
	if err == nil && orch != nil {
		frontend.AgentID = orch.ID
		if w.SessionsByAgent == nil {
			w.SessionsByAgent = make(map[string]*Session)
		}
		w.SessionsByAgent[orch.ID] = frontend
		w.RegisterAgentRoot(orch.ID)
	}
	return orch, err
}

// ── Message Conversion

func compactionRecordView(record sessiondb.CompactionRecord) ChatMessageView {
	phase := "compaction_" + record.State
	stageName := record.StageName
	switch stageName {
	case "intent":
		stageName = "Extracting intent"
	case "work":
		stageName = "Distilling work"
	case "state":
		stageName = "Capturing state"
	case "continuation":
		stageName = "Preparing continuation"
	}
	timestamp := record.UpdatedAt
	if !record.EndedAt.IsZero() {
		timestamp = record.EndedAt
	}
	return ChatMessageView{
		InternalID:             record.OperationID,
		Role:                   MessageRoleStatus,
		Content:                record.Error,
		Time:                   timestamp,
		ProviderID:             record.ProviderID,
		AccountID:              record.AccountID,
		ModelID:                record.ModelID,
		LifecyclePhase:         phase,
		LifecycleOperationID:   record.OperationID,
		LifecycleTrigger:       record.Trigger,
		LifecycleStageName:     stageName,
		LifecycleStageIndex:    record.StageIndex,
		LifecycleCompleted:     record.CompletedStages,
		LifecycleStageCount:    record.StageCount,
		LifecycleRetryAttempt:  record.RetryAttempt,
		LifecycleRetryLimit:    record.RetryLimit,
		LifecycleBeforeTokens:  record.BeforeTokens,
		LifecycleAfterTokens:   record.AfterTokens,
		LifecycleContextWindow: record.ContextWindow,
	}
}

// canonicalizeToolSnapshotForDisplay makes a persisted tool snapshot displayable:
// the session stores the raw argument summary (agent.FormatToolArgs), while the
// live conversation stores the compact label SummarizeToolArgs derives from that
// same string. Rows label themselves from the view's ToolArgs, so hydration must
// run the identical helper; otherwise a resumed session prints raw arguments
// ("pattern: func main path: tui/tools") where the live row printed the label
// ("func main" in tui/tools).
func canonicalizeToolSnapshotForDisplay(snapshot *ChatMessageView) {
	if snapshot == nil || snapshot.Role != "tool" || snapshot.ToolArgs == "" {
		return
	}
	if tools.RetainsStructuredToolInput(snapshot.ToolName) && snapshot.ToolInput == "" {
		snapshot.ToolInput = snapshot.ToolArgs
	}
	snapshot.ToolArgs = SummarizeToolArgs(snapshot.ToolName, snapshot.ToolArgs)
}

// ConvertMessagesForDisplay converts persisted backend messages into the
// workspace-owned view data consumed by frontends.
func ConvertMessagesForDisplay(msgs []sessiondb.MessageWithMeta) []ChatMessageView {
	var result []ChatMessageView

	batchArgsByCallID := parallelBatchArguments(msgs)
	retained := retainedAttachmentIdentities(msgs)

	for _, mm := range msgs {
		if mm.Meta.SnapshotVersion > 0 {
			for _, snapshot := range mm.Meta.Snapshots {
				if snapshot.ToolName == "multi_tool_use.parallel" && snapshot.ToolInput == "" {
					snapshot.ToolInput = batchArgsByCallID[snapshot.ToolID]
				}
				snapshot.ReferenceLabels = append([]string(nil), snapshot.ReferenceLabels...)
				snapshot.Attachments = slices.Clone(snapshot.Attachments)
				if snapshot.Role == ttypes.RoleUser && len(snapshot.Attachments) == 0 {
					var exact []attachment.Target
					for _, target := range retained[snapshot.InternalID] {
						if slices.Contains(snapshot.ReferenceLabels, target.Label) && strings.Count(snapshot.Content, target.Label) == 1 {
							exact = append(exact, target)
						}
					}
					snapshot.Attachments = attachment.Locate(snapshot.Content, exact)
				}
				canonicalizeToolSnapshotForDisplay(&snapshot)
				result = append(result, snapshot)
			}
		}
		// Rows without a canonical snapshot are provider-context envelopes;
		// they never enter the visible transcript or its geometry.
	}

	return result
}

// retainedAttachmentIdentities reads only the exact input-owned preparation
// envelope. Each text part starts with the producer's reference header; payload
// prose (including forged nested headers) is never searched for identities.
func retainedAttachmentIdentities(msgs []sessiondb.MessageWithMeta) map[string][]attachment.Target {
	out := make(map[string][]attachment.Target)
	for _, mm := range msgs {
		m := mm.Msg
		if m.Role != "user" || m.EffectiveOrigin() != provider.OriginRuntimeContext || m.EffectiveVisibility() != provider.VisibilityContextOnly || !strings.HasSuffix(m.InternalID, "-references") {
			continue
		}
		inputID := strings.TrimSuffix(m.InternalID, "-references")
		for _, part := range m.Content.Parts() {
			if part.Type != "text" || !strings.HasPrefix(part.Text, "Reference [") {
				continue
			}
			header, _, _ := strings.Cut(part.Text, "\n")
			name, suffix, ok := strings.Cut(strings.TrimPrefix(header, "Reference "), "] (")
			if !ok {
				continue
			}
			id, _, ok := strings.Cut(suffix, "):")
			if !ok || id == "" || strings.ContainsAny(id, " \t\n\r()") {
				continue
			}
			out[inputID] = append(out[inputID], attachment.Target{ReferenceID: id, Label: name + "]"})
		}
	}
	return out
}

func parallelBatchArguments(msgs []sessiondb.MessageWithMeta) map[string]string {
	batchArgs := make(map[string]string)
	for _, mm := range msgs {
		for _, tc := range mm.Msg.ToolCalls {
			if mm.Msg.Role == "assistant" && tc.ID != "" && tc.Function.Name == "multi_tool_use.parallel" {
				batchArgs[tc.ID] = tc.Function.Arguments
			}
		}
	}
	return batchArgs
}

func nullStr(ns *sql.NullString) string {
	if ns == nil || !ns.Valid {
		return ""
	}
	return ns.String
}
