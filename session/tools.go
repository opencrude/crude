package session

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/provider"
	"crude/safety"
	"crude/tools"
)

// asyncPromotionThreshold is how long a synchronous execute_command may run
// before it is promoted to an async handle. It must stay below the 60s default
// command timeout, otherwise default-timeout commands are killed before they
// can ever be promoted.
var asyncPromotionThreshold = 30 * time.Second

// newToolFn builds the session tool closure shared by every agent this
// session creates. multiAgent controls which agent tools are exposed.
func (s *Session) newToolFn(multiAgent bool) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	modalities := []string(nil)
	if s.cfg != nil {
		modalities = s.cfg.GetActiveDefaultCategory().ModelModalities
	}
	return s.buildToolFn(multiAgent, s.provider, modalities)
}

func (s *Session) newAgent(p *provider.Provider, multiAgent bool, modalities []string) *agent.Agent {
	a := agent.NewAgent(p, p.Model, s.logger)
	a.MediaReadPolicy = &tools.MediaReadPolicy{Model: p.Model, Standard: p.StandardID(), Modalities: slices.Clone(modalities)}
	if s.cfg != nil {
		a.CommandPolicy = safety.CommandPolicyFromSettings(s.cfg.Safety)
	}

	a.AsyncMgr = s.asyncMgr
	a.WorkDir = s.WorkDir()
	s.bindContinuity(a, s.db, s.sessionID)
	s.bindGuidanceCache(a)

	a.FilteredTools = configuredOrchestratorTools(s.cfg, multiAgent, p.Model)

	a.Policy = safety.DefaultPolicy()

	a.ToolFn = s.buildToolFn(multiAgent, p, slices.Clone(modalities))
	s.bindAgentPermissionPolicy(a)
	return a
}

func (s *Session) bindGuidanceCache(a *agent.Agent) {
	if s == nil || a == nil {
		return
	}
	a.GuidanceCacheLoad = func(key string) (string, bool) {
		s.guidanceMu.Lock()
		defer s.guidanceMu.Unlock()
		value, ok := s.guidanceCache[key]
		return value, ok
	}
	a.GuidanceCacheStore = func(key, value string) {
		s.guidanceMu.Lock()
		if s.guidanceCache == nil {
			s.guidanceCache = make(map[string]string)
		}
		if _, exists := s.guidanceCache[key]; !exists && len(s.guidanceCache) >= 8 {
			for oldest := range s.guidanceCache {
				delete(s.guidanceCache, oldest)
				break
			}
		}
		s.guidanceCache[key] = value
		s.guidanceMu.Unlock()
	}
}

// buildToolFn constructs the actual tool dispatch closure. It reads session
// state (yolo, multiAgent) at call time so it stays correct across turns.
func (s *Session) buildToolFn(multiAgent bool, turnProvider *provider.Provider, modalities []string) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	return s.buildToolFnAtRoot(multiAgent, turnProvider, modalities, s.WorkDir())
}

// buildToolFnAtRoot captures execution geometry without moving logical session
// settings, collaboration or history to another checkout.
func (s *Session) buildToolFnAtRoot(multiAgent bool, turnProvider *provider.Provider, modalities []string, root string) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	return func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return tools.ToolResult{Content: "Turn cancelled before tool execution."}, err
			}
		}
		resolveRelativeParamsAtRoot(root, tool, params)
		if isWriteTool(tool) && root != s.WorkDir() {
			if err := validateExecutionWriteTargets(root, s.TempDir(), writeToolTargets(tool, params)); err != nil {
				return tools.ToolResult{}, err
			}
		}
		s.recordToolTouch(tool, params)
		if result, handled, err := s.executeMemoryTool(ctx, tool, params); handled {
			return result, err
		}
		switch tool {
		case "exec_command", "write_stdin":
			policy := safety.DefaultCommandPolicy()
			if s.cfg != nil {
				policy = safety.CommandPolicyFromSettings(s.cfg.Safety)
			}
			return executeCodexCommand(ctx, s.asyncMgr, root, tool, params, policy)
		case "update_plan", "get_plan":
			return s.executePlanTool(ctx, tool, params)
		case "get_goal", "create_goal", "update_goal":
			return s.executeGoalTool(ctx, tool, params)
		case "async_execute_command":
			command := getStrParam(params, "command")
			if sr := safety.CheckCommandSafety(command, s.logger); sr.Risk.IsAlwaysBlocked() {
				return tools.ToolResult{}, fmt.Errorf("[SAFETY BLOCK] %s", sr.Reason)
			}
			handle := getStrParam(params, "handle")
			if command == "" {
				return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'command'")
			}
			if handle == "" {
				return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'handle'")
			}
			cwd := getStrParam(params, "cwd")
			if cwd == "" {
				cwd = root
			}
			timeout := tools.GetIntArgAny(params, "timeout_seconds", 0)
			info, err := s.asyncMgr.Start(handle, command, cwd, timeout)
			if err != nil {
				return tools.ToolResult{}, fmt.Errorf("async_execute_command: %w", err)
			}
			return tools.ToolResult{Content: FormatCommandStart(*info)}, nil

		case "check_async_command":
			handle := getStrParam(params, "handle")
			if handle == "" {
				return tools.ToolResult{}, fmt.Errorf("check_async_command: missing 'handle'")
			}
			info, ok := s.asyncMgr.Check(handle)
			if !ok {
				return tools.ToolResult{Content: fmt.Sprintf("Async command [%s] not found — it may have already been cleaned up or never started.", handle)}, nil
			}
			return tools.ToolResult{Content: FormatCommandStatus(*info)}, nil

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
			maxBytes := tools.GetIntArgAny(params, "max_bytes", fileio.DefaultOutputChunkBytes)
			page, err := s.asyncMgr.ReadOutputPage(handle, sinceByte, maxBytes)
			if err != nil {
				return tools.ToolResult{}, fmt.Errorf("read_async_output: %w", err)
			}
			return tools.ToolResult{Content: FormatCommandPage(page)}, nil

		case "wait":
			seconds := tools.GetFloatArgAny(params, "seconds", 0)
			if seconds <= 0 {
				return tools.ToolResult{}, fmt.Errorf("wait: seconds must be > 0")
			}
			s.logger.Info("TOOL", "wait", "seconds", seconds)
			dur := time.Duration(seconds * float64(time.Second))
			select {
			case <-time.After(dur):
				return tools.ToolResult{Content: fmt.Sprintf("Waited %.1f seconds.", seconds)}, nil
			case <-ctx.Done():
				return tools.ToolResult{Content: "Wait cancelled"}, nil
			}

		case "execute_command":
			if getStrParam(params, "cwd") == "" {
				params["cwd"] = root
			}
			return s.executeCommandAutoPromote(ctx, params)

		default:
			opts := toolOptionsForProvider(turnProvider, modalities, params)
			if opts == nil {
				opts = &tools.ToolOptions{}
			}
			opts.WorkDir = root
			opts.PrepareMutation = func(receipt fileio.MutationReceipt) (fileio.MutationComplete, error) {
				return s.prepareHistoryMutation(ctx, receipt)
			}
			result := tools.ExecuteWithJSONOptions(tool, params, s.exec, s.editor, s.YoloEnabled(), s.logger, ctx, opts)
			if agent.IsToolError(result.Content) {
				return result, fmt.Errorf("%s", result.Content)
			}
			return result, nil
		}
	}
}

