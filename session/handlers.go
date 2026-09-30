package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

func (s *Session) AppendWorkspaceHistoryEvent(content, display string) error {
	eventID := fmt.Sprintf("history-%d", time.Now().UnixNano())
	return s.AppendWorkspaceHistoryEventWithID(eventID, content, ttypes.ConversationMessage{InternalID: eventID + "-status", Role: "status", Content: display})
}

func (s *Session) AppendWorkspaceHistoryEventWithID(eventID, content string, status ttypes.ConversationMessage) error {
	if !s.turnGate.TryLock() {
		return errors.New("stop the active turn before recording workspace restore")
	}
	defer s.turnGate.Unlock()
	message := provider.NewTextMessage("user", content)
	createdAt := time.Now()
	message.InternalID = eventID
	message.Origin = provider.OriginRuntimeContext
	message.Visibility = provider.VisibilityContextOnly
	status.Role = "status"
	if status.InternalID == "" {
		status.InternalID = eventID + "-status"
	}
	if status.Time.IsZero() {
		status.Time = createdAt
	}
	meta := sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, DisplayRole: "hidden_wire_context", ContextOnly: true,
		Snapshots: []ttypes.ConversationMessage{status}}
	s.msgsMu.Lock()
	for _, existing := range s.msgs {
		if existing.InternalID == eventID || (existing.EffectiveOrigin() == provider.OriginRuntimeContext && existing.Content.String() == content) {
			for _, active := range s.activeContext {
				if active.Content.String() == content {
					s.msgsMu.Unlock()
					return nil
				}
			}
			active := append(append([]provider.ChatMessage(nil), s.activeContext...), existing)
			generation := s.contextGeneration + 1
			if generation == 1 {
				generation = 2
			}
			s.msgsMu.Unlock()
			if s.db != nil {
				if err := sessiondb.SaveContextState(s.db, s.sessionID, s.sessionID, sessiondb.ContextState{Generation: generation, Reason: "workspace_restore", Messages: active}); err != nil {
					return err
				}
			}
			s.msgsMu.Lock()
			s.activeContext = active
			s.contextGeneration = generation
			s.contextReason = "workspace_restore"
			s.msgsMu.Unlock()
			return nil
		}
	}
	msgs := append(append([]provider.ChatMessage(nil), s.msgs...), message)
	turns := append(append([]int(nil), s.msgTurns...), s.turnNumber)
	metas := append(append([]sessiondb.DisplayMeta(nil), s.displayMetas...), meta)
	createdAts := append(append([]int64(nil), s.createdAts...), createdAt.UnixNano())
	active := append(append([]provider.ChatMessage(nil), s.activeContext...), message)
	generation := s.contextGeneration + 1
	if generation == 1 {
		generation = 2
	}
	s.msgsMu.Unlock()
	if s.db != nil {
		if err := sessiondb.AppendMessageWithMeta(s.db, s.sessionID, message, s.turnNumber, meta); err != nil {
			return err
		}
		if err := sessiondb.SaveContextState(s.db, s.sessionID, s.sessionID, sessiondb.ContextState{Generation: generation, Reason: "workspace_restore", Messages: active}); err != nil {
			return err
		}
	}
	s.msgsMu.Lock()
	s.msgs = msgs
	s.msgTurns = turns
	s.displayMetas = metas
	s.createdAts = createdAts
	s.activeContext = active
	s.contextGeneration = generation
	s.contextReason = "workspace_restore"
	s.msgsMu.Unlock()
	return nil
}

