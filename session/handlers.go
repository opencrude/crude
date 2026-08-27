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

// HandleCompact force-compacts the conversation context on demand: older
// history is folded into a <context_checkpoint> summary and the durable
// store is rewritten with the compacted projection. It mirrors what
// truncateMessages does automatically at send time, but against half the
// model window so /compact always has headroom to reclaim below the
// automatic threshold.
func (s *Session) HandleCompact() ttypes.CompactResultMsg {
	if !s.turnGate.TryLock() {
		return ttypes.CompactResultMsg{Error: "Stop the active turn before compacting context"}
	}
	defer s.turnGate.Unlock()
	l := s.logger
	l.Info("COMPACT", "manual_request")

	// Drain in-flight async flushes first so a stale full-history write
	// cannot land after our compacted rewrite. finalizeTurn releases
	// msgsMu before spawning flushes, so waiting here cannot deadlock.
	s.flushWg.Wait()

	s.msgsMu.Lock()
	if len(s.msgs) == 0 {
		s.msgsMu.Unlock()
		return ttypes.CompactResultMsg{Desc: "Nothing to compact yet."}
	}

	msgs := make([]provider.ChatMessage, len(s.msgs))
	copy(msgs, s.msgs)

	model := ""
	if s.cfg != nil {
		model = s.cfg.GetActiveDefaultCategory().Model
	}
	ctxWindow := provider.LookupContextWindow(model)
	if ctxWindow <= 0 {
		ctxWindow = 256000
	}

	before := len(msgs)
	compacted := truncateMessages(msgs, ctxWindow/2, model)
	after := len(compacted)
	if after >= before {
		s.msgsMu.Unlock()
		l.Info("COMPACT", "already_compact", "history", before)
		return ttypes.CompactResultMsg{Desc: "Context is already compact.", Before: before, After: after}
	}

	// compacted = [summary] + msgs[cutoff:]; keep parallel arrays aligned.
	cutoff := before - (after - 1)
	newTurns := make([]int, 0, after)
	newMetas := make([]sessiondb.DisplayMeta, 0, after)
	newCreatedAts := make([]int64, 0, after)
	for i := cutoff; i < before; i++ {
		turn := 0
		if i < len(s.msgTurns) {
			turn = s.msgTurns[i]
		}
		meta := sessiondb.DisplayMeta{}
		if i < len(s.displayMetas) {
			meta = s.displayMetas[i]
		}
		createdAt := time.Now().Unix()
		if i < len(s.createdAts) {
			createdAt = s.createdAts[i]
		}
		newTurns = append(newTurns, turn)
		newMetas = append(newMetas, meta)
		newCreatedAts = append(newCreatedAts, createdAt)
	}

	s.msgs = compacted
	s.msgTurns = append([]int{0}, newTurns...)
	s.displayMetas = append([]sessiondb.DisplayMeta{{DisplayRole: "user"}}, newMetas...)
	s.createdAts = append([]int64{time.Now().Unix()}, newCreatedAts...)

	// Buffered writes are superseded by the compacted projection.
	s.writeMu.Lock()
	s.writeBuf = nil
	s.writeMu.Unlock()
	hasDB := s.db != nil && len(s.msgs) > 0
	if hasDB {
		s.appendToWriteBuf(s.msgs, s.msgTurns, s.displayMetas)
	}
	s.msgsMu.Unlock()

	if hasDB {
		s.flushWriteBufSync()
	}
	l.Info("COMPACT", "manual_compact", "history", before, "projected", after, "window", ctxWindow)
	return ttypes.CompactResultMsg{
		Desc:   fmt.Sprintf("Compacted %d message(s) into a context checkpoint; %d recent message(s) retained.", cutoff, after-1),
		Before: before,
		After:  after,
	}
}

