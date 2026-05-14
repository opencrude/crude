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
	"crude/fileio"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"
	"crude/tui"
)

type Config struct {
	ActiveProvider string
	Providers      map[string]ProviderConfig
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

	// Oracle model metadata
	OracleModel              string  `json:"oracle_model,omitempty"`
	OracleModelContextWindow int    `json:"oracle_model_context_window,omitempty"`
	OracleInputPrice         float64 `json:"oracle_input_price,omitempty"`
	OracleOutputPrice        float64 `json:"oracle_output_price,omitempty"`
	OracleCacheReadPrice     float64 `json:"oracle_cache_read_price,omitempty"`
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
	if activeCfg.OracleModelContextWindow > 0 {
		provider.SetModelContext(activeCfg.OracleModel, activeCfg.OracleModelContextWindow)
	}
	if activeCfg.OracleInputPrice > 0 {
		provider.SetModelPrice(activeCfg.OracleModel, provider.ModelPrice{
			InputPricePerToken:       activeCfg.OracleInputPrice,
			OutputPricePerToken:      activeCfg.OracleOutputPrice,
			InputCachedPricePerToken: activeCfg.OracleCacheReadPrice,
		})
	}
	exec := fileio.NewLocalExec()

	sessionDir := filepath.Join(homeDir, ".crude", "sessions")
	sessionID := fmt.Sprintf("session-%s", time.Now().Format("20060102-150405"))
	lg, err := logger.New(logger.Config{
		SessionDir: sessionDir,
		SessionID:  sessionID,
		Console:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	lg.Info("session.start", "version", "1.0")
	lg.Info("session.config", "provider", cfg.ActiveProvider, "model", activeCfg.Model)

	permStore := safety.NewPermissionStore()

	fileioSessionID := fileio.GenerateSessionID()
	var editor *fileio.Editor
	if ed, edErr := fileio.NewEditorWithLogger(fileioSessionID, lg); edErr != nil {
		lg.Info("agent.reflection", "content", fmt.Sprintf("editor init error: %v", edErr))
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
		asyncMgr:     fileio.NewAsyncCommandManager(),
		sessionID:    sessionID,
		sessionStart: time.Now(),
	}, nil
}

func (s *Session) HasMessages() bool {
	s.msgCounterMu.Lock()
	defer s.msgCounterMu.Unlock()
	return s.msgCounter > 0
}

func (s *Session) Close() {
	if s.editor != nil {
		s.editor.Close()
	}
	hasMsgs := s.HasMessages()
	s.logger.Info("session.end", "duration_ms", 0)
	s.logger.Close()
	if !hasMsgs {
		os.Remove(s.logger.Path())
	}
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

func (s *Session) LogPath() string {
	return s.logger.Path()
}

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) Config() *Config { return s.cfg }

func (s *Session) newAgent() *agent.Agent {
	activeCfg := s.cfg.GetActiveProviderConfig()

	p := s.provider
	p.Model = activeCfg.Model
	p.SetThinkingEffort(activeCfg.ThinkingEffort)

	a := agent.NewAgent(p, activeCfg.Model, s.logger)

	oracleModel := activeCfg.OracleModel
	oracleProvider := s.provider
	if oracleModel == "" {
		oracleModel = activeCfg.Model
	}
	a.OracleProvider = oracleProvider
	a.OracleModel = oracleModel
	a.Yolo = s.yolo
	a.AsyncMgr = s.asyncMgr

	a.ToolFn = func(tool string, params map[string]any) (string, error) {
		switch tool {
		case "consult_oracle":
			s.logger.Info("oracle.consult", "task", params["task"], "decision_point", params["decision_point"])
			query := agent.OracleQuery{
				Task:          getStrParam(params, "task"),
				Context:       getStrParam(params, "context"),
				DecisionPoint: getStrParam(params, "decision_point"),
				Constraints:   getStrParam(params, "constraints"),
			}
			resp, err := agent.ConsultOracle(a.Ctx, oracleProvider, oracleModel, query, a.Emit)
			if err != nil {
				s.logger.Error("oracle.error", "error", err.Error())
				return "", fmt.Errorf("oracle consultation failed: %w", err)
			}
			return agent.FormatOracleResponse(resp), nil

		case "plan":
			objective := getStrParam(params, "objective")
			if objective == "" {
				objective = "multi-step plan"
			}

			rawSteps, ok := params["steps"]
			if !ok {
				return "", fmt.Errorf("plan: missing 'steps' parameter")
			}
			stepsJSON, err := json.Marshal(rawSteps)
			if err != nil {
				return "", fmt.Errorf("plan: failed to marshal steps: %w", err)
			}
			var steps []map[string]any
			if err := json.Unmarshal(stepsJSON, &steps); err != nil {
				return "", fmt.Errorf("plan: invalid steps format: %w", err)
			}

			goal := s.sm.CreateGoal(s.sessionID, "plan: "+objective, objective, statemachine.PriorityNormal, false)
			state := s.sm.GetOrCreateState(s.sessionID)

			stepStatuses := make(map[string]string)
			stepLabels := make(map[string]string)
			for i, step := range steps {
				id, _ := step["id"].(string)
				desc, _ := step["description"].(string)
				stepLabels[id] = desc
				if i == 0 {
					stepStatuses[id] = "active"
				} else {
					stepStatuses[id] = "pending"
				}
			}
			state.AddToContext("plan_steps", steps)
			state.AddToContext("plan_step_statuses", stepStatuses)
			state.AddToContext("plan_step_labels", stepLabels)
			state.AddToContext("plan_goal_id", goal.ID)

			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			return status, nil

		case "mark_step_complete":
			stepID := getStrParam(params, "step_id")
			if stepID == "" {
				return "", fmt.Errorf("mark_step_complete: missing 'step_id'")
			}
			state := s.sm.GetOrCreateState(s.sessionID)

			wasActive := false
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					wasActive = m[stepID] == "active"
					m[stepID] = "completed"
					state.AddToContext("plan_step_statuses", m)
				}
			}

			if wasActive {
				if raw, ok := state.GetFromContext("plan_steps"); ok {
					if steps, ok := raw.([]map[string]any); ok {
						if rawStatuses, ok2 := state.GetFromContext("plan_step_statuses"); ok2 {
							if m, ok2 := rawStatuses.(map[string]string); ok2 {
								for _, step := range steps {
									id, _ := step["id"].(string)
									if m[id] == "pending" {
										m[id] = "active"
										break
									}
								}
								state.AddToContext("plan_step_statuses", m)
							}
						}
					}
				}
			}

			allDone := true
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					for _, st := range m {
						if st != "completed" && st != "failed" {
							allDone = false
							break
						}
					}
				}
			}
			if allDone {
				s.sm.CompleteCurrentGoal(s.sessionID, map[string]any{"status": "plan_complete"})
			}

			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return status, nil
			}
			return fmt.Sprintf("Step %q marked as complete.", stepID), nil

		case "mark_step_failed":
			stepID := getStrParam(params, "step_id")
			reason := getStrParam(params, "reason")
			if stepID == "" {
				return "", fmt.Errorf("mark_step_failed: missing 'step_id'")
			}
			if reason == "" {
				reason = "unknown error"
			}
			state := s.sm.GetOrCreateState(s.sessionID)
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					m[stepID] = "failed"
					state.AddToContext("plan_step_statuses", m)
				}
			}
			s.sm.FailCurrentGoal(s.sessionID, reason)
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return status, nil
			}
			return fmt.Sprintf("Step %q marked as failed: %s", stepID, reason), nil

		case "report_blocker":
			blockerType := getStrParam(params, "blocker_type")
			message := getStrParam(params, "message")
			if message == "" {
				return "", fmt.Errorf("report_blocker: missing 'message'")
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
						if st == "active" {
							m[id] = "blocked"
							break
						}
					}
					state.AddToContext("plan_step_statuses", m)
				}
			}
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status != "" {
				return status, nil
			}
			return fmt.Sprintf("Blocker reported: [%s] %s", blockerType, message), nil

		case "resolve_blocker":
			resolved := s.sm.TryResolveBlocker(s.sessionID, getStrParam(params, "message"))
			if resolved {
				state := s.sm.GetOrCreateState(s.sessionID)
				if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
					if m, ok := statuses.(map[string]string); ok {
						hasActive := false
						for _, st := range m {
							if st == "active" {
								hasActive = true
								break
							}
						}
						if !hasActive {
							for id, st := range m {
								if st == "blocked" {
									m[id] = "active"
									break
								}
							}
						}
						state.AddToContext("plan_step_statuses", m)
					}
				}
				status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
				if status != "" {
					return status, nil
				}
				return "Blocker resolved. Plan execution can resume.", nil
			}
			return "No active blocker to resolve.", nil

		case "list_goal_status":
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status == "" {
				return "No active goals or plans.", nil
			}
			return status, nil

		case "async_execute_command":
			command := getStrParam(params, "command")
			handle := getStrParam(params, "handle")
			if command == "" {
				return "", fmt.Errorf("async_execute_command: missing 'command'")
			}
			if handle == "" {
				return "", fmt.Errorf("async_execute_command: missing 'handle'")
			}
			cwd := getStrParam(params, "cwd")
			timeout := tools.GetIntArgAny(params, "timeout_seconds", 0)
			info, err := s.asyncMgr.Start(handle, command, cwd, timeout)
			if err != nil {
				return "", fmt.Errorf("async_execute_command: %w", err)
			}
			return fmt.Sprintf("Started async command [%s]: PID %d, status=%s, command=%q", handle, info.PID, info.Status, command), nil

		case "check_async_command":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return "", fmt.Errorf("check_async_command: missing 'handle'")
			}
			info, ok := s.asyncMgr.Check(handle)
			if !ok {
				return fmt.Sprintf("Async command [%s] not found — it may have already been cleaned up or never started.", handle), nil
			}
			dur := info.Duration.Truncate(time.Second).String()
			status := fmt.Sprintf("Handle: %s\nPID: %d\nStatus: %s\nExit code: %d\nDuration: %s", info.Handle, info.PID, info.Status, info.ExitCode, dur)
			if info.Error != "" {
				status += "\nError: " + info.Error
			}
			return status, nil

		case "kill_async_command":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return "", fmt.Errorf("kill_async_command: missing 'handle'")
			}
			if err := s.asyncMgr.Kill(handle); err != nil {
				return "", fmt.Errorf("kill_async_command: %w", err)
			}
			return fmt.Sprintf("Async command [%s] killed.", handle), nil

		case "read_async_output":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return "", fmt.Errorf("read_async_output: missing 'handle'")
			}
			sinceByte := tools.GetIntArgAny(params, "since_byte", 0)
			out, nextByte, err := s.asyncMgr.ReadOutput(handle, sinceByte)
			if err != nil {
				return "", fmt.Errorf("read_async_output: %w", err)
			}
			if out == "" {
				return fmt.Sprintf("[%s] next_byte=%d (no new output since byte %d)", handle, nextByte, sinceByte), nil
			}
			return fmt.Sprintf("[%s output from byte %d]\n%s\n[next_byte=%d]", handle, sinceByte, out, nextByte), nil

		case "wait":
			seconds := tools.GetFloatArgAny(params, "seconds", 0)
			if seconds <= 0 {
				return "", fmt.Errorf("wait: seconds must be > 0")
			}
			s.logger.Info("tool.wait", "seconds", seconds)
			time.Sleep(time.Duration(seconds * float64(time.Second)))
			return fmt.Sprintf("Waited %.1f seconds.", seconds), nil

		default:
			if tool == "execute_command" && !s.yolo {
				cmd := getStrParam(params, "command")
				sr := safety.CheckCommandSafety(cmd)
				if sr.Risk.IsAlwaysBlocked() {
					errMsg := fmt.Sprintf("Error: [SAFETY] %s\nSuggestion: %s", sr.Reason, sr.Suggestion)
					s.logger.Warn("safety.blocked", "command", cmd, "reason", sr.Reason)
					return errMsg, fmt.Errorf("%s", errMsg)
				}
				if sr.Risk.NeedsConfirmation() {
					s.logger.Info("safety.risky", "command", cmd, "risk", sr.Risk.String())
				}
			}
			result := tools.ExecuteWithJSON(tool, params, s.exec, s.editor, s.yolo)
			if strings.HasPrefix(result, "Error:") {
				return result, fmt.Errorf("%s", result)
			}
			return result, nil
		}
	}
	return a
}