// AppendSubAgentHistoryStatus durably adds restore status to a completed
// child's own transcript. The root context event remains separate because it
// informs future root requests about the changed workspace.
func (s *Session) AppendSubAgentHistoryStatus(agentID, eventID string, status ttypes.ConversationMessage) (bool, error) {
	if s == nil || s.db == nil || agentID == "" {
		return false, nil
	}
	results, err := sessiondb.ListSubAgentResults(s.db, s.sessionID)
	if err != nil {
		return false, err
	}
	childSessionID := ""
	for _, result := range results {
		if result.AgentID == agentID {
			childSessionID = result.ChildSessionID
			break
		}
	}
	if childSessionID == "" {
		return false, nil
	}
	dbPath := filepath.Join(s.logger.SessionDir(), "subagents", filepath.Base(childSessionID)+".db")
	childDB, err := sessiondb.Open(dbPath)
	if err != nil {
		return true, err
	}
	defer childDB.Close()
	rows, err := sessiondb.LoadMessagesWithMeta(childDB)
	if err != nil {
		return true, err
	}
	for _, row := range rows {
		for _, snapshot := range row.Meta.Snapshots {
			if snapshot.InternalID == eventID {
				return true, nil
			}
		}
	}
	message := provider.NewTextMessage("user", "<workspace_history_event>"+status.Content+"</workspace_history_event>")
	message.InternalID = eventID + "-wire"
	message.Origin = provider.OriginRuntimeContext
	message.Visibility = provider.VisibilityContextOnly
	status.InternalID = eventID
	status.Role = "status"
	if status.Time.IsZero() {
		status.Time = time.Now()
	}
	meta := sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, DisplayRole: "hidden_wire_context", ContextOnly: true,
		Snapshots: []ttypes.ConversationMessage{status}}
	turn := 1
	if len(rows) > 0 && rows[len(rows)-1].Turn > turn {
		turn = rows[len(rows)-1].Turn
	}
	return true, sessiondb.AppendMessageWithMeta(childDB, childSessionID, message, turn, meta)
}

func (s *Session) SetYolo(enabled bool) {
	s.yolo.Store(enabled)
	if s.logger != nil {
		s.logger.Info("SAFETY", "yolo", "enabled", enabled)
	}
}

// YoloEnabled reports the live session policy. Active turns consult this
// value at each permission boundary, so /yolo takes effect without waiting
// for the next turn to construct a new Agent.
func (s *Session) YoloEnabled() bool {
	return s != nil && s.yolo.Load()
}