func (s *Session) HandleUndo(cmd string) ttypes.UndoResultMsg {
	if !s.turnGate.TryLock() {
		return ttypes.UndoResultMsg{Error: "Stop the active turn before undoing file operations"}
	}
	defer s.turnGate.Unlock()
	l := s.logger
	l.Info("INPUT", "undo", "cmd", cmd)
	if s.editor == nil {
		l.Warn("EDITOR", "undo_no_editor", "cmd", cmd)
		return ttypes.UndoResultMsg{Error: "file editor not available"}
	}
	switch {
	case cmd == "/undo":
		l.Info("JOURNAL", "undo_last", "journal_len", len(s.editor.Log()))
		if err := s.editor.UndoLast(); err != nil {
			l.Error("JOURNAL", "undo_failed", "error", err.Error())
			return ttypes.UndoResultMsg{Error: err.Error()}
		}
		journalLog := s.editor.Log()
		remaining := len(journalLog)
		l.Info("JOURNAL", "undo_ok", "remaining", remaining)
		desc := "Undo complete. No more operations to undo."
		if remaining > 0 {
			desc = fmt.Sprintf("Undo complete. %d operation(s) remaining in journal.", remaining)
		}
		return ttypes.UndoResultMsg{Desc: desc}
	case strings.HasPrefix(cmd, "/undo-n"):
		n := 1
		parts := strings.Fields(cmd)
		if len(parts) > 1 {
			_, _ = fmt.Sscanf(parts[1], "%d", &n)
		}
		if n <= 0 {
			n = 1
		}
		l.Info("JOURNAL", "undo_n", "count", n, "journal_len", len(s.editor.Log()))
		if err := s.editor.UndoN(n); err != nil {
			l.Error("JOURNAL", "undo_n_failed", "count", n, "error", err.Error())
			return ttypes.UndoResultMsg{Error: err.Error()}
		}
		l.Info("JOURNAL", "undo_n_ok", "count", n)
		return ttypes.UndoResultMsg{Desc: fmt.Sprintf("Undid last %d file operation(s).", n)}
	case cmd == "/undo-all":
		totalOps := len(s.editor.Log())
		l.Info("JOURNAL", "undo_session", "ops_to_revert", totalOps)
		if err := s.editor.UndoSession(); err != nil {
			l.Error("JOURNAL", "undo_session_failed", "error", err.Error())
			return ttypes.UndoResultMsg{Error: err.Error()}
		}
		l.Info("JOURNAL", "undo_session_ok", "ops_reverted", totalOps)
		return ttypes.UndoResultMsg{Desc: "All file operations this session have been reverted."}
	case cmd == "/changes":
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
		sb.WriteString("Use /undo to revert the last operation, /undo-all to revert everything.")
		return ttypes.UndoResultMsg{Desc: sb.String()}
	}
	l.Warn("INPUT", "undo_unknown", "cmd", cmd)
	return ttypes.UndoResultMsg{}
}

