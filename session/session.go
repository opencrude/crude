package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crude/agent"
	"crude/clipboard"
	"crude/diff"
	"crude/fileio"
	"crude/js"
	"crude/jsonrepair"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/search"
	"crude/statemachine"
	"crude/tools"
	"crude/tui"
)

type Config struct {
	ActiveProvider  string
	Providers       map[string]ProviderConfig
	TurnLogsEnabled bool
}

type ProviderConfig struct {
	APIKey         string `json:"api_key,omitempty"`
	Model          string `json:"model,omitempty"`
	ProviderURL    string `json:"provider_url,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`

	// Main model metadata
	ModelContextWindow  int     `json:"model_context_window,omitempty"`
	MainInputPrice      float64 `json:"main_input_price,omitempty"`
	MainOutputPrice     float64 `json:"main_output_price,omitempty"`
	MainCacheReadPrice  float64 `json:"main_cache_read_price,omitempty"`
}

type Session struct {
	cfg *Config
	mu  sync.Mutex

	provider  *provider.Provider
	exec      fileio.Executer
	editor    *fileio.Editor
	logger    *logger.Logger
	permStore *safety.PermissionStore
	sm        *statemachine.ConversationStateMachine
	asyncMgr  *fileio.AsyncCommandManager

	msgs          []provider.ChatMessage
	msgsMu        sync.Mutex
	pendingBlock  bool
	blockMu       sync.Mutex
	lastBlockResp string
	blockRespMu   sync.Mutex
	msgCounter    int
	msgCounterMu  sync.Mutex

	sessionID    string
	sessionStart time.Time
	knowledgeDir string
	yolo         bool

	// Token tracking and cost accumulation — real counts from API
	cost   *CostAccumulator
	tokens *TokenTracker
}

func New(cfg *Config, homeDir string) (*Session, error) {
	activeCfg := cfg.GetActiveProviderConfig()
	p := provider.FromPreset(cfg.ActiveProvider, activeCfg.APIKey, activeCfg.ProviderURL)
	p.Model = activeCfg.Model
	p.SetThinkingEffort(activeCfg.ThinkingEffort)
	if activeCfg.ModelContextWindow > 0 {
		provider.SetModelContext(activeCfg.Model, activeCfg.ModelContextWindow)
	}
	if activeCfg.MainInputPrice > 0 {
		provider.SetModelPrice(activeCfg.Model, provider.ModelPrice{
			InputPricePerToken:       activeCfg.MainInputPrice,
			OutputPricePerToken:      activeCfg.MainOutputPrice,
			InputCachedPricePerToken: activeCfg.MainCacheReadPrice,
		})
	}
	lg, err := logger.New(logger.Options{
		HomeDir:        homeDir,
		TurnLogEnabled: cfg.TurnLogsEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	exec := fileio.NewLocalExecWithLogger(lg)
	sessionID := lg.SessionID()
	lg.Info("SESSION", "start", "version", "1.0")
	lg.Info("SESSION", "config", "provider", cfg.ActiveProvider, "model", activeCfg.Model)

	permStore := safety.NewPermissionStore()
	permStore.SetLogger(lg)
	safety.SetCapabilitiesLogger(lg)

	provider.SetLogger(lg)
	search.SetLogger(lg)
	js.SetLogger(lg)
	clipboard.SetLogger(lg)
	jsonrepair.SetLogger(lg)
	diff.SetLogger(lg)
	statemachine.SetGoalLogger(lg)
	statemachine.SetStepResultLogger(lg)
	fileio.SetSafetyLogger(lg)

	fileioSessionID := fileio.GenerateSessionID()
	var editor *fileio.Editor
	if ed, edErr := fileio.NewEditorWithLogger(fileioSessionID, lg); edErr != nil {
		lg.Warn("SESSION", "editor_init_error", "error", fmt.Sprintf("%v", edErr))
	} else {
		editor = ed
	}

	sm := statemachine.NewConversationStateMachine(lg)

	return &Session{
		cfg:          cfg,
		provider:     p,
		exec:         exec,
		editor:       editor,
		logger:       lg,
		permStore:    permStore,
		sm:           sm,
		asyncMgr:     fileio.NewAsyncCommandManagerWithLogger(lg),
		sessionID:    sessionID,
		sessionStart: time.Now(),
		cost:         NewCostAccumulator(lg),
		tokens:       NewTokenTracker(lg),
	}, nil
}

func (s *Session) HasMessages() bool {
	s.msgCounterMu.Lock()
	defer s.msgCounterMu.Unlock()
	return s.msgCounter > 0
}

func (s *Session) Close() {
	s.logger.Info("SESSION", "close", "has_messages", s.HasMessages())
	if s.editor != nil {
		s.editor.Close()
	}
	hasMsgs := s.HasMessages()

	if hasMsgs && s.cost != nil {
		s.persistCostSnapshot()
	}

	s.logger.Close()
	if !hasMsgs {
		s.logger.Remove()
	}
}

func (s *Session) persistCostSnapshot() {
	snap := s.cost.Snapshot()
	snapData, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		s.logger.Error("COST", "persist_error", "error", err.Error())
		return
	}

	costPath := filepath.Join(s.logger.SessionDir(), "cost.json")
	if err := os.WriteFile(costPath, snapData, 0o644); err != nil {
		s.logger.Error("COST", "persist_error", "error", err.Error())
		return
	}

	s.logger.Info("COST", "persisted", "path", costPath, "total_cost", snap.TotalCost,
		"prompt_tokens", snap.TotalPromptTokens, "completion_tokens", snap.TotalCompletionTokens)
}

func (c *Config) GetActiveProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	if cfg, ok := c.Providers[c.ActiveProvider]; ok {
		return cfg
	}
	return ProviderConfig{}
}

