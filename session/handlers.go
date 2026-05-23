package session

import (
	"fmt"
	"path/filepath"
	"strings"

	"crude/provider"
	ttypes "crude/tui/types"
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
			fmt.Sscanf(parts[1], "%d", &n)
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
		sb.WriteString(fmt.Sprintf("%d file(s) changed this session:\n", len(files)))
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
	oldModel := s.cfg.GetActiveMainCategory().Model
	l.Info("CONFIG", "save_begin", "old_provider", oldProvider, "old_model", oldModel, "turn_logs", tuiSettings.TurnLogsEnabled)

	newCfg := settingsToConfig(tuiSettings, s.cfg)
	mainCat := newCfg.GetActiveMainCategory()
	if mainCat.APIKey == "" {
		l.Warn("CONFIG", "save_blocked", "reason", "empty_api_key")
		return fmt.Errorf("API key cannot be empty — settings not saved")
	}

	providerChanged := s.cfg.ActiveProvider != newCfg.ActiveProvider
	modelChanged := mainCat.Model != oldModel

	s.cfg = newCfg

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

	metas, mdErr := provider.FetchModelsDevAll()
	if mdErr != nil {
		l.Warn("MODEL", "meta_fetch_error", "error", mdErr.Error())
	}

	entries := make([]ttypes.ModelEntry, len(models))
	enriched := 0
	for i, m := range models {
		entry := ttypes.ModelEntry{
			ID:            m.ID,
			Name:          m.Name,
			ContextWindow: m.ContextWindow,
		}
		if mdErr == nil {
			if meta := provider.LookupModelMeta(m.ID, metas); meta != nil {
				if entry.ContextWindow == 0 {
					entry.ContextWindow = meta.ContextWindow
				}
				entry.InputPrice = meta.InputPrice
				entry.OutputPrice = meta.OutputPrice
				entry.CacheReadPrice = meta.CacheReadPrice
				enriched++
			}
		}
		entries[i] = entry
	}
	l.Info("MODEL", "fetch_ok", "provider", providerID, "total", len(entries), "enriched", enriched)
	return entries, nil
}
