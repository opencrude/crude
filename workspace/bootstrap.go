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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crude/provider"
	"crude/session"
	"crude/sessiondb"
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
			w.Logger.Info("WORKSPACE", "fallback_new_session")
			fs, err = w.CreateSession(cfg, homeDir)
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
	if w.WorkspaceDB != nil {
		bs.SetMemoryStore(w.WorkspaceDB.Memory)
		bs.StartMemoryMaintenance()
	}

	fs := NewSession(bs.SessionID(), bs.SessionDir(), bs.DB(), w.Logger)
	fs.backend = bs
	fs.SessionDir = bs.SessionDir()
	fs.Settings = NewSessionSettingsState(
		bs.SettingsSnapshot(),
		bs.WorkspaceSettingsSnapshot(),
		bs.SessionOverridesSnapshot(),
	)

	w.addSession(fs)

	if w.WorkspaceDB != nil {
		model := bs.ModelName()
		providerID := cfg.EffectiveProviderID()
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

// ResumeSession loads an existing session by ID from this workspace.
// Falls back to the legacy ~/.crude/sessions/ layout when the session
// DB is not found under the workspace folder.
func (w *Workspace) ResumeSession(sessionID string, cfg *session.Config, homeDir string) (*Session, error) {
	dbPath, err := SessionDBPath(w.RootPath, sessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve session db: %w", err)
	}

	sessionsRoot := filepath.Join(w.WorkspaceDir, "sessions")
	if _, statErr := os.Stat(dbPath); statErr != nil {
		// Legacy fallback: ~/.crude/sessions/<id>.db
		if legacyRoot, legErr := session.SessionsRoot(); legErr == nil {
			legacyPath := filepath.Join(legacyRoot, sessionID+".db")
			if _, legStat := os.Stat(legacyPath); legStat == nil {
				w.Logger.Info("WORKSPACE", "resume_legacy_fallback", "id", sessionID)
				dbPath = legacyPath
				sessionsRoot = legacyRoot
			}
		}
	}

	bs, err := session.ResumeSessionWithDir(dbPath, cfg, homeDir, sessionsRoot)
	if err != nil {
		return nil, fmt.Errorf("resume backend session: %w", err)
	}
	if w.WorkspaceDB != nil {
		bs.SetMemoryStore(w.WorkspaceDB.Memory)
		bs.StartMemoryMaintenance()
	}

	fs := NewSession(bs.SessionID(), bs.SessionDir(), bs.DB(), w.Logger)
	fs.backend = bs
	fs.SessionDir = bs.SessionDir()
	fs.Settings = NewSessionSettingsState(
		bs.SettingsSnapshot(),
		bs.WorkspaceSettingsSnapshot(),
		bs.SessionOverridesSnapshot(),
	)

	w.addSession(fs)

	// Ensure the session is registered in the workspace registry (import legacy).
	if w.WorkspaceDB != nil {
		model := bs.ModelName()
		providerID := cfg.EffectiveProviderID()
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
	if len(msgs) > 0 {
		w.ActiveSession.hydratedMessages = ConvertMessagesForDisplay(msgs)
	}

	if db := w.ActiveSession.DB; db != nil {
		if refs, err := sessiondb.LoadPromptReferences(db); err == nil && len(refs) > 0 {
			w.ActiveSession.hydratedRefs = refs
		}
	}
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
			w.ActiveSession.Settings.Effective = s
			w.ActiveSession.Settings.Overrides = copyCategoryMap(s.SessionOverrides)
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
		frontendSession.Settings.Effective = settings
		frontendSession.Settings.Overrides = copyCategoryMap(settings.SessionOverrides)
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

// AllProviderConfigs returns a copy of the provider map from the live session config.
func (w *Workspace) AllProviderConfigs() map[string]ttypes.ProviderConfig {
	if w.sessionCfg == nil || w.sessionCfg.Providers == nil {
		return nil
	}
	out := make(map[string]ttypes.ProviderConfig, len(w.sessionCfg.Providers))
	for k, v := range w.sessionCfg.Providers {
		out[k] = ttypes.ProviderConfig{
			APIKey: v.APIKey, ProviderURL: v.ProviderURL,
			APIStandard: v.APIStandard, Model: v.Model,
			ModelStandards: copyStringMap(v.ModelStandards),
			ThinkingEffort: v.ThinkingEffort,
			OAuth:          cloneSessionOAuth(v.OAuth),
			Dialer:         cloneDialSettings(v.Dialer),
			Protocol:       cloneProtocolSettings(v.Protocol),
			ExtraBody:      copyExtraBody(v.ExtraBody),
		}
	}
	return out
}

func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

// cloneSessionOAuth returns a shallow copy of OAuth credentials. All fields are
// value types, so a struct copy is enough to keep the session config from
// aliasing the UI provider map.
func cloneSessionOAuth(src *provider.OAuthCredentials) *provider.OAuthCredentials {
	if src == nil {
		return nil
	}
	dup := *src
	return &dup
}

// cloneDialSettings copies user-tunable transport settings so neither side of
// the session/UI boundary can mutate the other's copy.
func cloneDialSettings(src *provider.DialSettings) *provider.DialSettings {
	if src == nil {
		return nil
	}
	dup := *src
	return &dup
}

// copyExtraBody copies a raw extra-body map so neither side of the session/UI
// boundary can mutate the other's copy. Values are JSON-decoded and never
// mutated in place, so a top-level copy suffices.
func copyExtraBody(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

// cloneProtocolSettings copies the protocol-level request switches so neither
// side of the session/UI boundary can mutate the other's copy.
func cloneProtocolSettings(src *provider.ProtocolSettings) *provider.ProtocolSettings {
	if src == nil {
		return nil
	}
	dup := *src
	if src.Store != nil {
		store := *src.Store
		dup.Store = &store
	}
	if src.ReasoningSummaries != nil {
		summaries := *src.ReasoningSummaries
		dup.ReasoningSummaries = &summaries
	}
	if src.Truncation != nil {
		truncation := *src.Truncation
		dup.Truncation = &truncation
	}
	return &dup
}

// SaveProviderSettings updates the live session config and persists via OnConfigSave.
// Category-level keys resolve from Config.Providers via resolveCategoryDefaults.
func (w *Workspace) SaveProviderSettings(active string, providers map[string]ttypes.ProviderConfig) error {
	if w.sessionCfg == nil {
		return fmt.Errorf("no session config")
	}
	if active != "" {
		w.sessionCfg.ActiveProvider = active
	}
	if w.sessionCfg.Providers == nil {
		w.sessionCfg.Providers = make(map[string]session.ProviderConfig)
	}
	for id, pc := range providers {
		w.sessionCfg.Providers[id] = session.ProviderConfig{
			APIKey:         pc.APIKey,
			ProviderURL:    pc.ProviderURL,
			APIStandard:    pc.APIStandard,
			Model:          pc.Model,
			ModelStandards: copyStringMap(pc.ModelStandards),
			ThinkingEffort: pc.ThinkingEffort,
			OAuth:          cloneSessionOAuth(pc.OAuth),
			Dialer:         cloneDialSettings(pc.Dialer),
			Protocol:       cloneProtocolSettings(pc.Protocol),
			ExtraBody:      copyExtraBody(pc.ExtraBody),
		}
	}
	for _, frontendSession := range w.Sessions {
		if frontendSession != nil && frontendSession.backend != nil {
			frontendSession.backend.UpdateWorkspaceDefaults(ttypes.AppSettings{
				ActiveProvider: active, Providers: providers,
			})
			if frontendSession.Settings != nil {
				frontendSession.Settings.SetInheritance(
					frontendSession.backend.SettingsSnapshot(),
					frontendSession.backend.WorkspaceSettingsSnapshot(),
					frontendSession.backend.SessionOverridesSnapshot(),
				)
			}
		}
	}
	if w.OnConfigSave != nil {
		return w.OnConfigSave(w.sessionCfg)
	}
	return nil
}

// HandleFetchModels fetches available models from a provider.
func (w *Workspace) HandleFetchModels(providerID, baseURL, apiKey, standardID string) ([]ttypes.ModelEntry, error) {
	if b := w.BackendSession(); b != nil {
		return b.HandleFetchModels(providerID, baseURL, apiKey, standardID)
	}
	p := provider.FromPreset(providerID, apiKey, baseURL)
	if standardID != "" {
		if err := p.SetProtocolID(standardID); err != nil {
			return nil, err
		}
	}
	// Attach stored OAuth credentials (e.g. ChatGPT subscription) and
	// transport/protocol tuning so the listing request is authenticated and
	// transported exactly like chat traffic.
	if w.sessionCfg != nil {
		if pc, ok := w.sessionCfg.Providers[providerID]; ok {
			if pc.OAuth != nil {
				p.SetOAuth(pc.OAuth)
			}
			if pc.Dialer != nil {
				p.SetDialSettings(*pc.Dialer)
			}
			if pc.Protocol != nil {
				p.SetProtocolSettings(pc.Protocol)
			}
			if len(pc.ExtraBody) > 0 {
				p.SetExtraBody(pc.ExtraBody)
			}
		}
	}
	client, err := p.Client()
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		return nil, err
	}
	return ttypes.ModelEntriesFromInfos(models), nil
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
	if sess := w.sessionForAgent(agentID); sess != nil && sess.backend != nil {
		return sess.backend.HandleCompact()
	}
	return ttypes.CompactResultMsg{Error: "no active session"}
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
		_, _ = runner.CancelSubAgent(map[string]any{"handle_id": agentID})
	}
}

// CancelAgent routes cancellation to the orchestrator or sub-agent that owns
// agentID, independent of which session is currently visible.
func (w *Workspace) CancelAgent(agentID string) {
	node := w.AgentMgr.Get(agentID)
	if node == nil {
		return
	}
	if node.Parent != nil {
		w.CancelSubAgent(agentID)
		return
	}
	if node.CancelChan != nil {
		select {
		case node.CancelChan <- struct{}{}:
		default:
		}
	}
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
	if w.AgentMgr == nil || w.AgentMgr.Get(agentID) == nil {
		return
	}
	w.AgentMgr.SetHidden(agentID, false)
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

// ResolveConfirmation sends a permission response to the owning orchestrator.
func (w *Workspace) ResolveConfirmation(agentID, choice string) bool {
	switch choice {
	case "allow", "always", "deny":
	default:
		choice = "deny"
	}
	return w.resolveAgentBlock(agentID, choice)
}

// ResolveQuestion sends formatted question answers to the owning orchestrator.
func (w *Workspace) ResolveQuestion(agentID string, answers, questions map[string]string, cancelled bool) bool {
	if cancelled {
		return w.resolveAgentBlock(agentID, "cancelled")
	}
	if len(answers) == 0 {
		return w.resolveAgentBlock(agentID, "No answers provided")
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
	return w.resolveAgentBlock(agentID, response.String())
}

func (w *Workspace) resolveAgentBlock(agentID, response string) bool {
	root := w.AgentMgr.OrchestratorFor(agentID)
	if root == nil || root.BlockResolveCh == nil {
		return false
	}
	select {
	case root.BlockResolveCh <- response:
		return true
	default:
		// A buffered response can be stale after cancellation or panel
		// replacement. Prefer the user's latest explicit answer.
		select {
		case <-root.BlockResolveCh:
		default:
		}
		select {
		case root.BlockResolveCh <- response:
			return true
		default:
			return false
		}
	}
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

// UsageSnapshot is the workspace-owned token/cost state for one session.
type UsageSnapshot struct {
	Prompt        int
	Completion    int
	Cached        int
	Cost          float64
	ContextWindow int
	ContextUsed   int
	Estimated     bool
}

// RecordAgentUsage applies a provider usage event to its owning session rather
// than whichever session happens to be visible.
//
// After updating the in-memory session cost, the cumulative cost is pushed
// live to the workspace_sessions registry (the push path of Phase 2's
// "UpdateSessionMeta for live updates"). The heavier full-row sync
// (turn_count, message_count, prompts) happens at session close via
// SyncSessionMeta — those fields are owned by the session DB and would
// require re-opening it to read here.
func (w *Workspace) RecordAgentUsage(agentID string, data any) (UsageSnapshot, bool) {
	sess := w.sessionForAgent(agentID)
	if sess == nil {
		return UsageSnapshot{}, false
	}
	switch usage := data.(type) {
	case map[string]any:
		sess.TokenPrompt = toInt(usage["prompt"])
		sess.TokenCompletion = toInt(usage["completion"])
		sess.TokenCached = toInt(usage["prompt_cached"])
		cost, _ := usage["cost"].(float64)
		sess.SessionCost += cost
		sess.TokenEstimated, _ = usage["estimated"].(bool)
		if contextWindow := toInt(usage["context_window"]); contextWindow > 0 {
			sess.ContextWindow = contextWindow
			contextUsed := toInt(usage["context_prompt"])
			if contextUsed == 0 {
				contextUsed = toInt(usage["context_used"])
			}
			if contextUsed > 0 {
				sess.ContextUsed = contextUsed
			} else {
				sess.ContextUsed = sess.TokenPrompt
			}
		}
	case map[string]int:
		sess.TokenPrompt = usage["prompt"]
		sess.TokenCompletion = usage["completion"]
		sess.TokenCached = usage["prompt_cached"]
		sess.TokenEstimated = false
		if contextWindow := usage["context_window"]; contextWindow > 0 {
			sess.ContextWindow = contextWindow
			sess.ContextUsed = usage["context_prompt"]
			if sess.ContextUsed == 0 {
				sess.ContextUsed = usage["context_used"]
			}
			if sess.ContextUsed == 0 {
				sess.ContextUsed = usage["prompt"]
			}
		}
	default:
		return UsageSnapshot{}, false
	}
	// Push cumulative cost to the workspace registry (live update).
	if w.WorkspaceDB != nil && sess.SessionID != "" {
		if err := w.WorkspaceDB.UpdateSessionCost(sess.SessionID, sess.SessionCost); err != nil {
			w.Logger.Warn("WORKSPACE", "session_cost_sync_failed",
				"id", sess.SessionID, "error", err.Error())
		}
	}
	return usageSnapshot(sess), true
}

// AgentUsage returns the latest usage state for the session owning agentID.
func (w *Workspace) AgentUsage(agentID string) (UsageSnapshot, bool) {
	sess := w.sessionForAgent(agentID)
	if sess == nil {
		return UsageSnapshot{}, false
	}
	return usageSnapshot(sess), true
}

func usageSnapshot(sess *Session) UsageSnapshot {
	return UsageSnapshot{
		Prompt:        sess.TokenPrompt,
		Completion:    sess.TokenCompletion,
		Cached:        sess.TokenCached,
		Cost:          sess.SessionCost,
		ContextWindow: sess.ContextWindow,
		ContextUsed:   sess.ContextUsed,
		Estimated:     sess.TokenEstimated,
	}
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
		Agents:         effective.Agents,
		ActiveProvider: effective.ActiveProvider,
		Providers:      effective.Providers,
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

// ProviderConfig returns the config for a given provider from the live
// session config (or active backend session).
func (w *Workspace) ProviderConfig(providerID string) ttypes.ProviderConfig {
	if w.sessionCfg != nil {
		if cfg, ok := w.sessionCfg.Providers[providerID]; ok {
			return ttypes.ProviderConfig{
				APIKey: cfg.APIKey, ProviderURL: cfg.ProviderURL,
				APIStandard: cfg.APIStandard, Model: cfg.Model,
				ModelStandards: copyStringMap(cfg.ModelStandards),
				ThinkingEffort: cfg.ThinkingEffort,
				OAuth:          cloneSessionOAuth(cfg.OAuth),
				Dialer:         cloneDialSettings(cfg.Dialer),
				Protocol:       cloneProtocolSettings(cfg.Protocol),
				ExtraBody:      copyExtraBody(cfg.ExtraBody),
			}
		}
	}
	b := w.BackendSession()
	if b == nil {
		return ttypes.ProviderConfig{}
	}
	if cfg, ok := b.Config().Providers[providerID]; ok {
		return ttypes.ProviderConfig{
			APIKey: cfg.APIKey, ProviderURL: cfg.ProviderURL,
			APIStandard: cfg.APIStandard, Model: cfg.Model,
			ModelStandards: copyStringMap(cfg.ModelStandards),
			ThinkingEffort: cfg.ThinkingEffort,
			OAuth:          cloneSessionOAuth(cfg.OAuth),
			Dialer:         cloneDialSettings(cfg.Dialer),
			Protocol:       cloneProtocolSettings(cfg.Protocol),
			ExtraBody:      copyExtraBody(cfg.ExtraBody),
		}
	}
	return ttypes.ProviderConfig{}
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

// enrichSessionMeta fills in fields that live in the session database.
// Falls back to the legacy ~/.crude/sessions/ layout when the workspace-
// local DB is missing.
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
		// Legacy fallback before marking the registry entry unavailable.
		if legacyRoot, legErr := session.SessionsRoot(); legErr == nil {
			legacyPath := filepath.Join(legacyRoot, r.ID+".db")
			if _, legStat := os.Stat(legacyPath); legStat == nil {
				dbPath = legacyPath
			} else {
				_ = w.WorkspaceDB.MarkSessionDBMissing(r.ID, true)
				w.Logger.Warn("WORKSPACE", "session_db_missing",
					"id", r.ID, "path", dbPath)
				return sessiondb.SessionMeta{}
			}
		} else {
			_ = w.WorkspaceDB.MarkSessionDBMissing(r.ID, true)
			w.Logger.Warn("WORKSPACE", "session_db_missing",
				"id", r.ID, "path", dbPath)
			return sessiondb.SessionMeta{}
		}
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

// ConvertMessagesForDisplay converts persisted backend messages into the
// workspace-owned view data consumed by frontends.
func ConvertMessagesForDisplay(msgs []sessiondb.MessageWithMeta) []ChatMessageView {
	var result []ChatMessageView

	toolNameByCallID := buildToolNameIndex(msgs)

	for _, mm := range msgs {
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
				Role:    "assistant",
				Content: mm.Msg.Content.String(),
				Time:    msgTime,
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
			})
			if mm.Msg.Content.String() != "" {
				result = append(result, ChatMessageView{
					Role:    "assistant",
					Content: mm.Msg.Content.String(),
					Time:    msgTime,
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
				ToolCommand:       extractJSONStringValue(toolArgs, "command"),
				ToolInput:         persistedToolInput(toolName, toolArgs),
				ToolScope:         persistedToolScope(toolName, toolArgs),
				ToolStatus:        toolStatus,
				ToolProgressLabel: mm.Meta.ProgressLabel,
				PermTag:           mm.Meta.PermTag,
				ToolPreviewPhase:  "done",
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

func persistedToolInput(toolName, args string) string {
	switch toolName {
	case "grep_files":
		return extractJSONStringValue(args, "pattern")
	case "web_search", "arxiv_search":
		return extractJSONStringValue(args, "query")
	case "web_fetch":
		return extractJSONStringValue(args, "url")
	}
	return ""
}

func persistedToolScope(toolName, args string) string {
	if toolName == "grep_files" {
		return extractJSONStringValue(args, "path")
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

func buildToolNameIndex(msgs []sessiondb.MessageWithMeta) map[string]string {
	index := make(map[string]string)
	for _, mm := range msgs {
		if mm.Msg.Role == "assistant" && len(mm.Msg.ToolCalls) > 0 {
			for _, tc := range mm.Msg.ToolCalls {
				if tc.ID != "" && tc.Function.Name != "" {
					index[tc.ID] = tc.Function.Name
				}
			}
		}
	}
	return index
}

func nullStr(ns *sql.NullString) string {
	if ns == nil || !ns.Valid {
		return ""
	}
	return ns.String
}
