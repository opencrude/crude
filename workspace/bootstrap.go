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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/tui/tools"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
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

	fs := NewSession(w.Theme, bs.SessionID(), bs.SessionDir(), bs.DB(), w.Logger)
	fs.backend = bs
	fs.SessionDir = bs.SessionDir()
	fs.Settings = widgets.NewSessionSettingsWithInheritance(
		w.Theme,
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
			w.Logger.Warn("WORKSPACE", "session_register_failed", "error", err.Error())
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

	fs := NewSession(w.Theme, bs.SessionID(), bs.SessionDir(), bs.DB(), w.Logger)
	fs.backend = bs
	fs.SessionDir = bs.SessionDir()
	fs.Settings = widgets.NewSessionSettingsWithInheritance(
		w.Theme,
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
			w.Logger.Warn("WORKSPACE", "session_register_failed", "error", err.Error())
		}
	}

	return fs, nil
}

// addSession registers a frontend session in the canonical collections.
func (w *Workspace) addSession(fs *Session) {
	w.Sessions = append(w.Sessions, fs)
	w.SessionsByID[fs.SessionID] = fs
	w.ActiveSession = fs
	if fs.Settings != nil {
		w.SessionSettings = fs.Settings
	}
	w.Input.SetReferenceStore(fs.RefStore)
}

// Hydrate loads conversation history from the active backend session into
// the active frontend session. Called after the TUI starts for resumed
// sessions.
func (w *Workspace) Hydrate() {
	if w.ActiveSession == nil || w.ActiveSession.backend == nil {
		return
	}
	msgs := w.ActiveSession.backend.MessagesWithMeta()
	if len(msgs) == 0 {
		return
	}

	w.ActiveSession.hydratedMessages = ConvertMessagesForDisplay(msgs)

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
		return b.HandleSettingsSave(s)
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
	return frontendSession.backend.HandleSettingsSave(settings)
}

// SaveAppSettings persists visual/tooling settings (theme, editor, LSP, UI, font)
// to workspace.db without touching the backend session. Provider credentials are
// saved separately via SaveProviderSettings.
func (w *Workspace) SaveAppSettings(s ttypes.AppSettings) error {
	// Provider credentials live in config.json. Agent defaults are scoped to
	// this workspace and stay in workspace.db with the IDE preferences.
	visual := ttypes.AppSettings{
		Theme:  s.Theme,
		Editor: s.Editor,
		LSP:    s.LSP,
		UI:     s.UI,
		Font:   s.Font,
		Agents: s.Agents,
	}
	if w.sessionCfg != nil {
		session.ApplyWorkspaceAgentDefaults(w.sessionCfg, s.Agents)
	}
	for _, frontendSession := range w.Sessions {
		if frontendSession != nil && frontendSession.backend != nil {
			frontendSession.backend.UpdateWorkspaceDefaults(ttypes.AppSettings{Agents: s.Agents})
			if frontendSession.Settings != nil {
				frontendSession.Settings.SetInheritance(
					frontendSession.backend.SettingsSnapshot(),
					frontendSession.backend.WorkspaceSettingsSnapshot(),
					frontendSession.backend.SessionOverridesSnapshot(),
				)
			}
		}
	}
	if w.WorkspaceDB != nil {
		data, _ := json.Marshal(visual)
		if err := w.WorkspaceDB.SetSetting("app_settings", string(data)); err != nil {
			w.Logger.Warn("WORKSPACE", "save_app_settings_failed", "error", err.Error())
			return err
		}
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
			Model: v.Model, ThinkingEffort: v.ThinkingEffort,
		}
	}
	return out
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
			Model:          pc.Model,
			ThinkingEffort: pc.ThinkingEffort,
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
func (w *Workspace) HandleFetchModels(providerID, baseURL, apiKey string) ([]ttypes.ModelEntry, error) {
	if b := w.BackendSession(); b != nil {
		return b.HandleFetchModels(providerID, baseURL, apiKey)
	}
	p := provider.FromPreset(providerID, apiKey, baseURL)
	models, err := provider.FetchModels(p)
	if err != nil {
		return nil, err
	}
	entries := make([]ttypes.ModelEntry, 0, len(models))
	for _, model := range models {
		entries = append(entries, ttypes.ModelEntry{
			ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow,
		})
	}
	return entries, nil
}

// HandleUndo undoes the last message.
func (w *Workspace) HandleUndo(cmd string) interface{} {
	if b := w.BackendSession(); b != nil {
		return b.HandleUndo(cmd)
	}
	return nil
}

// SetYolo toggles auto-approve mode.
func (w *Workspace) SetYolo(enabled bool) {
	if b := w.BackendSession(); b != nil {
		b.SetYolo(enabled)
	}
}

// HardCancel forcefully cancels all running agents.
func (w *Workspace) HardCancel() {
	if b := w.BackendSession(); b != nil {
		b.HardCancel()
	}
}

// CancelSubAgent cancels a specific sub-agent by ID.
func (w *Workspace) CancelSubAgent(agentID string) {
	b := w.BackendSession()
	if b == nil {
		return
	}
	runner := b.SubAgentRunner()
	if runner != nil {
		runner.CancelSubAgent(map[string]any{"handle_id": agentID})
	}
}

