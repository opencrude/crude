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
	OracleModel    string `json:"oracle_model,omitempty"`
	ProviderURL    string `json:"provider_url,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`
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

	msgs          []agent.APIMessage
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
}

func New(cfg *Config, homeDir string) (*Session, error) {
	activeCfg := cfg.GetActiveProviderConfig()
	p := provider.FromPreset(cfg.ActiveProvider, activeCfg.APIKey, activeCfg.ProviderURL)
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

// thinkingBudget maps effort levels to token budgets.
func thinkingBudget(effort string) int {
	switch effort {
	case "low":
		return 1000
	case "medium":
		return 2000
	case "high":
		return 4000
	default:
		return 2000
	}
}

// thinkingParamsForProvider returns provider-specific request fields that enable
// reasoning/thinking content in streaming responses.
func thinkingParamsForProvider(providerID, effort string) map[string]any {
	if effort == "" || effort == "auto" {
		effort = "medium"
	}
	budget := thinkingBudget(effort)

	switch providerID {
	case "openrouter":
		return map[string]any{
			"include_reasoning": true,
		}
	case "openai":
		return map[string]any{
			"reasoning_effort": effort,
		}
	case "anthropic":
		return map[string]any{
			"thinking": map[string]any{"type": "enabled", "budget_tokens": budget},
		}
	case "google":
		return map[string]any{
			"thinking_config": map[string]any{
				"thinking_budget":  budget,
				"include_thoughts": true,
			},
		}
	case "deepseek":
		// DeepSeek returns reasoning automatically; no extra param needed.
		return nil
	case "groq":
		return map[string]any{
			"reasoning_format": "raw",
		}
	default:
		return nil
	}
}

func (s *Session) newAgent() *agent.Agent {
	activeCfg := s.cfg.GetActiveProviderConfig()
	a := agent.NewAgent(s.provider, activeCfg.Model, s.logger)

	oracleModel := activeCfg.OracleModel
	oracleProvider := s.provider
	if oracleModel == "" {
		oracleModel = activeCfg.Model // fallback to worker model
	}
	a.OracleProvider = oracleProvider
	a.OracleModel = oracleModel

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
			resp, err := agent.ConsultOracle(a.Ctx, oracleProvider, oracleModel, query)
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

			// Track step statuses persistently in context so they survive sub-goal popping
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

			// Push each step as a pending sub-goal for state machine tracking
			for _, step := range steps {
				id, _ := step["id"].(string)
				desc, _ := step["description"].(string)
				sg := statemachine.NewGoal(fmt.Sprintf("%s: %s", id, desc), desc, statemachine.PriorityNormal, false)
				sg.Status = statemachine.GoalPending
				goal.SubGoalStack = append(goal.SubGoalStack, sg)
			}
			if len(goal.SubGoalStack) > 0 {
				goal.SubGoalStack[0].Status = statemachine.GoalActive
			}

			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			return status, nil

		case "mark_step_complete":
			stepID := getStrParam(params, "step_id")
			if stepID == "" {
				return "", fmt.Errorf("mark_step_complete: missing 'step_id'")
			}
			s.sm.CompleteCurrentGoal(s.sessionID, map[string]any{
				"step_id": stepID,
				"result":  getStrParam(params, "result"),
			})
			// Update persistent step status
			state := s.sm.GetOrCreateState(s.sessionID)
			if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := statuses.(map[string]string); ok {
					m[stepID] = "completed"
					state.AddToContext("plan_step_statuses", m)
				}
			}
			// Activate next pending sub-goal
			focus := s.sm.GetFocusGoal(s.sessionID)
			if focus != nil {
				activated := false
				for _, sg := range focus.SubGoalStack {
					if sg.Status == statemachine.GoalPending {
						sg.Status = statemachine.GoalActive
						activated = true
						break
					}
				}
				// All sub-goals done — complete the plan root goal
				if !activated && len(focus.SubGoalStack) == 0 {
					s.sm.CompleteCurrentGoal(s.sessionID, map[string]any{"status": "plan_complete"})
				}
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
			s.sm.FailCurrentGoal(s.sessionID, reason)
			state := s.sm.GetOrCreateState(s.sessionID)
			if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := statuses.(map[string]string); ok {
					m[stepID] = "failed"
					state.AddToContext("plan_step_statuses", m)
				}
			}
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
			// Mark the active step as blocked in persistent status
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
						for id, st := range m {
							if st == "blocked" {
								m[id] = "active"
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
				return "Blocker resolved. Plan execution can resume.", nil
			}
			return "No active blocker to resolve.", nil

		case "list_goal_status":
			status := agent.BuildStateMachineStatus(s.sm, s.sessionID)
			if status == "" {
				return "No active goals or plans.", nil
			}
			return status, nil

		default:
			result := tools.ExecuteWithJSON(tool, params, s.exec, s.editor)
			if strings.HasPrefix(result, "Error:") {
				return result, fmt.Errorf("%s", result)
			}
			return result, nil
		}
	}
	a.ExtraBody = thinkingParamsForProvider(s.provider.ID, activeCfg.ThinkingEffort)
	return a
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
	return nil
}