func (s *Session) HandleUndo(cmd string) ttypes.UndoResultMsg {
	if !s.turnGate.TryLock() {
		return ttypes.UndoResultMsg{Error: "Stop the active turn before undoing file operations"}
	}
	defer s.turnGate.Unlock()
	l := s.logger
	l.Info("INPUT", "undo", "cmd", cmd)
	if strings.HasPrefix(cmd, "/undo") {
		return ttypes.UndoResultMsg{Error: "Legacy journal undo is retired. Use /undo in the IDE to open the Local History preview."}
	}
	if s.editor == nil {
		l.Warn("EDITOR", "undo_no_editor", "cmd", cmd)
		return ttypes.UndoResultMsg{Error: "file editor not available"}
	}
	if cmd == "/changes" {
		files := s.editor.ChangedFiles()
		l.Info("EDITOR", "changed_files", "count", len(files))
		if len(files) == 0 {
			return ttypes.UndoResultMsg{Desc: "No file changes this session."}
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d file(s) changed this session:\n", len(files))
		for _, f := range files {
			rel, relErr := filepath.Rel(".", f)
			if relErr != nil {
				rel = f
			}
			sb.WriteString("  " + rel + "\n")
		}
		sb.WriteString("Use /undo in the IDE to review message-scoped Local History safely.")
		return ttypes.UndoResultMsg{Desc: sb.String()}
	}
	l.Warn("INPUT", "undo_unknown", "cmd", cmd)
	return ttypes.UndoResultMsg{}
}

func (s *Session) HandleSettingsSave(tuiSettings ttypes.Settings) error {
	l := s.logger
	s.delegationMu.Lock()
	defer s.delegationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissionReviewRevision.Add(1)

	oldDelegation := s.cfg.IsMultiAgentEnabled()
	defer func() { s.recordDelegationChange(oldDelegation, s.cfg.IsMultiAgentEnabled()) }()
	oldMain := s.cfg.GetActiveDefaultCategory()
	oldTemplate := s.cfg.templateIDForAccount(oldMain.AccountID)
	oldModel := oldMain.Model
	l.Info("CONFIG", "save_begin", "old_template", oldTemplate, "old_account", oldMain.AccountID, "old_model", oldModel, "turn_logs", tuiSettings.TurnLogsEnabled)

	overrides := settingsToSessionOverrides(tuiSettings)
	newCfg := configWithSessionOverrides(s.workspaceCfg, overrides)
	newCfg.SelectedAgent = tuiSettings.SessionMainAgent
	if tuiSettings.SessionMainAgent != "" {
		pin := overrides["root/"+tuiSettings.SessionMainAgent]
		newCfg.selectedMainOverride = &pin
	}
	if err := newCfg.ValidateSelectedAgent(); err != nil {
		return fmt.Errorf("select a usable root agent: %w", err)
	}
	mainCat := newCfg.GetActiveDefaultCategory()
	pinKey := CategoryDefault
	if tuiSettings.SessionMainAgent != "" {
		pinKey = "root/" + tuiSettings.SessionMainAgent
	}
	if s.accountUnpinned && overrides[pinKey].AccountID == "" {
		return fmt.Errorf("select a connection for this session before saving: %w", ErrAccountRequired)
	}
	configuredProvider, err := newCfg.providerForCategory(mainCat)
	if err != nil {
		return fmt.Errorf("invalid provider connection: %w", err)
	}
	// Every explicit role override is validated before any DB write or runtime
	// update; a mismatched account cannot be saved to fail only on launch.
	for role := range overrides {
		if strings.HasPrefix(role, "root/") {
			continue
		}
		if _, err := newCfg.providerForCategory(newCfg.GetCategoryConfig(role)); err != nil {
			return fmt.Errorf("%s connection: %w", role, err)
		}
	}

	newTemplate := newCfg.templateIDForAccount(mainCat.AccountID)
	connectionChanged := oldMain.AccountID != mainCat.AccountID
	modelChanged := mainCat.Model != oldModel
	if s.db != nil {
		if err := sessiondb.SaveSessionSettings(s.db, s.sessionID, newTemplate, mainCat.AccountID, overrides, sessiondb.SessionFlags{MultiAgent: tuiSettings.SessionMultiAgent, MainAgent: tuiSettings.SessionMainAgent}); err != nil {
			return fmt.Errorf("save session settings: %w", err)
		}
	}
	s.cfg = newCfg
	s.selectedMainAgent = tuiSettings.SessionMainAgent
	s.accountUnpinned = false
	s.sessionOverrides = overrides
	if tuiSettings.SessionMultiAgent != nil {
		applySessionMultiAgent(s.cfg, *tuiSettings.SessionMultiAgent)
	}
	s.multiAgentOverride = cloneBoolPtr(tuiSettings.SessionMultiAgent)
	// Update the runtime profile's base config so per-orchestrator overrides
	// continue to work on top of the new saved config.
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}

	s.provider = configuredProvider
	if mainCat.ModelInputPrice > 0 {
		provider.SetModelPrice(mainCat.Model, provider.ModelPrice{
			InputPricePerToken:       mainCat.ModelInputPrice,
			OutputPricePerToken:      mainCat.ModelOutputPrice,
			InputCachedPricePerToken: mainCat.ModelCachePrice,
		})
		l.Debug("CONFIG", "price_set", "model", mainCat.Model, "input", mainCat.ModelInputPrice, "output", mainCat.ModelOutputPrice)
	}
	provider.MergeModelCap(mainCat.Model, slices.Contains(mainCat.ModelModalities, provider.ModalityImage), mainCat.ModelSupportsTools)
	provider.MergeModelModalities(mainCat.Model, mainCat.ModelModalities)
	s.logger.SetTurnEnabled(tuiSettings.TurnLogsEnabled != "off")
	s.cfg.TurnLogsEnabled = tuiSettings.TurnLogsEnabled != "off"

	if connectionChanged {
		l.Info("CONFIG", "account_switch", "from", oldMain.AccountID, "to", mainCat.AccountID,
			"from_template", oldTemplate, "to_template", newTemplate)
	}
	if modelChanged {
		l.Info("CONFIG", "model_change", "from", oldModel, "to", mainCat.Model)
	}
	l.Info("CONFIG", "save_complete", "template", newTemplate, "account", mainCat.AccountID, "model", mainCat.Model, "turn_logs", s.cfg.TurnLogsEnabled)
	return nil
}

