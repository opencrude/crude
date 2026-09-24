package session

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/logger"
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
	if s.cfg != nil {
		a.CommandPolicy = safety.CommandPolicyFromSettings(s.cfg.Safety)
	}

	a.Yolo = s.YoloEnabled()
	a.YoloMode = s.YoloEnabled
	a.AsyncMgr = s.asyncMgr

	a.FilteredTools = orchestratorTools(multiAgent)

	a.Policy = safety.DefaultPolicy()

	a.ToolFn = s.buildToolFn(multiAgent, p, modalities)
	a.PermissionReviewer = s.permissionReviewer(a)
	return a
}

// buildToolFn constructs the actual tool dispatch closure. It reads session
// state (yolo, multiAgent) at call time so it stays correct across turns.
func (s *Session) buildToolFn(multiAgent bool, turnProvider *provider.Provider, modalities []string) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	return func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return tools.ToolResult{Content: "Turn cancelled before tool execution."}, err
			}
		}
		s.resolveRelativeParams(tool, params)
		s.recordToolTouch(tool, params)
		if result, handled, err := s.executeMemoryTool(ctx, tool, params); handled {
			return result, err
		}
		switch tool {
		case "update_plan", "get_plan":
			return s.executePlanTool(ctx, tool, params)
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
				cwd = s.WorkDir()
			}
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
			dur := time.Duration(seconds * float64(time.Second))
			select {
			case <-time.After(dur):
				return tools.ToolResult{Content: fmt.Sprintf("Waited %.1f seconds.", seconds)}, nil
			case <-ctx.Done():
				return tools.ToolResult{Content: "Wait cancelled"}, nil
			}

		case "execute_command":
			return s.executeCommandAutoPromote(ctx, params)

		case "run_sub_agent":
			owner, _ := ctx.Value(delegationOwnerKey{}).(delegationOwner)
			enabled := multiAgent
			if s.runtimeProfile != nil {
				enabled = s.runtimeProfile.GetEffectiveConfig(owner.id).IsMultiAgentEnabled()
			}
			if !enabled {
				return delegationDisabledResult(), nil
			}
			if !s.initSubAgentRunner() {
				return delegationDisabledResult(), nil
			}
			return s.SubAgentRunner().RunSubAgent(ctx, params)

		case "poll_sub_agent", "cancel_sub_agent", "list_sub_agents":
			runner := s.SubAgentRunner()
			if runner == nil {
				return tools.ToolResult{Content: "No sub-agents have been launched in this session."}, nil
			}
			switch tool {
			case "poll_sub_agent":
				return runner.PollSubAgent(params)
			case "cancel_sub_agent":
				return runner.CancelSubAgent(params)
			default:
				return runner.ListSubAgents(params)
			}

		case "write_report":
			return tools.ToolResult{Content: "write_report is for sub-agents only. Your response is already shown to the user."}, nil

		default:
			opts := toolOptionsForProvider(turnProvider, modalities, params)
			if opts == nil {
				opts = &tools.ToolOptions{}
			}
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
			Modalities: append([]string(nil), modalities...),
		},
	}
}

// resolveRelativeParams rewrites relative path parameters against the
// session's workspace root so every tool (read/write/edit/glob/grep/list and
// command cwd) operates inside the project the session belongs to.
func (s *Session) resolveRelativeParams(tool string, params map[string]any) {
	if s == nil || params == nil {
		return
	}
	root := s.workDir
	if root == "" {
		return
	}
	switch tool {
	case "glob_files":
		if getStrParam(params, "base_dir") == "" {
			params["base_dir"] = root
		}
	case "grep_files", "list_directory":
		if getStrParam(params, "path") == "" {
			params["path"] = root
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
	if tool == "grep_files" || tool == "execute_command" || tool == "async_execute_command" {
		if value, ok := params["cwd"].(string); ok && value != "" && !filepath.IsAbs(value) {
			params["cwd"] = filepath.Join(root, value)
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

// commandOutcome renders the terminal state of an untracked command as a tool
// result, preserving partial output on failure.
func commandOutcome(uc *fileio.UntrackedCmd) (tools.ToolResult, error) {
	out, _ := uc.ReadOutput(0)
	info := uc.Info()
	if info.Status == fileio.AsyncCompleted {
		if out == "" {
			return tools.ToolResult{Content: "(command completed with no output)"}, nil
		}
		return tools.ToolResult{Content: out}, nil
	}
	errMsg := info.Error
	if errMsg == "" {
		errMsg = fmt.Sprintf("exit code %d", info.ExitCode)
	}
	content := fmt.Sprintf("Command failed: %s", errMsg)
	if out != "" {
		content += "\n\n" + out
	}
	return tools.ToolResult{Content: content}, fmt.Errorf("%s", content)
}

// awaitCommandWithPromotion waits for a command started via StartUntracked.
// A command still running after asyncPromotionThreshold is registered with mgr
// under a fresh auto handle so the turn can continue instead of blocking; the
// result then tells the model how to check, read, or kill it. agentID is only
// used for log attribution and is empty for session-level commands.
func awaitCommandWithPromotion(
	ctx context.Context,
	mgr *fileio.AsyncCommandManager,
	uc *fileio.UntrackedCmd,
	lg *logger.Logger,
	agentID string,
) (tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		uc.Kill()
		<-uc.Done()
		return tools.ToolResult{Content: "Command cancelled."}, nil
	case <-uc.Done():
		return commandOutcome(uc)
	case <-time.After(asyncPromotionThreshold):
	}

	if uc.Status() == fileio.AsyncRunning {
		handle := mgr.NextAutoHandle()
		if _, err := mgr.Promote(uc, handle); err == nil {
			if lg != nil {
				fields := []any{"handle", handle, "pid", uc.PID(), "threshold", asyncPromotionThreshold.String()}
				if agentID != "" {
					fields = append(fields, "agent_id", agentID)
				}
				lg.Info("ASYNC", "auto_promote", fields...)
			}
			return tools.ToolResult{Content: fmt.Sprintf(
				"Command still running after %s — promoted to async command [%s] (PID %d).\n"+
					"It keeps running while you continue. Manage it with:\n"+
					"- check_async_command(handle=%q) — status and exit code\n"+
					"- read_async_output(handle=%q, since_byte=0) — output so far\n"+
					"- kill_async_command(handle=%q) — stop it",
				asyncPromotionThreshold, handle, uc.PID(), handle, handle, handle,
			)}, nil
		} else if lg != nil {
			lg.Error("ASYNC", "auto_promote_failed", "handle", handle, "error", err.Error())
		}
	}

	// The command finished inside the promotion window, or promotion failed:
	// fall back to the blocking path rather than losing the command.
	select {
	case <-ctx.Done():
		uc.Kill()
		<-uc.Done()
		return tools.ToolResult{Content: "Command cancelled."}, nil
	case <-uc.Done():
		return commandOutcome(uc)
	}
}
