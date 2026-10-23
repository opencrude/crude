package session

import (
	"fmt"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/types"
)

// recordStatusRow is the one place a change the user made to the session
// (Plan mode, YOLO, the goal) becomes a transcript row: it is stored with the
// session, so a reopened transcript shows what the live one showed, and sent
// to every shell through the main agent.
func (s *Session) recordStatusRow(phase, content string) {
	now := time.Now()
	row := sessiondb.StatusRow{ID: fmt.Sprintf("%s:%d", phase, now.UnixNano()), Phase: phase, Content: content, CreatedAt: now}
	if err := sessiondb.AppendStatusRow(s.db, row); err != nil && s.logger != nil {
		s.logger.Warn("SESSION", "status_row_save_failed", "phase", phase, "error", err.Error())
	}
	if o := s.liveOrchestrator(); o != nil && o.sender != nil {
		o.sender.SendAgentEventWithID("status_row", row, o.ID)
	}
}

// StatusRows are the session's recorded status rows, for hydration.
func (s *Session) StatusRows() []sessiondb.StatusRow {
	if s == nil {
		return nil
	}
	rows, err := sessiondb.ListStatusRows(s.db)
	if err != nil && s.logger != nil {
		s.logger.Warn("SESSION", "status_rows_load_failed", "error", err.Error())
	}
	return rows
}

func planModeRow(enabled bool) (string, string) {
	if enabled {
		return "plan_mode_on", "Plan mode on. From the next turn the agent explores read-only, asks what it cannot find out, and proposes a plan; nothing is changed. Use /plan again to leave Plan mode."
	}
	return "plan_mode_off", "Plan mode off. From the next turn the agent can change files and run commands again."
}

func yoloRow(enabled bool) (string, string) {
	if enabled {
		return "yolo_enabled", "Commands and file changes can run without confirmation. Hard blocks and restore safeguards remain enforced. Use /yolo to restore approval prompts."
	}
	return "yolo_disabled", "Tool approval prompts are restored for this session."
}

// goalTransitionRow is the transcript line for a goal change, or "".
// Accounting-only updates (time, tokens) have none.
func goalTransitionRow(before, after *types.Goal) string {
	switch {
	case after == nil && before == nil:
		return ""
	case after == nil:
		return "Goal cleared."
	case before == nil || before.ID != after.ID:
		return "Goal set: " + after.Objective
	case before.Objective != after.Objective:
		return "Goal updated: " + after.Objective
	case after.Status == types.GoalComplete && before.Status != types.GoalComplete:
		return "Goal complete. " + after.Reason
	case after.Status == types.GoalPaused && before.Status != types.GoalPaused:
		return "Goal paused. " + after.Reason
	case after.Status == types.GoalBlocked && before.Status != types.GoalBlocked:
		return "Goal blocked: " + after.Reason
	case after.Status == types.GoalBudgetLimited && before.Status != types.GoalBudgetLimited:
		return "Goal stopped: " + after.Reason + " Set a new goal with /goal or clear it."
	case after.Verifying && !before.Verifying:
		return "Checking the goal: an independent verifier is reviewing the work."
	case before.Verifying && !after.Verifying && after.Status == types.GoalActive:
		return "Goal not complete yet. " + strings.TrimPrefix(after.Reason, "Not yet: ")
	case after.Status == types.GoalActive && (before.Status != types.GoalActive || before.Waiting && !after.Waiting):
		return "Goal resumed."
	}
	return ""
}