func settingsToSessionOverrides(settings ttypes.Settings) map[string]sessiondb.AgentSettingOverride {
	overrides := make(map[string]sessiondb.AgentSettingOverride)
	if settings.SessionOverrides != nil {
		for category, cfg := range settings.SessionOverrides {
			overrides[category] = sessiondb.AgentSettingOverride{
				AccountID: cfg.AccountID, Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort,
				ContextCompactionPercent: cloneIntPtr(cfg.ContextCompactionPercent), Inherit: cfg.Inherit,
			}
		}
		return overrides
	}
	for category, cfg := range settings.Agents.Categories {
		overrides[category] = sessiondb.AgentSettingOverride{
			AccountID: cfg.AccountID, Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort,
			ContextCompactionPercent: cloneIntPtr(cfg.ContextCompactionPercent), Inherit: cfg.Inherit,
		}
	}
	if _, ok := overrides[CategoryDefault]; !ok {
		overrides[CategoryDefault] = sessiondb.AgentSettingOverride{
			AccountID: settings.AccountID, Model: settings.Model, ThinkingEffort: settings.ThinkingEffort,
			ContextCompactionPercent: cloneIntPtr(settings.Agents.Categories[CategoryDefault].ContextCompactionPercent),
			Inherit:                  settings.Agents.Categories[CategoryDefault].Inherit,
		}
	}
	return overrides
}

func configWithSessionOverrides(base *Config, overrides map[string]sessiondb.AgentSettingOverride) *Config {
	if base == nil {
		base = &Config{}
	}
	cfg := copyConfig(base)
	// Capture the live workspace main-agent category so categories marked
	// Inherit="workspace" can resolve through it at read time.
	cfg.workspaceMain = workspaceMainCategory(base)
	if cfg.Agents.Profiles == nil {
		cfg.Agents.Profiles = make(map[string]ProfileConfig)
	}
	profileName := cfg.Agents.ActiveProfile
	if profileName == "" {
		profileName = "default"
		cfg.Agents.ActiveProfile = profileName
	}
	profile := cfg.Agents.Profiles[profileName]
	if profile.Categories == nil {
		profile.Categories = make(map[string]CategoryConfig)
	}
	for category, override := range overrides {
		if strings.HasPrefix(category, "root/") {
			continue
		}
		current := profile.Categories[category]
		if override.Inherit != "" {
			// Inheritance controls model/policy, while a previously pinned
			// connection remains stable until the user explicitly changes it.
			pin := current.AccountID
			if override.AccountID != "" {
				pin = override.AccountID
			}
			profile.Categories[category] = CategoryConfig{Inherit: override.Inherit, AccountID: pin}
			continue
		}
		if override.AccountID != "" {
			current.AccountID = override.AccountID
		}
		if override.Model != "" {
			current.Model = override.Model
		}
		if override.ThinkingEffort != "" {
			current.ThinkingEffort = override.ThinkingEffort
		}
		if override.ContextCompactionPercent != nil {
			current.ContextCompactionPercent = cloneIntPtr(override.ContextCompactionPercent)
		}
		current.Inherit = ""
		profile.Categories[category] = current
	}
	cfg.Agents.Profiles[profileName] = profile
	return cfg
}

// workspaceMainCategory extracts the workspace baseline's main-agent category.
// This is what a session's Inherit="workspace" markers resolve through, so
// they follow the live workspace main agent rather than a snapshot copy.
func workspaceMainCategory(base *Config) *CategoryConfig {
	if base == nil {
		return nil
	}
	prof, _ := activeProfileCfg(base)
	for _, key := range []string{CategoryDefault, "main"} {
		if cat, ok := prof.Categories[key]; ok {
			dup := cat
			return &dup
		}
	}
	cat := CategoryConfig{}
	base.resolveCategoryDefaults(&cat)
	return &cat
}

// SettingsSnapshot returns the current effective session settings.
func (s *Session) SettingsSnapshot() ttypes.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := ConfigToSettings(s.cfg)
	settings.SessionMainAgent = s.selectedMainAgent
	settings.SessionMultiAgent = cloneBoolPtr(s.multiAgentOverride)
	if s.multiAgentOverride != nil {
		settings.Agents.Enabled = *s.multiAgentOverride
	}
	return settings
}

