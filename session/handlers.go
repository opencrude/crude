package session

import (
	"fmt"
	"path/filepath"
	"strings"

	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

func (s *Session) SetYolo(enabled bool) {
	s.yolo = enabled
	s.logger.Info("SAFETY", "yolo", "enabled", enabled)
}

func (s *Session) HandleUndo(cmd string) ttypes.UndoResultMsg {
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
	if mainCat.APIKey == "" {
		l.Warn("CONFIG", "save_blocked", "reason", "empty_api_key")
		return fmt.Errorf("API key cannot be empty — settings not saved")
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
	// Update the runtime profile's base config so per-orchestrator overrides
	// continue to work on top of the new saved config.
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(newCfg)
	}

	// If multi-agent was just enabled, create the sub-agent runner if missing
	if s.cfg.IsMultiAgentEnabled() && s.subAgentSup == nil {
		l.Info("CONFIG", "sub_agent_runner_create", "reason", "multi_agent_enabled")
		s.subAgentSup = NewSubAgentRunner(s, s.cfg.Agents.MaxConcurrent)
		if s.sender != nil {
			s.subAgentSup.SetSender(s.sender)
		}
	}
	s.provider = provider.FromPreset(mainCat.Provider, mainCat.APIKey, mainCat.ProviderURL)
	s.provider.Model = mainCat.Model
	s.provider.SetThinkingEffort(mainCat.ThinkingEffort)
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
	provider.SetModelCap(mainCat.Model, mainCat.ModelSupportsVision, mainCat.ModelSupportsTools)
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
				Provider: cfg.Provider, Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort,
			}
		}
		return overrides
	}
	for category, cfg := range settings.Agents.Categories {
		overrides[category] = sessiondb.AgentSettingOverride{
			Provider: cfg.Provider, Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort,
		}
	}
	if _, ok := overrides[CategoryDefault]; !ok {
		overrides[CategoryDefault] = sessiondb.AgentSettingOverride{
			Provider: settings.ProviderID, Model: settings.Model, ThinkingEffort: settings.ThinkingEffort,
		}
	}
	return overrides
}

func configWithSessionOverrides(base *Config, overrides map[string]sessiondb.AgentSettingOverride) *Config {
	if base == nil {
		base = &Config{ActiveProvider: "openrouter"}
	}
	cfg := copyConfig(base)
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
		if override.Provider != "" {
			if current.Provider != override.Provider {
				current.APIKey = ""
				current.ProviderURL = ""
			}
			current.Provider = override.Provider
		}
		if override.Model != "" {
			current.Model = override.Model
		}
		if override.ThinkingEffort != "" {
			current.ThinkingEffort = override.ThinkingEffort
		}
		profile.Categories[category] = current
	}
	cfg.Agents.Profiles[profileName] = profile
	if main := cfg.GetActiveDefaultCategory(); main.Provider != "" {
		cfg.ActiveProvider = main.Provider
	}
	return cfg
}

// SettingsSnapshot returns the current effective session settings.
func (s *Session) SettingsSnapshot() ttypes.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ConfigToSettings(s.cfg)
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
			Provider: override.Provider, Model: override.Model, ThinkingEffort: override.ThinkingEffort,
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
	}
	s.cfg = configWithSessionOverrides(s.workspaceCfg, nil)
	s.sessionOverrides = make(map[string]sessiondb.AgentSettingOverride)
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	main := s.cfg.GetActiveDefaultCategory()
	s.provider = provider.FromPreset(main.Provider, main.APIKey, main.ProviderURL)
	s.provider.Model = main.Model
	s.provider.SetThinkingEffort(main.ThinkingEffort)
	return nil
}

// UpdateWorkspaceDefaults refreshes the inherited baseline while preserving
// this session's explicit DB-backed overrides.
func (s *Session) UpdateWorkspaceDefaults(settings ttypes.AppSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workspaceCfg == nil {
		s.workspaceCfg = &Config{ActiveProvider: "openrouter"}
	}
	if settings.ActiveProvider != "" {
		s.workspaceCfg.ActiveProvider = settings.ActiveProvider
	}
	if len(settings.Providers) > 0 {
		if s.workspaceCfg.Providers == nil {
			s.workspaceCfg.Providers = make(map[string]ProviderConfig)
		}
		for id, cfg := range settings.Providers {
			s.workspaceCfg.Providers[id] = ProviderConfig{
				APIKey: cfg.APIKey, ProviderURL: cfg.ProviderURL,
				Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort,
			}
		}
	}
	ApplyWorkspaceAgentDefaults(s.workspaceCfg, settings.Agents)
	s.cfg = configWithSessionOverrides(s.workspaceCfg, s.sessionOverrides)
	if s.runtimeProfile != nil {
		s.runtimeProfile.UpdateBase(s.cfg)
	}
	main := s.cfg.GetActiveDefaultCategory()
	s.provider = provider.FromPreset(main.Provider, main.APIKey, main.ProviderURL)
	s.provider.Model = main.Model
	s.provider.SetThinkingEffort(main.ThinkingEffort)
}

func (s *Session) HandleFetchModels(providerID, baseURL, apiKey string) ([]ttypes.ModelEntry, error) {
	l := s.logger
	l.Info("MODEL", "fetch_begin", "provider", providerID)
	p := provider.FromPreset(providerID, apiKey, baseURL)
	models, err := provider.FetchModels(p)
	if err != nil {
		l.Error("MODEL", "fetch_error", "provider", providerID, "error", err.Error())
		return nil, err
	}
	l.Info("MODEL", "fetched", "provider", providerID, "count", len(models))

	// Model discovery must return as soon as the provider responds. The full
	// models.dev catalog is loaded independently during session startup; doing
	// another synchronous 6MB download here made the Settings dropdown appear
	// stuck for up to 30 seconds after models had already arrived.
	metas := provider.GetAllModelMetas()

	entries := make([]ttypes.ModelEntry, len(models))
	enriched := 0
	for i, m := range models {
		entry := ttypes.ModelEntry{
			ID:            m.ID,
			Name:          m.Name,
			ContextWindow: m.ContextWindow,
		}
		if meta := provider.LookupModelMeta(m.ID, metas); meta != nil {
			if entry.ContextWindow == 0 {
				entry.ContextWindow = meta.ContextWindow
			}
			entry.InputPrice = meta.InputPrice
			entry.OutputPrice = meta.OutputPrice
			entry.CacheReadPrice = meta.CacheReadPrice
			entry.SupportsVision = meta.SupportsVision
			entry.SupportsTools = meta.SupportsTools
			entry.MaxOutput = meta.MaxOutput
			entry.Attachment = meta.Attachment
			enriched++
		}
		entries[i] = entry
	}
	l.Info("MODEL", "fetch_ok", "provider", providerID, "total", len(entries), "enriched", enriched)
	return entries, nil
}
