package session

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/provider"
	"crude/safety"
	"crude/tools"
)

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

	if multiAgent {
		a.FilteredTools = agent.ApplyOrchestratorOverrides(
			agent.FilterTools(tools.AgentTools, nil, []string{"write_report"}, "", false),
		)
	} else {
		a.FilteredTools = agent.FilterTools(tools.AgentTools, nil, []string{"write_report", "run_sub_agent", "poll_sub_agent", "cancel_sub_agent", "list_sub_agents"}, "", false)
	}
	a.Policy = safety.DefaultPolicy()

	a.ToolFn = s.buildToolFn(multiAgent, p, modalities)
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
			if !multiAgent {
				return tools.ToolResult{Content: "Error: multi-agent mode is disabled"}, fmt.Errorf("multi-agent mode is disabled")
			}
			if s.subAgentSup == nil {
				if !s.initSubAgentRunner() {
					return tools.ToolResult{Content: "Error: sub-agent system not initialized"}, fmt.Errorf("sub-agent system not initialized")
				}
			}
			return s.subAgentSup.RunSubAgent(ctx, params)

		case "poll_sub_agent":
			if s.subAgentSup == nil {
				if !s.initSubAgentRunner() {
					return tools.ToolResult{Content: "Error: sub-agent system not initialized"}, fmt.Errorf("sub-agent system not initialized")
				}
			}
			return s.subAgentSup.PollSubAgent(params)

		case "cancel_sub_agent":
			if s.subAgentSup == nil {
				if !s.initSubAgentRunner() {
					return tools.ToolResult{Content: "Error: sub-agent system not initialized"}, fmt.Errorf("sub-agent system not initialized")
				}
			}
			return s.subAgentSup.CancelSubAgent(params)

		case "list_sub_agents":
			if s.subAgentSup == nil {
				if !s.initSubAgentRunner() {
					return tools.ToolResult{Content: "Error: sub-agent system not initialized"}, fmt.Errorf("sub-agent system not initialized")
				}
			}
			return s.subAgentSup.ListSubAgents(params)

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
	pid := uc.PID()
	s.logger.Info("EXEC", "auto_start", "pid", pid, "cmd", cmd)

	select {
	case <-ctx.Done():
		uc.Kill()
		<-uc.Done()
		return tools.ToolResult{Content: "Command cancelled."}, nil
	case <-uc.Done():
	}
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
