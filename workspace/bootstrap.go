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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if err := w.ensureWorkspacePersistence(); err != nil {
		return nil, fmt.Errorf("workspace persistence unavailable: %w", err)
	}
	// The backend session generates its own ID and directory under the
	// workspace sessions folder. We create the parent dir first so the
	// logger can land there.
	tmpDir, err := SessionDir(w.RootPath, "tmp")
	if err != nil {
		return nil, fmt.Errorf("session dir: %w", err)
	}
	// Remove the temporary placeholder.
	_ = os.RemoveAll(tmpDir)

	// Create the backend session. It will generate a session ID and place
	// its artifacts under the workspace sessions root.
	sessionsRoot := filepath.Join(w.WorkspaceDir, "sessions")
	bs, err := session.NewWithSessionDir("", cfg, homeDir, sessionsRoot)
	if err != nil {
		return nil, fmt.Errorf("new backend session: %w", err)
	}
	// Tools execute inside this workspace's project root. Every later
	// relative path and command cwd resolves against w.RootPath.
	bs.SetWorkDir(w.RootPath)
	if w.WorkspaceDB != nil {
		bs.SetMemoryStore(w.WorkspaceDB.Memory)
		bs.StartMemoryMaintenance()
	}
	w.bindSessionHistory(bs)
	if err := w.bindSessionRuntime(bs); err != nil {
		bs.Close()
		return nil, err
	}

	fs := NewSession(bs.SessionID(), bs.SessionDir(), bs.DB(), w.Logger)
	fs.attachBackend(bs)
	fs.SessionDir = bs.SessionDir()
	fs.Settings = NewSessionSettingsState(
		bs.SettingsSnapshot(),
		bs.WorkspaceSettingsSnapshot(),
		bs.SessionOverridesSnapshot(),
	)

	w.addSession(fs)
	w.deliverPendingHistoryNotifications(fs.SessionID)

	if w.WorkspaceDB != nil {
		if err := w.WorkspaceDB.SetAgentName(fs.SessionID, bs.AgentName()); err != nil {
			w.noteRegistryErr("agent_name_register_failed", err)
		}
		model := bs.ModelName()
		providerID := bs.SettingsSnapshot().ProviderID
		projectName := filepath.Base(w.RootPath)
		if err := w.WorkspaceDB.RegisterSession(fs.SessionID, w.RootPath, projectName, model, providerID); err != nil {
			// Non-fatal: session DB exists; registry failure must not block create.
			w.noteRegistryErr("session_register_failed", err)
		} else {
			w.noteRegistryErr("", nil)
		}
	}

	return fs, nil
}

// ResumeSession loads an existing workspace-scoped session by ID.
func (w *Workspace) ResumeSession(sessionID string, cfg *session.Config, homeDir string) (*Session, error) {
	if err := w.ensureWorkspacePersistence(); err != nil {
		return nil, fmt.Errorf("workspace persistence unavailable: %w", err)
	}
	dbPath, err := SessionDBPath(w.RootPath, sessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve session db: %w", err)
	}

	sessionsRoot := filepath.Join(w.WorkspaceDir, "sessions")
	if _, statErr := os.Stat(dbPath); statErr != nil {
		return nil, fmt.Errorf("session database unavailable: %w", statErr)
	}

	bs, err := session.ResumeSessionWithDir(dbPath, cfg, homeDir, sessionsRoot)
	if err != nil {
		return nil, fmt.Errorf("resume backend session: %w", err)
	}
	// The current workspace root is authoritative for the resumed lifetime.
	bs.SetWorkDir(w.RootPath)
	if w.WorkspaceDB != nil {
		bs.SetMemoryStore(w.WorkspaceDB.Memory)
		bs.StartMemoryMaintenance()
	}
	w.bindSessionHistory(bs)
	if err := w.bindSessionRuntime(bs); err != nil {
		bs.Close()
		return nil, err
	}

	fs := NewSession(bs.SessionID(), bs.SessionDir(), bs.DB(), w.Logger)
	fs.attachBackend(bs)
	fs.SessionDir = bs.SessionDir()
	fs.Settings = NewSessionSettingsState(
		bs.SettingsSnapshot(),
		bs.WorkspaceSettingsSnapshot(),
		bs.SessionOverridesSnapshot(),
	)

	w.addSession(fs)
	w.deliverPendingHistoryNotifications(fs.SessionID)

	// Ensure the workspace-scoped session is registered in the local registry.
	if w.WorkspaceDB != nil {
		model := bs.ModelName()
		providerID := bs.SettingsSnapshot().ProviderID
		projectName := filepath.Base(w.RootPath)
		if err := w.WorkspaceDB.RegisterSession(fs.SessionID, w.RootPath, projectName, model, providerID); err != nil {
			// Non-fatal: resume still succeeds; surface via LastRegistryError.
			w.noteRegistryErr("session_register_failed", err)
		} else {
			w.noteRegistryErr("", nil)
		}
	}

	return fs, nil
}