func (s *Session) HandleSettingsSave(tuiSettings ttypes.Settings) error {
	l := s.logger
	s.mu.Lock()
	defer s.mu.Unlock()

	oldProvider := s.cfg.ActiveProvider
	oldModel := s.cfg.GetActiveDefaultCategory().Model
	l.Info("CONFIG", "save_begin", "old_provider", oldProvider, "old_model", oldModel, "turn_logs", tuiSettings.TurnLogsEnabled)

	overrides := settingsToSessionOverrides(tuiSettings)
	newCfg := configWithSessionOverrides(s.workspaceCfg, overrides)
	mainCat := newCfg.GetActiveDefaultCategory()
	// OAuth providers (e.g. ChatGPT subscription) have no API key; allow them.
	if mainCat.APIKey == "" && mainCat.OAuth == nil {
		l.Warn("CONFIG", "save_blocked", "reason", "empty_api_key")
		return fmt.Errorf("API key cannot be empty — settings not saved")
	}
	configuredProvider, err := s.cfg.providerForCategory(mainCat)
	if err != nil {
		return fmt.Errorf("invalid provider communication standard: %w", err)
	}

	providerChanged := s.cfg.ActiveProvider != newCfg.ActiveProvider
	modelChanged := mainCat.Model != oldModel
	if s.db != nil {
		if err := sessiondb.SaveAgentSettingOverrides(s.db, s.sessionID, overrides); err != nil {
			return fmt.Errorf("save session settings: %w", err)
		}
	}

	s.cfg = newCfg
	s.sessionOverrides = overrides
	if tuiSettings.SessionMultiAgent != nil {
		applySessionMultiAgent(s.cfg, *tuiSettings.SessionMultiAgent)
	}
	s.multiAgentOverride = cloneBoolPtr(tuiSettings.SessionMultiAgent)
	if s.db != nil {
		if err := sessiondb.SaveSessionFlags(s.db, s.sessionID, sessiondb.SessionFlags{MultiAgent: s.multiAgentOverride}); err != nil {
			return fmt.Errorf("save session flags: %w", err)
		}
	}
	// Update the runtime profile's base config so per-orchestrator overrides
	// continue to work on top of the new saved config.
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}

	// If multi-agent was just enabled, create the sub-agent runner if missing
	if s.cfg.IsMultiAgentEnabled() && s.subAgentSup == nil {
		l.Info("CONFIG", "sub_agent_runner_create", "reason", "multi_agent_enabled")
		s.subAgentSup = NewSubAgentRunner(s, s.cfg.Agents.MaxConcurrent)
		if s.sender != nil {
			s.subAgentSup.SetSender(s.sender)
		}
	}
	s.provider = configuredProvider
	if mainCat.ModelContextWindow > 0 {
		provider.SetModelContext(mainCat.Model, mainCat.ModelContextWindow)
		l.Debug("CONFIG", "ctx_window_set", "model", mainCat.Model, "window", mainCat.ModelContextWindow)
	}
	if mainCat.ModelInputPrice > 0 {
		provider.SetModelPrice(mainCat.Model, provider.ModelPrice{
			InputPricePerToken:       mainCat.ModelInputPrice,
			OutputPricePerToken:      mainCat.ModelOutputPrice,
			InputCachedPricePerToken: mainCat.ModelCachePrice,
		})
		l.Debug("CONFIG", "price_set", "model", mainCat.Model, "input", mainCat.ModelInputPrice, "output", mainCat.ModelOutputPrice)
	}
	provider.MergeModelCap(mainCat.Model, slices.Contains(mainCat.ModelModalities, provider.ModalityImage), mainCat.ModelSupportsTools)
	s.logger.SetTurnEnabled(tuiSettings.TurnLogsEnabled != "off")
	s.cfg.TurnLogsEnabled = tuiSettings.TurnLogsEnabled != "off"

	if providerChanged {
		l.Info("CONFIG", "provider_switch", "from", oldProvider, "to", newCfg.ActiveProvider)
	}
	if modelChanged {
		l.Info("CONFIG", "model_change", "from", oldModel, "to", mainCat.Model)
	}
	l.Info("CONFIG", "save_complete", "provider", newCfg.ActiveProvider, "model", mainCat.Model, "turn_logs", s.cfg.TurnLogsEnabled)
	return nil
}

func settingsToSessionOverrides(settings ttypes.Settings) map[string]sessiondb.AgentSettingOverride {
	overrides := make(map[string]sessiondb.AgentSettingOverride)
	if settings.SessionOverrides != nil {
		for category, cfg := range settings.SessionOverrides {
			overrides[category] = sessiondb.AgentSettingOverride{
				Provider: cfg.Provider, Model: cfg.Model,
				APIStandard: cfg.APIStandard, ThinkingEffort: cfg.ThinkingEffort,
				Inherit: cfg.Inherit,
			}
		}
		return overrides
	}
	for category, cfg := range settings.Agents.Categories {
		overrides[category] = sessiondb.AgentSettingOverride{
			Provider: cfg.Provider, Model: cfg.Model,
			APIStandard: cfg.APIStandard, ThinkingEffort: cfg.ThinkingEffort,
			Inherit: cfg.Inherit,
		}
	}
	if _, ok := overrides[CategoryDefault]; !ok {
		overrides[CategoryDefault] = sessiondb.AgentSettingOverride{
			Provider: settings.ProviderID, Model: settings.Model,
			APIStandard: settings.APIStandard, ThinkingEffort: settings.ThinkingEffort,
			Inherit: settings.Agents.Categories[CategoryDefault].Inherit,
		}
	}
	return overrides
}