func (c *Config) SetActiveProviderConfig(providerID string, cfg ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	c.Providers[providerID] = cfg
}

func (c *Config) EffectiveProviderID() string {
	if c.ActiveProvider != "" {
		return c.ActiveProvider
	}
	return "openrouter"
}

func (s *Session) SessionDir() string {
	return s.logger.SessionDir()
}

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) Config() *Config { return s.cfg }

func (s *Session) newAgent() *agent.Agent {
	activeCfg := s.cfg.GetActiveProviderConfig()

	p := s.provider
	p.Model = activeCfg.Model
	p.SetThinkingEffort(activeCfg.ThinkingEffort)

	a := agent.NewAgent(p, activeCfg.Model, s.logger)

	a.Yolo = s.yolo
	a.AsyncMgr = s.asyncMgr

	a.ToolFn = func(tool string, params map[string]any) (tools.ToolResult, error) {
		switch tool {
		case "plan":
			objective := getStrParam(params, "objective")
			if objective == "" {
				objective = "multi-step plan"
			}

			rawSteps, ok := params["steps"]
			if !ok {
				return tools.ToolResult{}, fmt.Errorf("plan: missing 'steps' parameter")
			}
			stepsJSON, err := json.Marshal(rawSteps)
			if err != nil {
				return tools.ToolResult{}, fmt.Errorf("plan: failed to marshal steps: %w", err)
			}
			var steps []map[string]any
			if err := json.Unmarshal(stepsJSON, &steps); err != nil {
				return tools.ToolResult{}, fmt.Errorf("plan: invalid steps format: %w", err)
			}

			state := s.sm.GetOrCreateState(s.sessionID)
			if _, exists := state.GetFromContext("plan_step_statuses"); exists {
				return tools.ToolResult{}, fmt.Errorf("plan: a plan already exists. Complete or abandon the current plan before creating a new one.")
			}

			goal := s.sm.CreateGoal(s.sessionID, "plan: "+objective, objective, statemachine.PriorityNormal, false)

			stepStatuses := make(map[string]string)
			stepLabels := make(map[string]string)
			for _, step := range steps {
				id, _ := step["id"].(string)
				desc, _ := step["description"].(string)
				stepLabels[id] = desc
				stepStatuses[id] = "pending"
			}
			state.AddToContext("plan_steps", steps)
			state.AddToContext("plan_step_statuses", stepStatuses)
			state.AddToContext("plan_step_labels", stepLabels)
			state.AddToContext("plan_goal_id", goal.ID)

			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			return tools.ToolResult{Content: status}, nil

		case "mark_step_complete":
			stepIDs := resolveStepIDs(params["step_id"])
			if len(stepIDs) == 0 {
				return tools.ToolResult{}, fmt.Errorf("mark_step_complete: missing 'step_id'")
			}
			state := s.sm.GetOrCreateState(s.sessionID)

			for _, stepID := range stepIDs {
				if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
					if m, ok := raw.(map[string]string); ok {
						if _, exists := m[stepID]; !exists {
							return tools.ToolResult{}, fmt.Errorf("mark_step_complete: step %q not found in plan", stepID)
						}
						m[stepID] = "completed"
						state.AddToContext("plan_step_statuses", m)
					}
				}
			}

			activateIDs := resolveStepIDs(params["activate_steps"])
			for _, actID := range activateIDs {
				if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
					if m, ok := raw.(map[string]string); ok {
						if _, exists := m[actID]; !exists {
							return tools.ToolResult{}, fmt.Errorf("mark_step_complete: activate step %q not found in plan", actID)
						}
						if m[actID] == "pending" || m[actID] == "active" {
							m[actID] = "doing"
							state.AddToContext("plan_step_statuses", m)
						}
					}
				}
			}

			allDone := false
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok && len(m) > 0 {
					allDone = true
					for _, st := range m {
						if st != "completed" && st != "failed" {
							allDone = false
							break
						}
					}
				}
			}
			if allDone {
				if rawGoalID, ok := state.GetFromContext("plan_goal_id"); ok {
					if goalID, ok := rawGoalID.(string); ok && goalID != "" {
						s.sm.CompleteGoalByID(s.sessionID, goalID, map[string]any{"status": "plan_complete"})
					}
				}
				state.AddToContext("plan_steps", nil)
				state.AddToContext("plan_step_statuses", nil)
				state.AddToContext("plan_step_labels", nil)
				state.AddToContext("plan_goal_id", nil)
			}

			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return tools.ToolResult{Content: status}, nil
			}
			var resultParts []string
			if len(stepIDs) == 1 {
				resultParts = append(resultParts, fmt.Sprintf("Step %q marked as complete.", stepIDs[0]))
			} else {
				resultParts = append(resultParts, fmt.Sprintf("Steps %v marked as complete.", stepIDs))
			}
			if len(activateIDs) > 0 {
				resultParts = append(resultParts, fmt.Sprintf("Step %v marked as doing.", activateIDs))
			}
			return tools.ToolResult{Content: strings.Join(resultParts, " ")}, nil

		case "mark_step_doing":
			stepIDs := resolveStepIDs(params["step_id"])
			if len(stepIDs) == 0 {
				return tools.ToolResult{}, fmt.Errorf("mark_step_doing: missing 'step_id'")
			}
			state := s.sm.GetOrCreateState(s.sessionID)

			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					for _, stepID := range stepIDs {
						if _, exists := m[stepID]; !exists {
							return tools.ToolResult{}, fmt.Errorf("mark_step_doing: step %q not found in plan", stepID)
						}
						if m[stepID] == "pending" || m[stepID] == "active" {
							m[stepID] = "doing"
						}
					}
					state.AddToContext("plan_step_statuses", m)
				}
			}

			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return tools.ToolResult{Content: status}, nil
			}
			if len(stepIDs) == 1 {
				return tools.ToolResult{Content: fmt.Sprintf("Step %q marked as doing.", stepIDs[0])}, nil
			}
			return tools.ToolResult{Content: fmt.Sprintf("Steps %v marked as doing.", stepIDs)}, nil

		case "mark_step_failed":
			stepID := getStrParam(params, "step_id")
			reason := getStrParam(params, "reason")
			if stepID == "" {
				return tools.ToolResult{}, fmt.Errorf("mark_step_failed: missing 'step_id'")
			}
			if reason == "" {
				reason = "unknown error"
			}
			state := s.sm.GetOrCreateState(s.sessionID)
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					if _, exists := m[stepID]; !exists {
						return tools.ToolResult{}, fmt.Errorf("mark_step_failed: step %q not found in plan", stepID)
					}
					m[stepID] = "failed"
					state.AddToContext("plan_step_statuses", m)
				}
			}
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return tools.ToolResult{Content: status}, nil
			}
			return tools.ToolResult{Content: fmt.Sprintf("Step %q marked as failed: %s", stepID, reason)}, nil

		case "report_blocker":
			blockerType := getStrParam(params, "blocker_type")
			message := getStrParam(params, "message")
			if message == "" {
				return tools.ToolResult{}, fmt.Errorf("report_blocker: missing 'message'")
			}
			var bt statemachine.BlockingConditionType
			switch blockerType {
			case "missing_info":
				bt = statemachine.BlockMissingParameter
			case "confirmation_needed":
				bt = statemachine.BlockConfirmationNeeded
			case "api_error":
				bt = statemachine.BlockExternalAPIError
			case "ambiguous":
				bt = statemachine.BlockAmbiguousInput
			case "auth_required":
				bt = statemachine.BlockAuthRequired
			default:
				bt = statemachine.BlockConfirmationNeeded
			}
			blocker := &statemachine.BlockingCondition{
				Type:    bt,
				Message: message,
			}
			s.sm.PauseWithBlocker(s.sessionID, blocker)
			state := s.sm.GetOrCreateState(s.sessionID)
			if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := statuses.(map[string]string); ok {
					for id, st := range m {
						if st == "doing" || st == "active" {
							m[id] = "blocked"
						}
					}
					state.AddToContext("plan_step_statuses", m)
				}
			}
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return tools.ToolResult{Content: status}, nil
			}
			return tools.ToolResult{Content: fmt.Sprintf("Blocker reported: [%s] %s", blockerType, message)}, nil

		case "resolve_blocker":
			resolved := s.sm.TryResolveBlocker(s.sessionID, getStrParam(params, "message"))
			if resolved {
				state := s.sm.GetOrCreateState(s.sessionID)
				if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
					if m, ok := statuses.(map[string]string); ok {
						for id, st := range m {
							if st == "blocked" {
								m[id] = "doing"
							}
						}
						state.AddToContext("plan_step_statuses", m)
					}
				}
				status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
				if status != "" {
					return tools.ToolResult{Content: status}, nil
				}
				return tools.ToolResult{Content: "Blocker resolved. Plan execution can resume."}, nil
			}
			return tools.ToolResult{Content: "No active blocker to resolve."}, nil

		case "list_goal_status":
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status == "" {
				return tools.ToolResult{Content: "No active goals or plans."}, nil
			}
			return tools.ToolResult{Content: status}, nil

		case "async_execute_command":
			command := getStrParam(params, "command")
			handle := getStrParam(params, "handle")
			if command == "" {
				return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'command'")
			}
			if handle == "" {
				return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'handle'")
			}
			cwd := getStrParam(params, "cwd")
			timeout := tools.GetIntArgAny(params, "timeout_seconds", 0)
			info, err := s.asyncMgr.Start(handle, command, cwd, timeout)
			if err != nil {
				return tools.ToolResult{}, fmt.Errorf("async_execute_command: %w", err)
			}
			return tools.ToolResult{Content: fmt.Sprintf("Started async command [%s]: PID %d, status=%s, command=%q", handle, info.PID, info.Status, command)}, nil

		case "check_async_command":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return tools.ToolResult{}, fmt.Errorf("check_async_command: missing 'handle'")
			}
			info, ok := s.asyncMgr.Check(handle)
			if !ok {
				return tools.ToolResult{Content: fmt.Sprintf("Async command [%s] not found — it may have already been cleaned up or never started.", handle)}, nil
			}
			dur := info.Duration.Truncate(time.Second).String()
			status := fmt.Sprintf("Handle: %s\nPID: %d\nStatus: %s\nExit code: %d\nDuration: %s", info.Handle, info.PID, info.Status, info.ExitCode, dur)
			if info.Error != "" {
				status += "\nError: " + info.Error
			}
			return tools.ToolResult{Content: status}, nil

		case "kill_async_command":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return tools.ToolResult{}, fmt.Errorf("kill_async_command: missing 'handle'")
			}
			if err := s.asyncMgr.Kill(handle); err != nil {
				return tools.ToolResult{}, fmt.Errorf("kill_async_command: %w", err)
			}
			return tools.ToolResult{Content: fmt.Sprintf("Async command [%s] killed.", handle)}, nil

		case "read_async_output":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return tools.ToolResult{}, fmt.Errorf("read_async_output: missing 'handle'")
			}
			sinceByte := tools.GetIntArgAny(params, "since_byte", 0)
			out, nextByte, err := s.asyncMgr.ReadOutput(handle, sinceByte)
			if err != nil {
				return tools.ToolResult{}, fmt.Errorf("read_async_output: %w", err)
			}
			if out == "" {
				return tools.ToolResult{Content: fmt.Sprintf("[%s] next_byte=%d (no new output since byte %d)", handle, nextByte, sinceByte)}, nil
			}
			return tools.ToolResult{Content: fmt.Sprintf("[%s output from byte %d]\n%s\n[next_byte=%d]", handle, sinceByte, out, nextByte)}, nil

		case "wait":
			seconds := tools.GetFloatArgAny(params, "seconds", 0)
			if seconds <= 0 {
				return tools.ToolResult{}, fmt.Errorf("wait: seconds must be > 0")
			}
			s.logger.Info("TOOL", "wait", "seconds", seconds)
			time.Sleep(time.Duration(seconds * float64(time.Second)))
			return tools.ToolResult{Content: fmt.Sprintf("Waited %.1f seconds.", seconds)}, nil

		default:
			if tool == "execute_command" && !s.yolo {
				cmd := getStrParam(params, "command")
				sr := safety.CheckCommandSafety(cmd, s.logger)
				if sr.Risk.IsAlwaysBlocked() {
					errMsg := fmt.Sprintf("Error: [SAFETY] %s\nSuggestion: %s", sr.Reason, sr.Suggestion)
					s.logger.Warn("SAFETY", "blocked", "command", cmd, "reason", sr.Reason)
					return tools.ToolResult{Content: errMsg}, fmt.Errorf("%s", errMsg)
				}
				if sr.Risk.NeedsConfirmation() {
					s.logger.Info("SAFETY", "risky", "command", cmd, "risk", sr.Risk.String())
				}
			}
			result := tools.ExecuteWithJSON(tool, params, s.exec, s.editor, s.yolo, s.logger)
			if strings.HasPrefix(result.Content, "Error:") {
				return result, fmt.Errorf("%s", result.Content)
			}
			return result, nil
		}
	}
	return a
}