// addSession registers a frontend session in the canonical collections.
func (w *Workspace) addSession(fs *Session) {
	w.Sessions = append(w.Sessions, fs)
	w.SessionsByID[fs.SessionID] = fs
	w.ActiveSession = fs
	// The header badge must show the session's real main-agent model once a
	// session is active, replacing the startup placeholder.
	if fs.backend != nil {
		w.ModelName = fs.backend.ModelName()
	}
}

// Hydrate loads conversation history from the active backend session into
// the active frontend session. Called after the TUI starts for resumed
// sessions.
func (w *Workspace) Hydrate() {
	if w.ActiveSession == nil || w.ActiveSession.backend == nil {
		return
	}
	msgs := w.ActiveSession.backend.MessagesWithMeta()
	views := ConvertMessagesForDisplay(msgs)
	views = w.hydrateRecoveryViews(views, w.ActiveSession)
	for _, record := range w.ActiveSession.backend.CompactionRecords("") {
		views = append(views, compactionRecordView(record))
	}
	if len(views) > 0 {
		slices.SortStableFunc(views, func(left, right ChatMessageView) int {
			return left.Time.Compare(right.Time)
		})
		w.ActiveSession.hydratedMessages = views
	}

	if db := w.ActiveSession.DB; db != nil {
		if records, err := sessiondb.ListUsageRecords(db); err == nil {
			w.ActiveSession.usageMu.Lock()
			w.ActiveSession.usageRecords = make(map[string]sessiondb.UsageRecord, len(records))
			w.ActiveSession.usageCache = make(map[usageCacheKey]ttypes.UsageSummary)
			w.ActiveSession.usageVersion++
			for _, record := range records {
				w.ActiveSession.usageRecords[record.RequestID] = record
			}
			w.ActiveSession.usageMu.Unlock()
		} else {
			w.Logger.Warn("WORKSPACE", "usage_hydrate_failed", "error", err.Error())
		}
		if refs, err := sessiondb.LoadPromptReferences(db); err == nil && len(refs) > 0 {
			w.ActiveSession.hydratedRefs = refs
		}
	}
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
	var hydrated []string
	snapshots := frontendSession.backend.CompletedSubAgentSnapshots()
	snapshots = append(snapshots, frontendSession.backend.PendingSubAgentSnapshots()...)
	for _, snapshot := range snapshots {
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
		paneViews := ConvertMessagesForDisplay(snapshot.Messages)
		for _, record := range snapshot.Compactions {
			paneViews = append(paneViews, compactionRecordView(record))
		}
		slices.SortStableFunc(paneViews, func(left, right ChatMessageView) int {
			return left.Time.Compare(right.Time)
		})
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
		node.Hidden = true
		hydrated = append(hydrated, id)
	}
	return hydrated
}

// ── Backend Session Accessors