func (s *Session) SetYolo(enabled bool) {
	s.yolo = enabled
	s.logger.Info("safety.yolo", "enabled", enabled)
}

func (s *Session) HandleUndo(cmd string) tui.UndoResultMsg {
	if s.editor == nil {
		return tui.UndoResultMsg{Error: "file editor not available"}
	}
	switch {
	case cmd == "/undo":
		if err := s.editor.UndoLast(); err != nil {
			return tui.UndoResultMsg{Error: err.Error()}
		}
		journalLog := s.editor.Log()
		desc := "Undo complete. No more operations to undo."
		if len(journalLog) > 0 {
			desc = fmt.Sprintf("Undo complete. %d operation(s) remaining in journal.", len(journalLog))
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
		if err := s.editor.UndoN(n); err != nil {
			return tui.UndoResultMsg{Error: err.Error()}
		}
		return tui.UndoResultMsg{Desc: fmt.Sprintf("Undid last %d file operation(s).", n)}
	case cmd == "/undo-all":
		if err := s.editor.UndoSession(); err != nil {
			return tui.UndoResultMsg{Error: err.Error()}
		}
		return tui.UndoResultMsg{Desc: "All file operations this session have been reverted."}
	case cmd == "/changes":
		files := s.editor.ChangedFiles()
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
	return tui.UndoResultMsg{}
}

func (s *Session) HandleSettingsSave(tuiSettings tui.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newCfg := settingsToConfig(tuiSettings, s.cfg)
	activeCfg := newCfg.GetActiveProviderConfig()
	if activeCfg.APIKey == "" {
		return fmt.Errorf("API key cannot be empty — settings not saved")
	}

	s.cfg = newCfg
	s.provider = provider.FromPreset(s.cfg.ActiveProvider, activeCfg.APIKey, activeCfg.ProviderURL)
	s.provider.Model = activeCfg.Model
	s.provider.SetThinkingEffort(activeCfg.ThinkingEffort)
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
	if activeCfg.OracleModelContextWindow > 0 {
		provider.SetModelContext(activeCfg.OracleModel, activeCfg.OracleModelContextWindow)
	}
	if activeCfg.OracleInputPrice > 0 {
		provider.SetModelPrice(activeCfg.OracleModel, provider.ModelPrice{
			InputPricePerToken:       activeCfg.OracleInputPrice,
			OutputPricePerToken:      activeCfg.OracleOutputPrice,
			InputCachedPricePerToken: activeCfg.OracleCacheReadPrice,
		})
	}
	return nil
}

func (s *Session) HandleFetchModels(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
	p := provider.FromPreset(providerID, apiKey, baseURL)
	models, err := provider.FetchModels(p)
	if err != nil {
		return nil, err
	}

	// Enrich with models.dev data (context window + pricing).
	metas, mdErr := provider.FetchModelsDevAll()

	entries := make([]tui.ModelEntry, len(models))
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
			}
		}
		entries[i] = entry
	}
	return entries, nil
}