func (s *Session) SetYolo(enabled bool) {
	s.yolo = enabled
	s.logger.Info("SAFETY", "yolo", "enabled", enabled)
}

func (s *Session) HandleUndo(cmd string) tui.UndoResultMsg {
	l := s.logger
	l.Info("INPUT", "undo", "cmd", cmd)
	if s.editor == nil {
		l.Warn("EDITOR", "undo_no_editor", "cmd", cmd)
		return tui.UndoResultMsg{Error: "file editor not available"}
	}
	switch {
	case cmd == "/undo":
		l.Info("JOURNAL", "undo_last", "journal_len", len(s.editor.Log()))
		if err := s.editor.UndoLast(); err != nil {
			l.Error("JOURNAL", "undo_failed", "error", err.Error())
			return tui.UndoResultMsg{Error: err.Error()}
		}
		journalLog := s.editor.Log()
		remaining := len(journalLog)
		l.Info("JOURNAL", "undo_ok", "remaining", remaining)
		desc := "Undo complete. No more operations to undo."
		if remaining > 0 {
			desc = fmt.Sprintf("Undo complete. %d operation(s) remaining in journal.", remaining)
		}
		return tui.UndoResultMsg{Desc: desc}
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
			return tui.UndoResultMsg{Error: err.Error()}
		}
		l.Info("JOURNAL", "undo_n_ok", "count", n)
		return tui.UndoResultMsg{Desc: fmt.Sprintf("Undid last %d file operation(s).", n)}
	case cmd == "/undo-all":
		totalOps := len(s.editor.Log())
		l.Info("JOURNAL", "undo_session", "ops_to_revert", totalOps)
		if err := s.editor.UndoSession(); err != nil {
			l.Error("JOURNAL", "undo_session_failed", "error", err.Error())
			return tui.UndoResultMsg{Error: err.Error()}
		}
		l.Info("JOURNAL", "undo_session_ok", "ops_reverted", totalOps)
		return tui.UndoResultMsg{Desc: "All file operations this session have been reverted."}
	case cmd == "/changes":
		files := s.editor.ChangedFiles()
		l.Info("EDITOR", "changed_files", "count", len(files))
		if len(files) == 0 {
			return tui.UndoResultMsg{Desc: "No file changes this session."}
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
		return tui.UndoResultMsg{Desc: sb.String()}
	}
	l.Warn("INPUT", "undo_unknown", "cmd", cmd)
	return tui.UndoResultMsg{}
}