// ResetOrchestratorOverrides clears runtime overrides for an orchestrator.
func (w *Workspace) ResetOrchestratorOverrides(orchID string) {
	b := w.BackendSession()
	if b == nil {
		return
	}
	b.RuntimeProfile().RemoveOverride(orchID)
	if err := b.ResetSessionSettings(); err != nil {
		w.Logger.Warn("RUNTIME", "session_settings_reset_failed", "error", err.Error())
	} else if w.ActiveSession != nil && w.ActiveSession.Settings != nil {
		w.ActiveSession.Settings.ResetToWorkspace(b.WorkspaceSettingsSnapshot())
	}
	w.Logger.Info("RUNTIME", "orchestrator_reset", "id", orchID)
}

// ResetSessionSettings clears the active session's DB-backed overrides.
func (w *Workspace) ResetSessionSettings() error {
	b := w.BackendSession()
	if b == nil {
		return fmt.Errorf("no active session")
	}
	if err := b.ResetSessionSettings(); err != nil {
		return err
	}
	if w.ActiveSession != nil && w.ActiveSession.Settings != nil {
		w.ActiveSession.Settings.ResetToWorkspace(b.WorkspaceSettingsSnapshot())
		w.SessionSettings = w.ActiveSession.Settings
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
				APIKey:      cfg.APIKey,
				ProviderURL: cfg.ProviderURL,
			}
		}
	}
	b := w.BackendSession()
	if b == nil {
		return ttypes.ProviderConfig{}
	}
	if cfg, ok := b.Config().Providers[providerID]; ok {
		return ttypes.ProviderConfig{
			APIKey:      cfg.APIKey,
			ProviderURL: cfg.ProviderURL,
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
func (w *Workspace) enrichSessionMeta(r sessiondb.SessionMeta) sessiondb.SessionMeta {
	dbPath, err := SessionDBPath(w.RootPath, r.ID)
	if err != nil {
		return r
	}
	if _, err := os.Stat(dbPath); err != nil {
		// Legacy fallback before dropping the registry entry.
		if legacyRoot, legErr := session.SessionsRoot(); legErr == nil {
			legacyPath := filepath.Join(legacyRoot, r.ID+".db")
			if _, legStat := os.Stat(legacyPath); legStat == nil {
				dbPath = legacyPath
			} else {
				// Session DB is gone; drop stale registry entry.
				_, _ = w.WorkspaceDB.DB.Exec(`DELETE FROM workspace_sessions WHERE session_id = ?`, r.ID)
				return sessiondb.SessionMeta{}
			}
		} else {
			_, _ = w.WorkspaceDB.DB.Exec(`DELETE FROM workspace_sessions WHERE session_id = ?`, r.ID)
			return sessiondb.SessionMeta{}
		}
	}

	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return r
	}
	defer db.Close()

	m, err := sessiondb.GetMeta(db)
	if err != nil {
		return r
	}
	// Preserve the model/provider from the registry if the DB is empty.
	if m.Model == "" {
		m.Model = r.Model
	}
	if m.Provider == "" {
		m.Provider = r.Provider
	}
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
	}
	return orch, err
}

// ── Message Conversion

// ConvertMessagesForDisplay converts backend session messages into the
// TUI display format. This is a workspace concern because it transforms
// session persistence data into the format the TUI layer expects.
func ConvertMessagesForDisplay(msgs []sessiondb.MessageWithMeta) []ttypes.ChatMessage {
	var result []ttypes.ChatMessage

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
			result = append(result, ttypes.ChatMessage{
				Role:    ttypes.RoleUser,
				Content: mm.Msg.Content.ContentDescription(),
				Time:    msgTime,
			})

		case "assistant":
			result = append(result, ttypes.ChatMessage{
				Role:    ttypes.RoleAssistant,
				Content: mm.Msg.Content.String(),
				Time:    msgTime,
			})

		case "thinking":
			result = append(result, ttypes.ChatMessage{
				Role:    ttypes.RoleThinking,
				Content: mm.Msg.ReasoningContent,
				Time:    msgTime,
			})
			if mm.Msg.Content.String() != "" {
				result = append(result, ttypes.ChatMessage{
					Role:    ttypes.RoleAssistant,
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

			argsCompact := tools.SummarizeToolArgs(toolName, toolArgs)
			content := toolName
			if argsCompact != "" {
				content = toolName + " " + argsCompact
			}

			chatMsg := ttypes.ChatMessage{
				Role:              ttypes.RoleTool,
				Content:           content,
				Time:              msgTime,
				ToolName:          toolName,
				ToolID:            mm.Msg.ToolCallID,
				ToolArgs:          argsCompact,
				ToolStatus:        toolStatus,
				ToolProgressLabel: mm.Meta.ProgressLabel,
				PermTag:           mm.Meta.PermTag,
				ToolPreviewPhase:  ttypes.ToolPreviewDone,
			}

			if mm.Meta.DurationMs > 0 {
				chatMsg.ToolDuration = time.Duration(mm.Meta.DurationMs) * time.Millisecond
			}
			if mm.Meta.EstTokens > 0 {
				chatMsg.ToolEstTokens = mm.Meta.EstTokens
			}
			if mm.Meta.LinesAdded > 0 || mm.Meta.LinesRemoved > 0 {
				chatMsg.ToolLivedAdded = mm.Meta.LinesAdded
				chatMsg.ToolLivedRemoved = mm.Meta.LinesRemoved
			}
			if mm.Meta.SubPaneID != "" {
				chatMsg.SubPaneID = mm.Meta.SubPaneID
			}

			result = append(result, chatMsg)

		default:
			if mm.Msg.Content.String() != "" {
				result = append(result, ttypes.ChatMessage{
					Role:    role,
					Content: mm.Msg.Content.String(),
					Time:    msgTime,
				})
			}
		}
	}

	return result
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