// toolOptionsForProvider snapshots the exact per-turn provider identity.
func toolOptionsForProvider(turnProvider *provider.Provider, modalities []string, params map[string]any) *tools.ToolOptions {
	if turnProvider == nil {
		return nil
	}
	model := turnProvider.Model
	standard := turnProvider.StandardID()
	if override, ok := params["_media_model"].(string); ok && override != "" {
		model = override
	}
	if override, ok := params["_media_standard"].(string); ok && override != "" {
		standard = override
	}
	if override, ok := params["_media_modalities"].([]string); ok {
		modalities = override
	}
	delete(params, "_media_model")
	delete(params, "_media_standard")
	delete(params, "_media_modalities")
	return &tools.ToolOptions{
		Media: &tools.MediaReadPolicy{
			Model:      model,
			Standard:   standard,
			Modalities: slices.Clone(modalities),
		},
	}
}

func resolveRelativeParamsAtRoot(root, tool string, params map[string]any) {
	if params == nil {
		return
	}
	if root == "" {
		return
	}
	switch tool {
	case "apply_patch":
		params["_patch_root"] = root
	case "glob_files":
		if getStrParam(params, "base_dir") == "" {
			params["base_dir"] = root
		}
		params["_display_root"] = root
	case "grep_files", "list_directory":
		if getStrParam(params, "path") == "" {
			params["path"] = root
		}
		if tool == "grep_files" {
			params["_display_root"] = root
		}
	}
	pathKeys := []string{"path", "source", "destination", "base_dir", "directory"}
	for _, key := range pathKeys {
		if value, ok := params[key].(string); ok && value != "" && !filepath.IsAbs(value) {
			if resolved, err := filepath.Abs(filepath.Join(root, value)); err == nil {
				params[key] = resolved
			}
		}
	}
	if tool == "grep_files" || tool == "execute_command" || tool == "async_execute_command" || tool == "exec_command" {
		for _, key := range []string{"cwd", "workdir"} {
			if value, ok := params[key].(string); ok && value != "" && !filepath.IsAbs(value) {
				params[key] = filepath.Join(root, value)
			}
		}
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

func (s *Session) executeCommandAutoPromote(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	cmd := getStrParam(params, "command")
	if cmd == "" {
		return tools.ToolResult{}, fmt.Errorf("execute_command: missing 'command'")
	}

	if !s.YoloEnabled() {
		policy := safety.DefaultCommandPolicy()
		if s.cfg != nil {
			policy = safety.CommandPolicyFromSettings(s.cfg.Safety)
		}
		sr := safety.CheckCommandSafetyWithPolicy(cmd, s.logger, policy)
		if sr.Risk.IsAlwaysBlocked() {
			errMsg := fmt.Sprintf("Error: [SAFETY] %s\nSuggestion: %s", sr.Reason, sr.Suggestion)
			s.logger.Warn("SAFETY", "blocked", "command", cmd, "reason", sr.Reason)
			return tools.ToolResult{Content: errMsg}, fmt.Errorf("%s", errMsg)
		}
		if sr.NeedsConfirmation() {
			s.logger.Info("SAFETY", "risky", "command", cmd, "risk", sr.Risk.String())
		}
	}

	cwd := getStrParam(params, "cwd")
	if cwd == "" {
		cwd = s.WorkDir()
	}
	timeout := tools.GetIntArgAny(params, "timeout_seconds", -1)
	if timeout < 0 {
		timeout = 60
	}

	uc, err := s.asyncMgr.StartUntracked(cmd, cwd, timeout)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("execute_command: %w", err)
	}
	s.logger.Info("EXEC", "auto_start", "pid", uc.PID(), "cmd", cmd)
	return awaitCommandWithPromotion(ctx, s.asyncMgr, uc, s.logger, "")
}