func (s *Session) HandleSettingsSave(tuiSettings tui.Settings) error {
	l := s.logger
	s.mu.Lock()
	defer s.mu.Unlock()

	oldProvider := s.cfg.ActiveProvider
	oldModel := s.cfg.GetActiveProviderConfig().Model
	l.Info("CONFIG", "save_begin", "old_provider", oldProvider, "old_model", oldModel, "turn_logs", tuiSettings.TurnLogsEnabled)

	newCfg := settingsToConfig(tuiSettings, s.cfg)
	activeCfg := newCfg.GetActiveProviderConfig()
	if activeCfg.APIKey == "" {
		l.Warn("CONFIG", "save_blocked", "reason", "empty_api_key")
		return fmt.Errorf("API key cannot be empty — settings not saved")
	}

	providerChanged := s.cfg.ActiveProvider != newCfg.ActiveProvider
	modelChanged := activeCfg.Model != oldModel

	s.cfg = newCfg
	s.provider = provider.FromPreset(s.cfg.ActiveProvider, activeCfg.APIKey, activeCfg.ProviderURL)
	s.provider.Model = activeCfg.Model
	s.provider.SetThinkingEffort(activeCfg.ThinkingEffort)
	if activeCfg.ModelContextWindow > 0 {
		provider.SetModelContext(activeCfg.Model, activeCfg.ModelContextWindow)
		l.Debug("CONFIG", "ctx_window_set", "model", activeCfg.Model, "window", activeCfg.ModelContextWindow)
	}
	if activeCfg.MainInputPrice > 0 {
		provider.SetModelPrice(activeCfg.Model, provider.ModelPrice{
			InputPricePerToken:       activeCfg.MainInputPrice,
			OutputPricePerToken:      activeCfg.MainOutputPrice,
			InputCachedPricePerToken: activeCfg.MainCacheReadPrice,
		})
		l.Debug("CONFIG", "price_set", "model", activeCfg.Model, "input", activeCfg.MainInputPrice, "output", activeCfg.MainOutputPrice)
	}
	s.logger.SetTurnEnabled(tuiSettings.TurnLogsEnabled != "off")
	s.cfg.TurnLogsEnabled = tuiSettings.TurnLogsEnabled != "off"

	if providerChanged {
		l.Info("CONFIG", "provider_switch", "from", oldProvider, "to", newCfg.ActiveProvider)
	}
	if modelChanged {
		l.Info("CONFIG", "model_change", "from", oldModel, "to", activeCfg.Model)
	}
	l.Info("CONFIG", "save_complete", "provider", newCfg.ActiveProvider, "model", activeCfg.Model, "turn_logs", s.cfg.TurnLogsEnabled)
	return nil
}