// BackendSession returns the underlying backend session for the active
// frontend session.
func (w *Workspace) BackendSession() *session.Session {
	if w.ActiveSession != nil {
		return w.ActiveSession.backend
	}
	return nil
}

// HandleSettingsSave applies settings only to the active AI session.
// Workspace defaults are persisted separately through SaveAppSettings.
func (w *Workspace) HandleSettingsSave(s ttypes.Settings) error {
	if b := w.BackendSession(); b != nil {
		if err := b.HandleSettingsSave(s); err != nil {
			return err
		}
		if w.ActiveSession != nil && w.ActiveSession.Settings != nil {
			w.ActiveSession.Settings.SetInheritance(b.SettingsSnapshot(), b.WorkspaceSettingsSnapshot(), b.SessionOverridesSnapshot())
		}
		return nil
	}
	return nil
}

// HandleSettingsSaveForSession applies overrides to the session that opened
// the inline editor, even if the user switches tabs before the async save runs.
func (w *Workspace) HandleSettingsSaveForSession(sessionID string, settings ttypes.Settings) error {
	frontendSession := w.SessionsByID[sessionID]
	if frontendSession == nil || frontendSession.backend == nil {
		return fmt.Errorf("session %q is not active", sessionID)
	}
	if err := frontendSession.backend.HandleSettingsSave(settings); err != nil {
		return err
	}
	if frontendSession.Settings != nil {
		frontendSession.Settings.SetInheritance(frontendSession.backend.SettingsSnapshot(), frontendSession.backend.WorkspaceSettingsSnapshot(), frontendSession.backend.SessionOverridesSnapshot())
	}
	return nil
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

// HandleUndo undoes the last message.
func (w *Workspace) HandleUndo(cmd string) interface{} {
	agentID := ""
	if w.AgentMgr != nil {
		agentID = w.AgentMgr.ActiveID()
	}
	return w.HandleUndoForAgent(agentID, cmd)
}

// HandleUndoForAgent applies an undo to the session that owns agentID. The
// explicit owner keeps an async slash command from following a later tab
// switch into a different session.
func (w *Workspace) HandleUndoForAgent(agentID, cmd string) ttypes.UndoResultMsg {
	if sess := w.sessionForAgent(agentID); sess != nil && sess.backend != nil {
		return sess.backend.HandleUndo(cmd)
	}
	return ttypes.UndoResultMsg{Error: "no active session"}
}

// HandleCompact force-compacts the backend session's conversation context
// into a summary checkpoint.
func (w *Workspace) HandleCompact() interface{} {
	agentID := ""
	if w.AgentMgr != nil {
		agentID = w.AgentMgr.ActiveID()
	}
	return w.HandleCompactForAgent(agentID)
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

// SetYolo toggles auto-approve mode.
func (w *Workspace) SetYolo(enabled bool) {
	agentID := ""
	if w.AgentMgr != nil {
		agentID = w.AgentMgr.ActiveID()
	}
	w.SetYoloForAgent(agentID, enabled)
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

// HardCancel forcefully cancels all running agents.
func (w *Workspace) HardCancel() {
	if b := w.BackendSession(); b != nil {
		b.HardCancel()
	}
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
		SenderName:     "Human",
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
	if w == nil || w.Collaboration == nil || agentID == "" || input.InternalID == "" {
		return false
	}
	participant, err := w.Collaboration.Agent(context.Background(), agentID)
	if err != nil || participant.State != collaboration.AgentAvailable ||
		participant.Presence != collaboration.RuntimeAttached ||
		participant.CurrentRunID == "" {
		return false
	}
	run, err := w.Collaboration.Run(context.Background(), participant.CurrentRunID)
	if err != nil || run.AgentID != agentID || run.RuntimeEpoch != participant.RuntimeEpoch || run.State.Terminal() {
		return false
	}
	references := []collaboration.Reference{{Kind: "history_message", ID: input.InternalID}}
	for _, id := range input.ReferenceIDs {
		if id != "" {
			references = append(references, collaboration.Reference{Kind: "prompt_reference", ID: id})
		}
	}
	_, err = w.Collaboration.Publish(context.Background(), collaboration.PublishRequest{
		SenderID:       "human",
		SenderName:     "Human",
		IdempotencyKey: "human-steering:" + agentID + ":" + input.InternalID,
		RecipientIDs:   []string{agentID},
		Origin:         collaboration.OriginHuman,
		Kind:           collaboration.EventHumanSteering,
		Body:           input.Text,
		RequestID:      participant.CurrentRequestID,
		RunID:          participant.CurrentRunID,
		References:     references,
	})
	return err == nil
}

// MinimizeAgent dismisses an agent from the tab bar without stopping any
// backend work. The orchestrator keeps streaming, tool calls keep running,
// and the session stays owned by this agent — only the surfaced tab goes
// away. This is what closing a tab does; terminating is CloseAgent.
func (w *Workspace) MinimizeAgent(agentID string) {
	if w.AgentMgr == nil || w.AgentMgr.Get(agentID) == nil {
		return
	}
	w.AgentMgr.SetHidden(agentID, true)
}

// RestoreAgent brings a minimized agent back and makes it the active one.
func (w *Workspace) RestoreAgent(agentID string) {
	if w.AgentMgr == nil {
		return
	}
	node := w.AgentMgr.Get(agentID)
	if node == nil {
		return
	}
	// A child opened from Agents must be reachable even when its root is
	// minimized. Reveal only this path; sibling review tabs stay archived.
	for current := node; current != nil; current = current.Parent {
		w.AgentMgr.SetHidden(current.ID, false)
	}
	w.AgentMgr.SetActive(agentID)
	w.ActivateSessionByAgent(agentID)
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
		w.persistSessionRegistry(sess)
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
func (w *Workspace) ResolveQuestion(agentID, blockID string, answers, questions map[string]string, cancelled bool) bool {
	if cancelled {
		return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
			RequestID: blockID,
			Outcome:   ttypes.BlockUserCancelled,
		})
	}
	if len(answers) == 0 {
		return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
			RequestID: blockID,
			Outcome:   ttypes.BlockValidationError,
			Detail:    "no answers were provided",
		})
	}

	var response strings.Builder
	for i := 1; i <= len(answers); i++ {
		key := fmt.Sprintf("%d", i)
		if question := questions[key]; question != "" {
			fmt.Fprintf(&response, "Q%d: %s\nA%d: %s", i, question, i, answers[key])
		} else {
			fmt.Fprintf(&response, "A%d: %s", i, answers[key])
		}
		if i < len(answers) {
			response.WriteString("\n\n")
		}
	}
	return w.resolveAgentBlock(agentID, ttypes.BlockResponse{
		RequestID: blockID,
		Outcome:   ttypes.BlockAnswered,
		Value:     response.String(),
	})
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
	return sess.backend.ResolveBlock(agentID, response)
}