func (s *Session) HandleFetchModels(providerID, baseURL, apiKey string) ([]tui.ModelEntry, error) {
	p := provider.FromPreset(providerID, apiKey, baseURL)
	models, err := provider.FetchModels(p)
	if err != nil {
		return nil, err
	}
	entries := make([]tui.ModelEntry, len(models))
	for i, m := range models {
		entries[i] = tui.ModelEntry{ID: m.ID, Name: m.Name}
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

	for userText := range tuiInst.InputCh {
		// Drain any stale cancel signal from a previous iteration
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
		s.msgs = append(s.msgs, agent.APIMessage{Role: "user", Content: userText})
		msgsCopy := make([]agent.APIMessage, len(s.msgs))
		copy(msgsCopy, s.msgs)
		s.msgsMu.Unlock()
		s.logger.Info("conv.user_msg", "content", userText, "msg_id", currentMsgID)

		s.logger.Info("agent.loop_start", "content", userText, "iteration", 0)
		s.sm.CreateGoal(s.sessionID, "respond", userText, statemachine.PriorityNormal, false)

		if s.editor != nil {
			s.editor = s.editor.WithMessage(currentMsgID)
		}

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

		ag.Run(ctx, msgsCopy, prompt, func(evt agent.AgentEvent) {
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
				s.blockMu.Lock()
				s.pendingBlock = true
				s.blockMu.Unlock()
				tuiInst.SendAgentEvent(evt.Type, evt.Data)
				return
			}
			if evt.Type == "confirmation_always" {
				if data, ok := evt.Data.(map[string]string); ok {
					op := safety.CRUDEOp(data["operation"])
					tool := data["tool"]
					target := data["target"]
					s.permStore.GrantAlways(op, tool, target)
				}
				return
			}
			tuiInst.SendAgentEvent(evt.Type, evt.Data)
			if evt.Type == "done" {
				if content, ok := evt.Data.(string); ok {
					finalContent = content
				}
			}
		}, blockResolveCh, s.sm, s.sessionID)

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

		if finalContent != "" {
			s.msgsMu.Lock()
			s.msgs = append(s.msgs, agent.APIMessage{Role: "assistant", Content: finalContent})
			s.msgsMu.Unlock()
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

	return tui.Settings{
		APIKey:         apiKey,
		Model:          model,
		OracleModel:    oracleModel,
		ProviderID:     providerID,
		ProviderURL:    providerURL,
		ProviderAPIKey: apiKey,
		ThinkingEffort: thinkingEffort,
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

	providers := base.Providers
	if providers == nil {
		providers = make(map[string]ProviderConfig)
	}
	providers[providerID] = ProviderConfig{
		APIKey:         apiKey,
		Model:          s.Model,
		OracleModel:    s.OracleModel,
		ProviderURL:    s.ProviderURL,
		ThinkingEffort: s.ThinkingEffort,
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