func configWithSessionOverrides(base *Config, overrides map[string]sessiondb.AgentSettingOverride) *Config {
	if base == nil {
		base = &Config{ActiveProvider: "openrouter"}
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
		current := profile.Categories[category]
		if override.Inherit != "" {
			// A live reference: replace any stored manual values with the
			// marker and let resolution follow the chain at read time.
			profile.Categories[category] = CategoryConfig{Inherit: override.Inherit}
			continue
		}
		if override.Provider != "" {
			if current.Provider != override.Provider {
				current.APIKey = ""
				current.ProviderURL = ""
				current.APIStandard = ""
			}
			current.Provider = override.Provider
		}
		if override.Model != "" {
			if current.Model != override.Model && override.APIStandard == "" {
				current.APIStandard = ""
			}
			current.Model = override.Model
		}
		if override.APIStandard != "" {
			current.APIStandard = override.APIStandard
		}
		if override.ThinkingEffort != "" {
			current.ThinkingEffort = override.ThinkingEffort
		}
		current.Inherit = ""
		profile.Categories[category] = current
	}
	cfg.Agents.Profiles[profileName] = profile
	if main := cfg.GetActiveDefaultCategory(); main.Provider != "" {
		cfg.ActiveProvider = main.Provider
	}
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
	cat := CategoryConfig{Provider: base.EffectiveProviderID()}
	base.resolveCategoryDefaults(&cat)
	return &cat
}

// SettingsSnapshot returns the current effective session settings.
func (s *Session) SettingsSnapshot() ttypes.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := ConfigToSettings(s.cfg)
	settings.SessionMultiAgent = cloneBoolPtr(s.multiAgentOverride)
	if s.multiAgentOverride != nil {
		settings.Agents.Enabled = *s.multiAgentOverride
	}
	return settings
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
			Provider: override.Provider, Model: override.Model,
			APIStandard: override.APIStandard, ThinkingEffort: override.ThinkingEffort,
			Inherit: override.Inherit,
		}
	}
	return out
}

// ResetSessionSettings removes persisted overrides and immediately restores
// the workspace defaults for this session.
func (s *Session) ResetSessionSettings() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		if err := sessiondb.ClearAgentSettingOverrides(s.db, s.sessionID); err != nil {
			return err
		}
		if err := sessiondb.ClearSessionFlags(s.db, s.sessionID); err != nil {
			return err
		}
	}
	s.cfg = configWithSessionOverrides(s.workspaceCfg, nil)
	s.sessionOverrides = make(map[string]sessiondb.AgentSettingOverride)
	s.multiAgentOverride = nil
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	main := s.cfg.GetActiveDefaultCategory()
	configuredProvider, err := s.cfg.providerForCategory(main)
	if err != nil {
		return err
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshProviderCredentialsLocked(settings)
	if settings.ActiveProvider != "" {
		s.workspaceCfg.ActiveProvider = settings.ActiveProvider
	}
	ApplyWorkspaceAgentDefaults(s.workspaceCfg, settings.Agents)
	s.workspaceCfg.Memory = settings.Memory
	s.rebuildEffectiveConfigLocked()
}

// AdoptWorkspaceDefaults re-inherits the whole workspace baseline including
// agent role defaults. Only explicit user action (resetting a session's
// settings) reaches it.
func (s *Session) AdoptWorkspaceDefaults(settings ttypes.AppSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshProviderCredentialsLocked(settings)
	if settings.ActiveProvider != "" {
		s.workspaceCfg.ActiveProvider = settings.ActiveProvider
	}
	ApplyWorkspaceAgentDefaults(s.workspaceCfg, settings.Agents)
	s.workspaceCfg.Memory = settings.Memory
	s.rebuildEffectiveConfigLocked()
}