func (w *Workspace) sessionForAgent(agentID string) *Session {
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

// ResetOrchestratorOverrides clears runtime overrides for an orchestrator.
func (w *Workspace) ResetOrchestratorOverrides(orchID string) {
	b := w.BackendSession()
	if b == nil {
		return
	}
	b.RuntimeProfile().RemoveOverride(orchID)
	w.adoptCurrentDefaults(b)
	if err := b.ResetSessionSettings(); err != nil {
		w.Logger.Warn("RUNTIME", "session_settings_reset_failed", "error", err.Error())
	} else if w.ActiveSession != nil && w.ActiveSession.Settings != nil {
		w.ActiveSession.Settings.ResetToWorkspace(b.WorkspaceSettingsSnapshot())
	}
	w.Logger.Info("RUNTIME", "orchestrator_reset", "id", orchID)
}

// adoptCurrentDefaults re-inherits today's effective agent defaults into a
// live session. Sessions snapshot their agent settings at creation, so this
// is the only way a running session picks up later workspace edits — and it
// runs exclusively from user-invoked resets.
func (w *Workspace) adoptCurrentDefaults(b *session.Session) {
	if b == nil {
		return
	}
	effective := w.EffectiveAppSettings()
	b.AdoptWorkspaceDefaults(ttypes.AppSettings{
		Agents:   effective.Agents,
		Accounts: effective.Accounts,
	})
}

// ResetSessionSettings clears the active session's DB-backed overrides and
// re-inherits the current workspace defaults.
func (w *Workspace) ResetSessionSettings() error {
	b := w.BackendSession()
	if b == nil {
		return fmt.Errorf("no active session")
	}
	w.adoptCurrentDefaults(b)
	if err := b.ResetSessionSettings(); err != nil {
		return err
	}
	if w.ActiveSession != nil && w.ActiveSession.Settings != nil {
		w.ActiveSession.Settings.ResetToWorkspace(b.WorkspaceSettingsSnapshot())
	}
	return nil
}

// ResetSessionSettingsForSession clears the persisted overrides owned by a
// specific frontend session without depending on which tab is currently active.
func (w *Workspace) ResetSessionSettingsForSession(sessionID string) error {
	frontendSession := w.SessionsByID[sessionID]
	if frontendSession == nil || frontendSession.backend == nil {
		return fmt.Errorf("session %q is not active", sessionID)
	}
	w.adoptCurrentDefaults(frontendSession.backend)
	if err := frontendSession.backend.ResetSessionSettings(); err != nil {
		return err
	}
	if frontendSession.Settings != nil {
		frontendSession.Settings.ResetToWorkspace(frontendSession.backend.WorkspaceSettingsSnapshot())
	}
	return nil
}

// BackendModelName returns the display name of the active model.
func (w *Workspace) BackendModelName() string {
	if b := w.BackendSession(); b != nil {
		return b.ModelName()
	}
	return ""
}

// CloseSession closes the active backend session.
func (w *Workspace) CloseSession() {
	if b := w.BackendSession(); b != nil {
		b.Close()
	}
}

// SessionHasMessages returns true if the active backend session has messages.
func (w *Workspace) SessionHasMessages() bool {
	if b := w.BackendSession(); b != nil {
		return b.HasMessages()
	}
	return false
}

// SessionID returns the active backend session's database ID.
func (w *Workspace) SessionID() string {
	if b := w.BackendSession(); b != nil {
		return b.SessionID()
	}
	return ""
}

// SessionStart returns when the active backend session was created.
func (w *Workspace) SessionStart() time.Time {
	if b := w.BackendSession(); b != nil {
		return b.SessionStart()
	}
	return time.Time{}
}

// CostSnapshot returns the active session's cost accumulation snapshot.
func (w *Workspace) CostSnapshot() session.CostSnapshot {
	if b := w.BackendSession(); b != nil {
		return b.CostSnapshot()
	}
	return session.CostSnapshot{}
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
	// DB is present — clear any prior missing mark (reversible).
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

// StartOrchestrator starts the backend orchestrator for the active session.
// Returns the orchestrator's channels, or an error.
func (w *Workspace) StartOrchestrator(sender session.EventSender) (*session.Orchestrator, error) {
	b := w.BackendSession()
	if b == nil {
		return nil, fmt.Errorf("no active backend session")
	}
	orch, err := b.NewOrchestrator(sender)
	if err == nil && orch != nil && w.ActiveSession != nil {
		w.ActiveSession.AgentID = orch.ID
		if w.SessionsByAgent == nil {
			w.SessionsByAgent = make(map[string]*Session)
		}
		w.SessionsByAgent[orch.ID] = w.ActiveSession
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
// run the identical helper — otherwise a resumed session prints raw arguments
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

	toolNameByCallID, batchArgsByCallID := buildToolNameIndex(msgs)

	for _, mm := range msgs {
		if mm.Meta.SnapshotVersion > 0 {
			for _, snapshot := range mm.Meta.Snapshots {
				if snapshot.ToolName == "multi_tool_use.parallel" && snapshot.ToolInput == "" {
					snapshot.ToolInput = batchArgsByCallID[snapshot.ToolID]
				}
				snapshot.ReferenceLabels = append([]string(nil), snapshot.ReferenceLabels...)
				canonicalizeToolSnapshotForDisplay(&snapshot)
				result = append(result, snapshot)
			}
			continue
		}
		// Explicit snapshots above may expose a context boundary. The provider
		// envelope itself must never enter the visible transcript or its geometry.
		if mm.Meta.ContextOnly || mm.Msg.EffectiveVisibility() == provider.VisibilityContextOnly {
			continue
		}
		role := mm.Meta.DisplayRole
		if role == "" {
			role = mm.Msg.Role
		}

		msgTime := time.Now()
		if mm.CreatedAt > 0 {
			msgTime = time.Unix(0, mm.CreatedAt)
		}

		switch role {
		case "user":
			content := mm.Meta.DisplayContent
			if content == "" {
				content = userMessageDisplayContent(mm.Msg.Content)
			}
			result = append(result, ChatMessageView{
				Role:            "user",
				Content:         content,
				ReferenceLabels: append([]string(nil), mm.Meta.ReferenceLabels...),
				Time:            msgTime,
			})

		case "assistant":
			result = append(result, ChatMessageView{
				Role: "assistant", Content: mm.Msg.Content.String(), Time: msgTime,
				TurnGeneration: mm.Meta.TurnGeneration,
				AccountID:      mm.Meta.AccountID,
				ProviderID:     mm.Meta.ProviderID, ModelID: mm.Meta.ModelID,
			})

		case "thinking":
			var thinkingDuration time.Duration
			if mm.Meta.DurationMs > 0 {
				thinkingDuration = time.Duration(mm.Meta.DurationMs) * time.Millisecond
			}
			result = append(result, ChatMessageView{
				Role:             "thinking",
				Content:          mm.Msg.ReasoningContent,
				Time:             msgTime,
				ThinkingDuration: thinkingDuration,
				ThinkingDisplay:  mm.Msg.ReasoningDisplay,
				TurnGeneration:   mm.Meta.TurnGeneration,
				AccountID:        mm.Meta.AccountID,
				ProviderID:       mm.Meta.ProviderID,
				ModelID:          mm.Meta.ModelID,
			})
			if mm.Msg.Content.String() != "" {
				result = append(result, ChatMessageView{
					Role: "assistant", Content: mm.Msg.Content.String(), Time: msgTime,
					TurnGeneration: mm.Meta.TurnGeneration,
					AccountID:      mm.Meta.AccountID,
					ProviderID:     mm.Meta.ProviderID, ModelID: mm.Meta.ModelID,
				})
			}

		case "tool":
			toolName := mm.Meta.ToolName
			toolArgs := mm.Meta.ToolArgs
			toolStatus := mm.Meta.ToolStatus
			if toolStatus == "" {
				toolStatus = "success"
			}
			if toolName == "" && mm.Msg.ToolCallID != "" {
				if name, ok := toolNameByCallID[mm.Msg.ToolCallID]; ok {
					toolName = name
				}
			}

			argsCompact := SummarizeToolArgs(toolName, toolArgs)
			content := mm.Msg.Content.String()
			if content == "" {
				content = toolName
				if argsCompact != "" {
					content = toolName + " " + argsCompact
				}
			}

			chatMsg := ChatMessageView{
				Role:              "tool",
				Content:           content,
				Time:              msgTime,
				ToolName:          toolName,
				ToolID:            mm.Msg.ToolCallID,
				ToolArgs:          argsCompact,
				ToolCommand:       ExtractArg(toolArgs, "command"),
				ToolInput:         persistedToolInput(toolName, toolArgs),
				ToolScope:         persistedToolScope(toolName, toolArgs),
				ToolStatus:        toolStatus,
				ToolProgressLabel: mm.Meta.ProgressLabel,
				PermTag:           mm.Meta.PermTag,
				ToolPreviewPhase:  "done",
				TurnGeneration:    mm.Meta.TurnGeneration,
				AccountID:         mm.Meta.AccountID,
				ProviderID:        mm.Meta.ProviderID,
				ModelID:           mm.Meta.ModelID,
			}
			if toolName == "multi_tool_use.parallel" && chatMsg.ToolInput == "" {
				chatMsg.ToolInput = batchArgsByCallID[mm.Msg.ToolCallID]
			}

			if mm.Meta.DurationMs > 0 {
				chatMsg.ToolDuration = time.Duration(mm.Meta.DurationMs) * time.Millisecond
			}
			if mm.Meta.EstTokens > 0 {
				chatMsg.ToolEstTokens = mm.Meta.EstTokens
			}
			if mm.Meta.LinesAdded > 0 || mm.Meta.LinesRemoved > 0 {
				chatMsg.ToolLinesAdded = mm.Meta.LinesAdded
				chatMsg.ToolLinesRemoved = mm.Meta.LinesRemoved
			}
			if mm.Meta.SubPaneID != "" {
				chatMsg.SubPaneID = mm.Meta.SubPaneID
			} else if toolName == "agent_start" {
				chatMsg.SubPaneID = persistedSubAgentID(content)
			}

			result = append(result, chatMsg)

		default:
			if mm.Msg.Content.String() != "" {
				result = append(result, ChatMessageView{
					Role:    role,
					Content: mm.Msg.Content.String(),
					Time:    msgTime,
				})
			}
		}
	}

	return result
}

func persistedSubAgentID(content string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) == nil {
		if value, _ := payload["agent_id"].(string); value != "" {
			return value
		}
	}
	return ""
}

func persistedToolInput(toolName, args string) string {
	if tools.RetainsStructuredToolInput(toolName) {
		return args
	}
	switch toolName {
	case "multi_tool_use.parallel":
		return args
	case "grep_files":
		return ExtractArg(args, "pattern")
	case "web_search", "arxiv_search":
		return ExtractArg(args, "query")
	case "web_fetch":
		return ExtractArg(args, "url")
	}
	return ""
}

func persistedToolScope(toolName, args string) string {
	if toolName == "grep_files" {
		return ExtractArg(args, "path")
	}
	return ""
}

// userMessageDisplayContent keeps hydration identical to the live composer.
// Multipart payload parts contain expanded files/images for the provider and
// must never be dumped into the visible user message on resume.
func userMessageDisplayContent(content provider.MessageContent) string {
	if content.IsText() {
		return content.String()
	}
	for _, part := range content.Parts() {
		if part.Type == "text" && part.Text != "" {
			return part.Text
		}
	}
	return content.ContentDescription()
}

func buildToolNameIndex(msgs []sessiondb.MessageWithMeta) (map[string]string, map[string]string) {
	index := make(map[string]string)
	batchArgs := make(map[string]string)
	for _, mm := range msgs {
		if mm.Msg.Role == "assistant" && len(mm.Msg.ToolCalls) > 0 {
			for _, tc := range mm.Msg.ToolCalls {
				if tc.ID != "" && tc.Function.Name != "" {
					index[tc.ID] = tc.Function.Name
					if tc.Function.Name == "multi_tool_use.parallel" {
						batchArgs[tc.ID] = tc.Function.Arguments
					}
				}
			}
		}
	}
	return index, batchArgs
}

func nullStr(ns *sql.NullString) string {
	if ns == nil || !ns.Valid {
		return ""
	}
	return ns.String
}