func (s *Session) Run(tuiInst *tui.TUI, blockResolveCh chan string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("agent.panic", "recovered", fmt.Sprintf("%v", r))
			s.logger.Error("session.error", "error", fmt.Sprintf("panic: %v", r))
			tuiInst.SendAgentEvent("error", fmt.Sprintf("Internal error: %v", r))
		}
	}()

	cancelCh := tuiInst.CancelCh

	// Start polling async command status for TUI status bar
	asyncPollingStop := make(chan struct{})
	defer close(asyncPollingStop)
	go func() {
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
				return
			}
		}
	}()

	for userText := range tuiInst.InputCh {
		select {
		case <-cancelCh:
		default:
		}

		activeCfg := s.cfg.GetActiveProviderConfig()
		if activeCfg.APIKey == "" {
			tuiInst.SendAgentEvent("error", "No API key configured. Press Ctrl+S to open settings.")
			continue
		}

		s.blockMu.Lock()
		isBlocked := s.pendingBlock
		s.pendingBlock = false
		s.blockMu.Unlock()
		if isBlocked {
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

		s.msgsMu.Lock()
		s.msgs = append(s.msgs, provider.ChatMessage{Role: "user", Content: userText})
		msgsCopy := make([]provider.ChatMessage, len(s.msgs))
		copy(msgsCopy, s.msgs)
		s.msgsMu.Unlock()
		s.logger.Info("conv.user_msg", "content", userText, "msg_id", currentMsgID)

		s.logger.Info("agent.loop_start", "content", userText, "iteration", 0)

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
				cancel()
			case <-ctx.Done():
			}
		}()

		oracleEmit := func(evt agent.AgentEvent) {
			if evt.Type == "blocking" {
				data, _ := evt.Data.(map[string]string)
				if data != nil {
					op := safety.CRUDEOp(data["operation"])
					tool := data["tool"]
					target := data["target"]
					if s.permStore.IsAlwaysAllowed(op, tool, target) {
						blockResolveCh <- "allow"
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
								blockResolveCh <- "always"
								return
							}
							break
						}
					}
				}
				s.blockMu.Lock()
				s.pendingBlock = true
				s.blockMu.Unlock()
				tuiInst.SendAgentEvent(evt.Type, evt.Data)
				return
			}
			if evt.Type == "confirmation_always" {
				if data, ok := evt.Data.(map[string]string); ok {
					opStr := data["operation"]
					tool := data["tool"]
					target := data["target"]

					// File tool operations: store path-tree permission
					switch tool {
					case "write_file", "edit_file", "delete_file", "move_file", "copy_file", "delete_lines", "replace_lines":
						s.permStore.GrantAlways(safety.CRUDEOp(opStr), tool, target)
					default:
						// Command risk handling (existing)
						for _, r := range []safety.CommandRisk{
							safety.RiskDestructive, safety.RiskNetwork,
							safety.RiskProjectWrite, safety.RiskUnknown,
						} {
							if r.String() == opStr {
								s.permStore.GrantCommandRisk(r)
								break
							}
						}
					}
				}
				return
			}
			tuiInst.SendAgentEvent(evt.Type, evt.Data)
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
		ag.Emit = oracleEmit
		agentMsgs := ag.Run(ctx, msgsCopy, prompt, oracleEmit, blockResolveCh, s.sm, s.sessionID)

		cancel()

		s.logger.Info("agent.loop_end", "content", finalContent, "iteration", 1)
		s.blockMu.Lock()
		s.pendingBlock = false
		s.blockMu.Unlock()

		s.blockRespMu.Lock()
		if s.lastBlockResp != "" && finalContent == "" {
			finalContent = s.lastBlockResp
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
		persisted = sanitizeMessages(persisted)
		ctxWindow := provider.LookupContextWindow(activeCfg.Model)
		persisted = truncateMessages(persisted, ctxWindow)
		if len(persisted) > 0 {
			s.msgs = persisted
		} else if finalContent != "" {
			s.msgs = append(s.msgs, provider.ChatMessage{Role: "assistant", Content: finalContent})
		}
		s.msgsMu.Unlock()
		if finalContent != "" {
			s.logger.Info("conv.assistant_msg", "content", finalContent, "msg_id", currentMsgID)
		}
	}
}