func (s *Session) HandleFetchModels(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
	l := s.logger
	l.Info("MODEL", "fetch_begin", "provider", providerID)
	p := provider.FromPreset(providerID, apiKey, baseURL)
	models, err := provider.FetchModels(p)
	if err != nil {
		l.Error("MODEL", "fetch_error", "provider", providerID, "error", err.Error())
		return nil, err
	}
	l.Info("MODEL", "fetched", "provider", providerID, "count", len(models))

	// Enrich with models.dev data (context window + pricing).
	metas, mdErr := provider.FetchModelsDevAll()
	if mdErr != nil {
		l.Warn("MODEL", "meta_fetch_error", "error", mdErr.Error())
	}

	entries := make([]tui.ModelEntry, len(models))
	enriched := 0
	for i, m := range models {
		entry := tui.ModelEntry{
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

func (s *Session) Run(tuiInst *tui.TUI, blockResolveCh chan string) {
	l := s.logger
	l.Info("SESSION", "run_start", "session_id", s.sessionID)
	defer func() {
		if r := recover(); r != nil {
			l.Error("SESSION", "panic", "error", fmt.Sprintf("%v", r))
			tuiInst.SendAgentEvent("error", fmt.Sprintf("Internal error: %v", r))
		}
	}()

	cancelCh := tuiInst.CancelCh
	l.Debug("SESSION", "cancel_ch_setup", "channel", "cancelCh")

	go func() {
		l.Debug("MODEL", "models_dev_start")
		metas, err := provider.FetchModelsDevAll()
		if err != nil {
			l.Warn("MODEL", "load_error", "error", err.Error())
			return
		}
		provider.LoadModelMetas(metas)
		l.Info("MODEL", "load_ok", "count", len(metas))
	}()

	// Start polling async command status for TUI status bar
	asyncPollingStop := make(chan struct{})
	defer close(asyncPollingStop)
	go func() {
		l.Debug("ASYNC", "poll_start", "interval", "1s")
		tick := time.NewTicker(1 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				cmds := s.asyncMgr.List()
				if len(cmds) > 0 {
					tuiInst.SendAgentEvent("async_command_status", cmds)
				} else {
					tuiInst.SendAgentEvent("async_command_status", []fileio.AsyncCmdInfo{})
				}
			case <-asyncPollingStop:
				l.Debug("ASYNC", "poll_stop")
				return
			}
		}
	}()

	for userText := range tuiInst.InputCh {
		select {
		case <-cancelCh:
			l.Debug("CANCEL", "cancel_drained", "iteration", "start_of_loop")
		default:
		}

		activeCfg := s.cfg.GetActiveProviderConfig()
		if activeCfg.APIKey == "" {
			l.Warn("INPUT", "no_api_key")
			tuiInst.SendAgentEvent("error", "No API key configured. Press Ctrl+S to open settings.")
			continue
		}

		s.blockMu.Lock()
		isBlocked := s.pendingBlock
		s.pendingBlock = false
		s.blockMu.Unlock()
		if isBlocked {
			l.Debug("BLOCK", "block_resolve", "text_len", len(userText))
			s.blockRespMu.Lock()
			s.lastBlockResp = userText
			s.blockRespMu.Unlock()
			blockResolveCh <- userText
			continue
		}

		s.msgCounterMu.Lock()
		s.msgCounter++
		currentMsgID := fmt.Sprintf("msg-%d", s.msgCounter)
		s.msgCounterMu.Unlock()
		l.Info("INPUT", "received", "len", len(userText), "msg_id", currentMsgID, "is_blocked", isBlocked)

		s.msgsMu.Lock()
		s.msgs = append(s.msgs, provider.ChatMessage{Role: "user", Content: userText})
		msgsCopy := make([]provider.ChatMessage, len(s.msgs))
		copy(msgsCopy, s.msgs)
		s.msgsMu.Unlock()
		l.Info("CONV", "user_msg", "msg_id", currentMsgID, "len", len(userText))

		l.Info("AGENT", "loop_start", "msg_id", currentMsgID)

		if s.editor != nil {
			s.editor = s.editor.WithMessage(currentMsgID)
		}

		// Update provider model & thinking effort before each run (settings may have changed)
		s.provider.Model = activeCfg.Model
		s.provider.SetThinkingEffort(activeCfg.ThinkingEffort)

		ag := s.newAgent()
		var finalContent string
		prompt := agent.BuildSystemPrompt()

		ctx, cancel := context.WithCancel(context.Background())
		ag.Ctx = ctx
		go func() {
			select {
			case <-cancelCh:
				l.Debug("CANCEL", "ctx_cancelled", "msg_id", currentMsgID)
				cancel()
			case <-ctx.Done():
			}
		}()
		activeCfg = s.cfg.GetActiveProviderConfig() // refresh for emit closure

		emit := func(evt agent.AgentEvent) {
			if evt.Type == "blocking" {
				data, _ := evt.Data.(map[string]string)
				if data != nil {
					op := safety.CRUDEOp(data["operation"])
					tool := data["tool"]
					target := data["target"]
					if s.permStore.IsAlwaysAllowed(op, tool, target) {
						l.Debug("PERM", "auto_allowed", "operation", op, "tool", tool, "target", target)
						blockResolveCh <- "allow"
						tuiInst.SendAgentEventWithID("blocking_auto", evt.Data, evt.AgentID)
						return
					}
				}
				if data != nil && data["risk"] != "" {
					riskStr := data["risk"]
					for _, r := range []safety.CommandRisk{
						safety.RiskDestructive, safety.RiskNetwork,
						safety.RiskProjectWrite, safety.RiskUnknown,
					} {
						if r.String() == riskStr {
							sr := &safety.CommandSafetyResult{Risk: r, BaseCommand: data["tool"]}
							if s.permStore.IsCommandAllowed(sr) {
								l.Debug("PERM", "command_auto_allowed", "risk", riskStr, "tool", data["tool"])
								blockResolveCh <- "always"
								tuiInst.SendAgentEventWithID("blocking_auto", evt.Data, evt.AgentID)
								return
							}
							break
						}
					}
				}
				l.Info("BLOCK", "prompt", "operation", data["operation"], "tool", data["tool"], "target", data["target"], "question", data["question"])
				s.blockMu.Lock()
				s.pendingBlock = true
				s.blockMu.Unlock()
				tuiInst.SendAgentEventWithID(evt.Type, evt.Data, evt.AgentID)
				return
			}
			if evt.Type == "confirmation_always" {
				if data, ok := evt.Data.(map[string]string); ok {
					opStr := data["operation"]
					tool := data["tool"]
					target := data["target"]
					l.Info("PERM", "grant", "operation", opStr, "tool", tool, "target", target)

					switch tool {
					case "write_file", "edit_file", "delete_file", "move_file", "copy_file":
						s.permStore.GrantAlways(safety.CRUDEOp(opStr), tool, target)
					default:
						for _, r := range []safety.CommandRisk{
							safety.RiskDestructive, safety.RiskNetwork,
							safety.RiskProjectWrite, safety.RiskUnknown,
						} {
							if r.String() == opStr {
								s.permStore.GrantCommandRisk(r)
								l.Debug("PERM", "risk_granted", "risk", opStr)
								break
							}
						}
					}
				}
				return
			}
			tuiInst.SendAgentEventWithID(evt.Type, evt.Data, evt.AgentID)
			if evt.Type == "usage" {
				model := activeCfg.Model
				var promptT, compT, cachedT int
				isEstimated := false
				switch d := evt.Data.(type) {
				case map[string]int:
					promptT = d["prompt"]
					compT = d["completion"]
					cachedT = d["prompt_cached"]
				case map[string]any:
					if v, ok := d["prompt"].(float64); ok {
						promptT = int(v)
					} else if v, ok := d["prompt"].(int); ok {
						promptT = v
					}
					if v, ok := d["completion"].(float64); ok {
						compT = int(v)
					} else if v, ok := d["completion"].(int); ok {
						compT = v
					}
					if v, ok := d["prompt_cached"].(float64); ok {
						cachedT = int(v)
					} else if v, ok := d["prompt_cached"].(int); ok {
						cachedT = v
					}
					if v, ok := d["estimated"].(bool); ok {
						isEstimated = v
					}
				}
				if promptT > 0 || compT > 0 {
					s.cost.RecordUsage(model, promptT, compT, cachedT)
					if !isEstimated {
						s.tokens.Record(promptT, compT)
					}
					var perTurnCost float64
					if c, ok := evt.Data.(map[string]any); ok {
						if v, ok := c["cost"].(float64); ok {
							perTurnCost = v
						}
					}
					sessionTotal := s.cost.Snapshot().TotalCost
					l.Info("COST", "turn_usage", "model", model, "prompt", promptT, "completion", compT, "cached", cachedT, "turn_cost", perTurnCost, "session_total", sessionTotal, "estimated", isEstimated)
				}
			}

			if evt.Type == "done" {
				if content, ok := evt.Data.(string); ok {
					finalContent = content
				} else if m, ok := evt.Data.(map[string]any); ok {
					if c, ok := m["content"].(string); ok {
						finalContent = c
					}
				}
			}
		}
		ag.Emit = emit
		agentMsgs := ag.Run(ctx, msgsCopy, prompt, emit, blockResolveCh, s.sm, s.sessionID)

		cancel()

		l.Info("AGENT", "loop_end", "len", len(finalContent))
		s.blockMu.Lock()
		wasPending := s.pendingBlock
		s.pendingBlock = false
		s.blockMu.Unlock()
		if wasPending {
			l.Debug("BLOCK", "pending_cleared", "msg_id", currentMsgID)
		}

		s.blockRespMu.Lock()
		if s.lastBlockResp != "" && finalContent == "" {
			finalContent = s.lastBlockResp
			l.Debug("BLOCK", "block_resp_used", "len", len(s.lastBlockResp))
		}
		s.lastBlockResp = ""
		s.blockRespMu.Unlock()

		s.msgsMu.Lock()
		var persisted []provider.ChatMessage
		for _, m := range agentMsgs {
			if m.Role == "system" {
				continue
			}
			persisted = append(persisted, m)
		}
		preSanitize := len(persisted)
		persisted = sanitizeMessages(persisted)
		removedCount := preSanitize - len(persisted)
		if removedCount > 0 {
			l.Debug("SANITIZE", "messages_removed", "count", removedCount, "remaining", len(persisted))
		}
		ctxWindow := provider.LookupContextWindow(activeCfg.Model)
		preTruncate := len(persisted)
		persisted = truncateMessages(persisted, ctxWindow, activeCfg.Model)
		if len(persisted) < preTruncate {
			l.Info("TRUNCATE", "messages", "before", preTruncate, "after", len(persisted), "window", ctxWindow)
		}
		if len(persisted) > 0 {
			s.msgs = persisted
		} else if finalContent != "" {
			s.msgs = append(s.msgs, provider.ChatMessage{Role: "assistant", Content: finalContent})
		}
		s.msgsMu.Unlock()
		if finalContent != "" {
			l.Info("CONV", "assistant_msg", "msg_id", currentMsgID, "len", len(finalContent))
		}
	}
	l.Info("SESSION", "run_exit")
}

func (s *Session) ModelName() string {
	return s.cfg.GetActiveProviderConfig().Model
}

func (s *Session) Logger() *logger.Logger { return s.logger }

func ConfigToSettings(cfg *Config) tui.Settings {
	providerID := cfg.EffectiveProviderID()

	providerURL := ""
	apiKey := ""
	model := ""
	thinkingEffort := ""

	if pc, ok := cfg.Providers[providerID]; ok {
		providerURL = pc.ProviderURL
		apiKey = pc.APIKey
		model = pc.Model
		thinkingEffort = pc.ThinkingEffort
	}

	if providerURL == "" {
		p := provider.GetPreset(providerID)
		if p == nil {
			p = provider.GetPreset("openrouter")
		}
		providerURL = p.BaseURL
	}

	ctxWindow := 0
	mainInputPrice := 0.0
	mainOutputPrice := 0.0
	mainCacheReadPrice := 0.0
	if pc, ok := cfg.Providers[providerID]; ok {
		ctxWindow = pc.ModelContextWindow
		mainInputPrice = pc.MainInputPrice
		mainOutputPrice = pc.MainOutputPrice
		mainCacheReadPrice = pc.MainCacheReadPrice
	}

	turnLogs := "on"
	if !cfg.TurnLogsEnabled {
		turnLogs = "off"
	}

	return tui.Settings{
		APIKey:                   apiKey,
		Model:                    model,
		ModelContextWindow:       ctxWindow,
		MainInputPrice:           mainInputPrice,
		MainOutputPrice:          mainOutputPrice,
		MainCacheReadPrice:       mainCacheReadPrice,
		ProviderID:               providerID,
		ProviderURL:              providerURL,
		ProviderAPIKey:           apiKey,
		ThinkingEffort:           thinkingEffort,
		TurnLogsEnabled:          turnLogs,
	}
}

func settingsToConfig(s tui.Settings, base *Config) *Config {
	providerID := s.ProviderID
	if providerID == "" {
		providerID = base.EffectiveProviderID()
	}

	apiKey := s.APIKey
	if apiKey == "" {
		apiKey = s.ProviderAPIKey
	}

	providers := make(map[string]ProviderConfig, len(base.Providers))
	for k, v := range base.Providers {
		providers[k] = v
	}
	providers[providerID] = ProviderConfig{
		APIKey:                   apiKey,
		Model:                    s.Model,
		ModelContextWindow:       s.ModelContextWindow,
		MainInputPrice:           s.MainInputPrice,
		MainOutputPrice:          s.MainOutputPrice,
		MainCacheReadPrice:       s.MainCacheReadPrice,
		ProviderURL:              s.ProviderURL,
		ThinkingEffort:           s.ThinkingEffort,
	}

	return &Config{
		ActiveProvider:  providerID,
		Providers:      providers,
		TurnLogsEnabled: s.TurnLogsEnabled != "off",
	}
}

func getStrParam(params map[string]any, key string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func resolveStepIDs(val any) []string {
	if val == nil {
		return nil
	}
	if s, ok := val.(string); ok && s != "" {
		parts := strings.Split(s, ",")
		var ids []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				ids = append(ids, p)
			}
		}
		return ids
	}
	if arr, ok := val.([]any); ok {
		var ids []string
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
		return ids
	}
	if s := fmt.Sprintf("%v", val); s != "" && s != "<nil>" {
		return []string{s}
	}
	return nil
}

func sanitizeMessages(msgs []provider.ChatMessage) []provider.ChatMessage {
	for {
		if len(msgs) == 0 {
			break
		}
		last := msgs[len(msgs)-1]
		if last.Role == "tool" {
			if len(msgs) >= 2 && msgs[len(msgs)-2].Role == "assistant" {
				prev := msgs[len(msgs)-2]
				if len(prev.ToolCalls) > 0 && allCommTools(prev.ToolCalls) {
					content := prev.Content
					toolCount := 0
					for i := len(msgs) - 1; i >= 0 && msgs[i].Role == "tool"; i-- {
						toolCount++
						if content == "" && msgs[i].Content != "" {
							content = msgs[i].Content
						}
					}
					for len(msgs) > 0 && msgs[len(msgs)-1].Role == "tool" {
						msgs = msgs[:len(msgs)-1]
					}
					msgs[len(msgs)-1].ToolCalls = nil
					if content != "" {
						msgs[len(msgs)-1].Content = content
					}
					break
				}
			}
			msgs = msgs[:len(msgs)-1]
			continue
		}
		if last.Role == "assistant" && len(last.ToolCalls) > 0 {
			expectedIDs := make(map[string]bool)
			for _, tc := range last.ToolCalls {
				expectedIDs[tc.ID] = true
			}
			i := len(msgs) - 1
			for i+1 < len(msgs) && msgs[i+1].Role == "tool" {
				delete(expectedIDs, msgs[i+1].ToolCallID)
				i++
			}
			if len(expectedIDs) > 0 {
				msgs = msgs[:len(msgs)-1]
				continue
			}
		}
		break
	}
	return msgs
}

func allCommTools(toolCalls []provider.ToolCall) bool {
	for _, tc := range toolCalls {
		if tc.Function.Name != "reply" {
			return false
		}
	}
	return true
}

func truncateMessages(msgs []provider.ChatMessage, ctxWindow int, model string) []provider.ChatMessage {
	if ctxWindow <= 0 || len(msgs) <= 2 {
		return msgs
	}

	tokenBudget := int(float64(ctxWindow) * 0.75)

	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		msgTokens := provider.EstimateMessageTokens(msgs[i], model)
		if used+msgTokens > tokenBudget && i > 0 {
			summary := provider.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("[Earlier conversation truncated — %d messages omitted, ~%d tokens saved]", i, used),
			}
			return append([]provider.ChatMessage{summary}, msgs[i+1:]...)
		}
		used += msgTokens
	}
	return msgs
}

func ResolveUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	if name := os.Getenv("LOGNAME"); name != "" {
		return name
	}
	return "user"
}