// refreshProviderCredentialsLocked updates how the session reaches a
// provider without touching which model a role picks. A provider the
// snapshot has never seen is adopted whole; there is nothing to keep stable.
func (s *Session) refreshProviderCredentialsLocked(settings ttypes.AppSettings) {
	if s.workspaceCfg == nil {
		s.workspaceCfg = &Config{ActiveProvider: "openrouter"}
	}
	if len(settings.Providers) == 0 {
		return
	}
	if s.workspaceCfg.Providers == nil {
		s.workspaceCfg.Providers = make(map[string]ProviderConfig)
	}
	for id, cfg := range settings.Providers {
		existing, known := s.workspaceCfg.Providers[id]
		if !known {
			s.workspaceCfg.Providers[id] = ProviderConfig{
				APIKey: cfg.APIKey, ProviderURL: cfg.ProviderURL,
				APIStandard: cfg.APIStandard, Model: cfg.Model,
				ModelStandards: cloneProviderModelStandards(cfg.ModelStandards),
				ThinkingEffort: cfg.ThinkingEffort,
				OAuth:          cfg.OAuth,
				Dialer:         cfg.Dialer,
				Protocol:       cfg.Protocol,
				ExtraBody:      cloneExtraBody(cfg.ExtraBody),
			}
			continue
		}
		existing.APIKey = cfg.APIKey
		existing.ProviderURL = cfg.ProviderURL
		existing.APIStandard = cfg.APIStandard
		existing.ModelStandards = cloneProviderModelStandards(cfg.ModelStandards)
		existing.OAuth = cfg.OAuth
		existing.Dialer = cfg.Dialer
		existing.Protocol = cfg.Protocol
		existing.ExtraBody = cloneExtraBody(cfg.ExtraBody)
		s.workspaceCfg.Providers[id] = existing
	}
}

func (s *Session) rebuildEffectiveConfigLocked() {
	s.cfg = configWithSessionOverrides(s.workspaceCfg, s.sessionOverrides)
	if s.multiAgentOverride != nil {
		applySessionMultiAgent(s.cfg, *s.multiAgentOverride)
	}
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	main := s.cfg.GetActiveDefaultCategory()
	configuredProvider, err := s.cfg.providerForCategory(main)
	if err != nil {
		s.logger.Warn("PROVIDER", "invalid_standard", "standard", main.APIStandard, "error", err.Error())
		return
	}
	s.provider = configuredProvider
}

func (s *Session) HandleFetchModels(providerID, baseURL, apiKey, standardID string) ([]ttypes.ModelEntry, error) {
	l := s.logger
	l.Info("MODEL", "fetch_begin", "provider", providerID)
	models, err := fetchProviderModels(providerID, baseURL, apiKey, standardID, func() (*provider.OAuthCredentials, *provider.DialSettings, *provider.ProtocolSettings, map[string]any) {
		if cfg := s.Config(); cfg != nil {
			if pc, ok := cfg.Providers[providerID]; ok {
				return pc.OAuth, pc.Dialer, pc.Protocol, pc.ExtraBody
			}
		}
		return nil, nil, nil, nil
	})
	if err != nil {
		l.Error("MODEL", "fetch_error", "provider", providerID, "error", err.Error())
		return nil, err
	}
	l.Info("MODEL", "fetch_ok", "provider", providerID, "count", len(models))
	return ttypes.ModelEntriesFromInfos(models), nil
}

// fetchProviderModels resolves a provider from settings and lists its models
// through the provider client. The stored lookup returns the saved OAuth
// credentials (e.g. ChatGPT subscription) and transport/protocol tuning so
// listing is authenticated and transported exactly like chat traffic.
func fetchProviderModels(providerID, baseURL, apiKey, standardID string, stored func() (*provider.OAuthCredentials, *provider.DialSettings, *provider.ProtocolSettings, map[string]any)) ([]provider.ModelInfo, error) {
	p := provider.FromPreset(providerID, apiKey, baseURL)
	if standardID != "" {
		if err := p.SetProtocolID(standardID); err != nil {
			return nil, err
		}
	}
	if stored != nil {
		oauth, dialer, protocol, extra := stored()
		if oauth != nil {
			p.SetOAuth(oauth)
		}
		if dialer != nil {
			p.SetDialSettings(*dialer)
		}
		if protocol != nil {
			p.SetProtocolSettings(protocol)
		}
		if len(extra) > 0 {
			p.SetExtraBody(extra)
		}
	}
	client, err := p.Client()
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		switch {
		case errors.Is(err, provider.ErrUnauthorized):
			if p.OAuth() != nil {
				return nil, fmt.Errorf("%s authentication failed — re-sign in with ChatGPT in Settings → Providers", p.Name)
			}
			if apiKey == "" {
				return nil, fmt.Errorf("%s requires an API key to list models — enter one in Settings → Providers", p.Name)
			}
			return nil, fmt.Errorf("invalid API key for %s", p.Name)
		case errors.Is(err, provider.ErrModelsUnsupported):
			return nil, fmt.Errorf("models endpoint not supported by %s", p.Name)
		}
		return nil, err
	}
	return models, nil
}