// ConnectionIssue reports a missing pinned connection without replacing it
// with the workspace default. History remains available for inspection.
func (s *Session) ConnectionIssue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountUnpinned {
		return ErrAccountRequired
	}
	if err := s.cfg.ValidateSelectedAgent(); err != nil {
		return err
	}
	main := s.cfg.GetActiveDefaultCategory()
	_, err := s.cfg.resolveAccount(main.AccountID)
	return err
}

// WorkspaceSettingsSnapshot returns the inherited workspace baseline without
// this session's persisted overrides.
func (s *Session) WorkspaceSettingsSnapshot() ttypes.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ConfigToSettings(s.workspaceCfg)
}

// SessionOverridesSnapshot returns only explicit DB-backed overrides.
func (s *Session) SessionOverridesSnapshot() map[string]ttypes.CategorySettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ttypes.CategorySettings, len(s.sessionOverrides))
	for category, override := range s.sessionOverrides {
		out[category] = ttypes.CategorySettings{
			AccountID: override.AccountID, Model: override.Model,
			ThinkingEffort:           override.ThinkingEffort,
			Inherit:                  override.Inherit,
			ContextCompactionPercent: cloneIntPtr(override.ContextCompactionPercent),
		}
	}
	return out
}

// ResetSessionSettings removes persisted overrides and immediately restores
// the workspace defaults for this session.
func (s *Session) ResetSessionSettings() error {
	s.delegationMu.Lock()
	defer s.delegationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountUnpinned {
		return fmt.Errorf("select a connection for this session before resetting: %w", ErrAccountRequired)
	}
	s.permissionReviewRevision.Add(1)
	oldDelegation := s.cfg.IsMultiAgentEnabled()
	defer func() { s.recordDelegationChange(oldDelegation, s.cfg.IsMultiAgentEnabled()) }()
	// Reset can adopt current workspace role models, but this session's
	// connection pin survives until an explicit connection change is saved.
	pins := make(map[string]sessiondb.AgentSettingOverride)
	for role, override := range s.sessionOverrides {
		if strings.HasPrefix(role, "root/") {
			continue
		}
		if override.AccountID != "" {
			pins[role] = sessiondb.AgentSettingOverride{AccountID: override.AccountID}
		}
	}
	if _, ok := pins[CategoryDefault]; !ok {
		main := s.cfg.GetActiveDefaultCategory()
		pins[CategoryDefault] = sessiondb.AgentSettingOverride{AccountID: main.AccountID}
	}
	newCfg := configWithSessionOverrides(s.workspaceCfg, pins)
	main := newCfg.GetActiveDefaultCategory()
	configuredProvider, err := newCfg.providerForCategory(main)
	if err != nil {
		return err
	}
	if s.db != nil {
		if err := sessiondb.SaveSessionSettings(s.db, s.sessionID, newCfg.templateIDForAccount(main.AccountID), main.AccountID, pins, sessiondb.SessionFlags{}); err != nil {
			return err
		}
	}
	s.cfg = newCfg
	s.selectedMainAgent = ""
	s.sessionOverrides = pins
	s.multiAgentOverride = nil
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	s.provider = configuredProvider
	return nil
}

// UpdateWorkspaceDefaults refreshes the inherited workspace baseline: provider
// endpoints, wire standards, credentials, AND agent role defaults.
//
// With live inheritance, an Inherit="workspace" marker must resolve against the
// current workspace main agent, and a role that inherits the session main
// follows today's main. So a workspace edit propagates to live sessions without
// copying values. Manual session overrides are preserved by
// rebuildEffectiveConfigLocked, which re-applies them on top of the refreshed
// baseline.
func (s *Session) UpdateWorkspaceDefaults(settings ttypes.AppSettings) {
	s.delegationMu.Lock()
	defer s.delegationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshProviderCredentialsLocked(settings)
	ApplyWorkspaceAgentDefaults(s.workspaceCfg, settings.Agents)
	s.workspaceCfg.Memory = settings.Memory
	s.workspaceCfg.Safety = settings.Safety
	s.rebuildEffectiveConfigLocked()
}

