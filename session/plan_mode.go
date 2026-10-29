package session

import (
	"context"
	"fmt"
	"time"

	"crude/agent"
	"crude/logger"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
)

// planModeKey persists Plan mode with the conversation, so resume and fork
// keep the mode the user chose.
const planModeKey = "plan_mode"

// PlanMode reports whether this session's next turn runs in Plan mode: the
// agent explores read-only and agrees a plan instead of changing anything.
func (s *Session) PlanMode() bool {
	return s != nil && s.planMode.Load()
}

// SetPlanMode persists the mode; it applies from the next turn, never to a
// turn that is already running.
func (s *Session) SetPlanMode(enabled bool) error {
	var err error
	if enabled {
		err = sessiondb.SaveRuntimeState(context.Background(), s.db, planModeKey, []byte("on"))
	} else {
		err = sessiondb.DeleteRuntimeState(context.Background(), s.db, planModeKey)
	}
	if err != nil {
		return err
	}
	if s.planMode.Swap(enabled) == enabled {
		return nil
	}
	if !s.IsTurnActive() {
		s.syncGoalPlanAccounting(nil)
	}
	if s.logger != nil {
		s.logger.Info("SESSION", "plan_mode", "enabled", enabled)
	}
	s.recordStatusRow(planModeRow(enabled))
	return nil
}

func (s *Session) loadPlanMode() error {
	value, err := sessiondb.LoadRuntimeState(context.Background(), s.db, planModeKey)
	if err != nil {
		return err
	}
	s.planMode.Store(len(value) != 0)
	s.lastTurnPlanMode.Store(len(value) != 0)
	s.goalPlanAccounting = len(value) != 0
	return nil
}

// planModeAllows is the runtime half of Plan mode, applied to the tools a
// turn offers and again to every call: read-only tools run, commands run
// only when they are read-only (see planModeCommandRefusal), and progress
// tracking, the control gateway and anything that writes, deletes, executes
// or delegates do not.
func planModeAllows(tool string, metadata func(string) (tools.ToolMetadata, bool)) bool {
	switch tool {
	case "update_plan", "crude_control", "create_goal", "update_goal":
		return false
	case "exec_command", "write_stdin", "execute_command", "async_execute_command":
		return true
	}
	described, known := tools.NativeMetadata(tool)
	if metadata != nil {
		described, known = metadata(tool)
	}
	return known && agent.PolicyAllowsMetadata(safety.ReadOnlyPolicy(), described)
}

func planModeTools(defs []tools.ToolDef, metadata func(string) (tools.ToolMetadata, bool)) []tools.ToolDef {
	allowed := make([]tools.ToolDef, 0, len(defs))
	for _, def := range defs {
		if planModeAllows(def.Function.Name, metadata) {
			allowed = append(allowed, def)
		}
	}
	return allowed
}

// planModeRefusal explains why a Plan mode call cannot run, or returns nil.
func planModeRefusal(tool string, params map[string]any, metadata func(string) (tools.ToolMetadata, bool), lg *logger.Logger) error {
	if !planModeAllows(tool, metadata) {
		return fmt.Errorf("[PLAN MODE] %s changes the workspace or carries out work, and the user has Plan mode on. Explore read-only and put this step in the proposed plan instead", tool)
	}
	command := getStrParam(params, "command")
	switch tool {
	case "exec_command":
		command = getStrParam(params, "cmd")
	case "write_stdin":
		if chars := getStrParam(params, "chars"); chars != "" && chars != "\x03" {
			return fmt.Errorf("[PLAN MODE] interactive input can change the workspace, and the user has Plan mode on")
		}
		return nil
	case "execute_command", "async_execute_command":
	default:
		return nil
	}
	if command == "" {
		return nil
	}
	if result := safety.CheckCommandSafety(command, lg); result.Risk != safety.RiskSafe {
		return fmt.Errorf("[PLAN MODE] %q is not a read-only command (risk: %s, %s), and the user has Plan mode on. Use read-only commands to explore and put this step in the proposed plan instead", command, result.Risk, result.Reason)
	}
	return nil
}

// notePlanModeTurn records the mode a starting turn runs in and reports
// whether it is the first turn after the user turned Plan mode off.
func (s *Session) notePlanModeTurn(planMode bool) (ended bool) {
	return s.lastTurnPlanMode.Swap(planMode) && !planMode
}

func planModeEndedNotice() provider.ChatMessage {
	notice := provider.NewTextMessage("user", rootprompt.PlanModeEndedNotice())
	notice.Origin = provider.OriginRuntimeContext
	notice.Visibility = provider.VisibilityContextOnly
	notice.InternalID = fmt.Sprintf("plan-mode-ended-%d", time.Now().UnixNano())
	return notice
}