func (s *Session) ModelName() string {
	return s.cfg.GetActiveProviderConfig().Model
}

func ConfigToSettings(cfg *Config) tui.Settings {
	providerID := cfg.EffectiveProviderID()

	providerURL := ""
	apiKey := ""
	model := ""
	oracleModel := ""
	thinkingEffort := ""

	if pc, ok := cfg.Providers[providerID]; ok {
		providerURL = pc.ProviderURL
		apiKey = pc.APIKey
		model = pc.Model
		oracleModel = pc.OracleModel
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
	oracleCtxWindow := 0
	mainInputPrice := 0.0
	mainOutputPrice := 0.0
	mainCacheReadPrice := 0.0
	oracleInputPrice := 0.0
	oracleOutputPrice := 0.0
	oracleCacheReadPrice := 0.0
	if pc, ok := cfg.Providers[providerID]; ok {
		ctxWindow = pc.ModelContextWindow
		oracleCtxWindow = pc.OracleModelContextWindow
		mainInputPrice = pc.MainInputPrice
		mainOutputPrice = pc.MainOutputPrice
		mainCacheReadPrice = pc.MainCacheReadPrice
		oracleInputPrice = pc.OracleInputPrice
		oracleOutputPrice = pc.OracleOutputPrice
		oracleCacheReadPrice = pc.OracleCacheReadPrice
	}

	return tui.Settings{
		APIKey:                   apiKey,
		Model:                    model,
		ModelContextWindow:       ctxWindow,
		MainInputPrice:           mainInputPrice,
		MainOutputPrice:          mainOutputPrice,
		MainCacheReadPrice:       mainCacheReadPrice,
		OracleModel:              oracleModel,
		OracleModelContextWindow: oracleCtxWindow,
		OracleInputPrice:         oracleInputPrice,
		OracleOutputPrice:        oracleOutputPrice,
		OracleCacheReadPrice:     oracleCacheReadPrice,
		ProviderID:               providerID,
		ProviderURL:              providerURL,
		ProviderAPIKey:           apiKey,
		ThinkingEffort:           thinkingEffort,
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
		OracleModel:              s.OracleModel,
		OracleModelContextWindow: s.OracleModelContextWindow,
		OracleInputPrice:         s.OracleInputPrice,
		OracleOutputPrice:        s.OracleOutputPrice,
		OracleCacheReadPrice:     s.OracleCacheReadPrice,
		ProviderURL:              s.ProviderURL,
		ThinkingEffort:           s.ThinkingEffort,
	}

	return &Config{
		ActiveProvider: providerID,
		Providers:      providers,
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
					for i := len(msgs) - 1; i >= 0 && msgs[i].Role == "tool"; i-- {
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

func truncateMessages(msgs []provider.ChatMessage, ctxWindow int) []provider.ChatMessage {
	if ctxWindow <= 0 || len(msgs) <= 2 {
		return msgs
	}
	charBudget := int(float64(ctxWindow)*0.75) * 4
	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		c := len(msgs[i].Content)
		for _, tc := range msgs[i].ToolCalls {
			c += len(tc.Function.Arguments)
		}
		if used+c > charBudget && i > 0 {
			summary := provider.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("[Earlier conversation truncated — %d messages omitted]", i),
			}
			return append([]provider.ChatMessage{summary}, msgs[i+1:]...)
		}
		used += c
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