// AdoptWorkspaceDefaults re-inherits the whole workspace baseline including
// agent role defaults. Only explicit user action (resetting a session's
// settings) reaches it.
func (s *Session) AdoptWorkspaceDefaults(settings ttypes.AppSettings) {
	s.delegationMu.Lock()
	defer s.delegationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshProviderCredentialsLocked(settings)
	ApplyWorkspaceAgentDefaults(s.workspaceCfg, settings.Agents)
	s.workspaceCfg.Memory = settings.Memory
	s.workspaceCfg.Safety = settings.Safety
	s.rebuildEffectiveConfigLocked()
}

// refreshProviderCredentialsLocked updates how the session reaches a
// provider without touching which model a role picks. A provider the
// snapshot has never seen is adopted whole; there is nothing to keep stable.
func (s *Session) refreshProviderCredentialsLocked(settings ttypes.AppSettings) {
	if s.workspaceCfg == nil {
		s.workspaceCfg = &Config{}
	}
	s.workspaceCfg.Accounts = provider.CloneAccounts(settings.Accounts)
	s.workspaceCfg.Providers = make(map[string]ProviderConfig, len(settings.Providers))
	for id, template := range settings.Providers {
		s.workspaceCfg.Providers[id] = provider.CloneTemplate(template)
	}
}

func (s *Session) rebuildEffectiveConfigLocked() {
	s.permissionReviewRevision.Add(1)
	oldDelegation := s.cfg.IsMultiAgentEnabled()
	defer func() { s.recordDelegationChange(oldDelegation, s.cfg.IsMultiAgentEnabled()) }()
	s.cfg = configWithSessionOverrides(s.workspaceCfg, s.sessionOverrides)
	s.cfg.SelectedAgent = s.selectedMainAgent
	if s.selectedMainAgent != "" {
		pin := s.sessionOverrides["root/"+s.selectedMainAgent]
		s.cfg.selectedMainOverride = &pin
	}
	if s.accountUnpinned {
		profile, name := activeProfileCfg(s.cfg)
		cat := profile.Categories[CategoryDefault]
		cat.AccountID = ""
		cat.Inherit = ""
		profile.Categories[CategoryDefault] = cat
		s.cfg.Agents.Profiles[name] = profile
		s.provider = nil
	}
	if s.multiAgentOverride != nil {
		applySessionMultiAgent(s.cfg, *s.multiAgentOverride)
	}
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	s.provider = nil
	main := s.cfg.GetActiveDefaultCategory()
	configuredProvider, err := s.cfg.providerForCategory(main)
	if err != nil {
		s.logger.Warn("PROVIDER", "account_unavailable", "account", main.AccountID, "error", err.Error())
		return
	}
	s.provider = configuredProvider
}

// HandleFetchModelsForAccount lists models using the exact selected connection.
// The caller must correlate asynchronous results to ProviderID and AccountID.
func (s *Session) HandleFetchModelsForAccount(providerID, accountID, baseURL, standardID string) ([]ttypes.ModelEntry, error) {
	s.mu.Lock()
	cfg := copyConfig(s.cfg)
	s.mu.Unlock()
	_ = providerID // The selected account, not the UI's provider label, owns routing.
	p, err := cfg.ProviderForAccount(accountID, "")
	if err != nil {
		return nil, err
	}
	if baseURL != "" {
		p.BaseURL = baseURL
	}
	if standardID != "" {
		if err := p.SetProtocolID(standardID); err != nil {
			return nil, err
		}
	}
	models, err := listBoundProviderModels(p)
	if err != nil {
		return nil, err
	}
	return ttypes.ModelEntriesFromInfos(models), nil
}

func listBoundProviderModels(p *provider.Provider) ([]provider.ModelInfo, error) {
	client, err := p.Client()
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		switch {
		case errors.Is(err, provider.ErrUnauthorized):
			return nil, fmt.Errorf("%s account %q authentication failed — reconnect it in Settings → Providers", p.Name, p.AccountID)
		case errors.Is(err, provider.ErrModelsUnsupported):
			return nil, fmt.Errorf("models endpoint not supported by %s", p.Name)
		}
		return nil, err
	}
	return models, nil
}
