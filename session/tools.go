package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/provider"
	"crude/safety"
	"crude/statemachine"
	"crude/tools"
)

const asyncPromotionThreshold = 15 * time.Second

func (s *Session) newAgent(p *provider.Provider, multiAgent bool) *agent.Agent {
	a := agent.NewAgent(p, p.Model, s.logger)

	a.Yolo = s.yolo
	a.AsyncMgr = s.asyncMgr

	if multiAgent {
		a.FilteredTools = agent.ApplyOrchestratorOverrides(
			agent.FilterTools(tools.AgentTools, nil, []string{"write_report"}, "", false),
		)
	} else {
		a.FilteredTools = agent.FilterTools(tools.AgentTools, nil, []string{"write_report", "run_sub_agent", "poll_sub_agent", "cancel_sub_agent", "list_sub_agents"}, "", false)
	}
	a.Policy = safety.DefaultPolicy()

	a.ToolFn = func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if isPlanningTool(tool) {
			defer func() {
				if err := persistConversationState(
					context.Background(), s.db, s.sm, s.sessionID,
				); err != nil {
					s.logger.Error("STATE", "persist_failed", "error", err.Error())
				}
			}()
		}
		if result, handled, err := s.executeMemoryTool(ctx, tool, params); handled {
			return result, err
		}
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
			if rawGoalID, ok := state.GetFromContext("plan_goal_id"); ok {
				if goalID, ok := rawGoalID.(string); ok && goalID != "" {
					if g := s.sm.GetActiveGoal(s.sessionID); g != nil && g.ID == goalID {
						return tools.ToolResult{}, fmt.Errorf("plan: a plan already exists. Complete or abandon the current plan before creating a new one")
					}
				}
			}
			state.AddToContext("plan_steps", nil)
			state.AddToContext("plan_step_statuses", nil)
			state.AddToContext("plan_step_labels", nil)
			state.AddToContext("plan_goal_id", nil)

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
						s.sm.CompleteGoalByID(s.sessionID, goalID, map[string]any{"status": "plan_all_steps_done"})
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
			if s.subAgentSup == nil {
				if !s.initSubAgentRunner() {
					return tools.ToolResult{Content: "Error: sub-agent system not initialized"}, fmt.Errorf("sub-agent system not initialized")
				}
			}
			if multiAgent {
				params["wait"] = false
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
			result := tools.ExecuteWithJSON(tool, params, s.exec, s.editor, s.yolo, s.logger, ctx)
			if agent.IsToolError(result.Content) {
				return result, fmt.Errorf("%s", result.Content)
			}
			return result, nil
		}
	}
	return a
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

func (s *Session) executeCommandAutoPromote(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	cmd := getStrParam(params, "command")
	if cmd == "" {
		return tools.ToolResult{}, fmt.Errorf("execute_command: missing 'command'")
	}

	if !s.yolo {
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

	cwd := getStrParam(params, "cwd")
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

	deadline := time.After(asyncPromotionThreshold)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			uc.Kill()
			return tools.ToolResult{Content: "Command cancelled."}, nil
		case <-deadline:
			s.autoHandleMu.Lock()
			s.autoHandleCounter++
			handle := fmt.Sprintf("auto-%d", s.autoHandleCounter)
			s.autoHandleMu.Unlock()

			_, _ = s.asyncMgr.Promote(uc, handle)
			s.logger.Info("ASYNC", "auto_promote", "handle", handle, "pid", pid, "threshold", asyncPromotionThreshold)
			return tools.ToolResult{Content: fmt.Sprintf(
				"Command still running after %s — automatically promoted to async (handle: %q, PID: %d).\n\nYou can continue working while it runs. To manage this command:\n- check_async_command(handle=%q) — check if it's done\n- read_async_output(handle=%q, since_byte=0) — read output so far\n- kill_async_command(handle=%q) — kill it if needed",
				asyncPromotionThreshold, handle, pid, handle, handle, handle,
			)}, nil
		case <-poll.C:
			status := uc.Status()
			if status == fileio.AsyncRunning {
				continue
			}
			out, _ := uc.ReadOutput(0)
			info := uc.Info()

			if status == fileio.AsyncCompleted {
				if out == "" {
					return tools.ToolResult{Content: "(command completed with no output)"}, nil
				}
				return tools.ToolResult{Content: out}, nil
			}

			var errMsg string
			if info.Error != "" {
				errMsg = info.Error
			} else {
				errMsg = fmt.Sprintf("exit code %d", info.ExitCode)
			}
			content := fmt.Sprintf("Command failed: %s", errMsg)
			if out != "" {
				content += "\n\n" + out
			}
			return tools.ToolResult{Content: content}, fmt.Errorf("%s", content)
		}
	}
}
