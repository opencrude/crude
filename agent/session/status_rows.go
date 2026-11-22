package session

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"crude/agent/sessiondb"
	"crude/types/conversation"
)

// recordStatusRow is the one place a change the user made to the session
// (Plan mode, YOLO, the goal) becomes a transcript row: it is stored with the
// session, so a reopened transcript shows what the live one showed, and sent
// to every shell through the main agent.
func (s *Session) recordStatusRow(phase, content string) {
	now := statusRowTime()
	row := sessiondb.StatusRow{ID: fmt.Sprintf("%s:%d", phase, now.UnixNano()), Phase: phase, Content: content, CreatedAt: now}
	if err := sessiondb.AppendStatusRow(s.db, row); err != nil && s.logger != nil {
		s.logger.Warn("SESSION", "status_row_save_failed", "phase", phase, "error", err.Error())
	}
	if o := s.liveOrchestrator(); o != nil && o.sender != nil {
		o.sender.SendAgentEventWithID("status_row", row, o.ID)
	}
}

// lastStatusRow is the time of the newest status row in this process.
var lastStatusRow atomic.Int64

// statusRowTime returns a time after every earlier status row. The wall
// clock can return one value for several rows (on Windows it advances in
// steps of about a millisecond); equal times would give two rows one ID, and
// the second row would be lost.
func statusRowTime() time.Time {
	for {
		last, now := lastStatusRow.Load(), time.Now().UnixNano()
		if now <= last {
			now = last + 1
		}
		if lastStatusRow.CompareAndSwap(last, now) {
			return time.Unix(0, now)
		}
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
		return "plan_mode_on", "Plan mode on for the next turn. The agent can test changes in a separate checkout before proposing a plan. Use /plan again to leave Plan mode."
	}
	return "plan_mode_off", "Plan mode off. From the next turn the agent can change project files."
}

func yoloRow(enabled bool) (string, string) {
	if enabled {
		return "yolo_enabled", "Commands and file changes can run without confirmation. Hard blocks and restore safeguards remain enforced. Use /yolo to restore approval prompts."
	}
	return "yolo_disabled", "Tool approval prompts are restored for this session."
}

// goalTransitionRow is the transcript line for a goal change, or "".
// Accounting-only updates (time, tokens) have none.
func goalTransitionRow(before, after *conversation.Goal) string {
	switch {
	case after == nil && before == nil:
		return ""
	case after == nil:
		return "Goal cleared."
	case before == nil || before.ID != after.ID:
		return "Goal set: " + after.Objective
	case before.Objective != after.Objective:
		return "Goal updated: " + after.Objective
	case after.Status == conversation.GoalComplete && before.Status != conversation.GoalComplete:
		return "Goal complete. " + after.Reason
	case after.Status == conversation.GoalPaused && before.Status != conversation.GoalPaused:
		return "Goal paused. " + after.Reason
	case after.Status == conversation.GoalBlocked && before.Status != conversation.GoalBlocked:
		return "Goal blocked: " + after.Reason
	case after.Status == conversation.GoalBudgetLimited && before.Status != conversation.GoalBudgetLimited:
		return "Goal stopped: " + after.Reason + " Set a new goal with /goal or clear it."
	case after.Verifying && !before.Verifying:
		return "Checking the goal: an independent verifier is reviewing the work."
	case before.Verifying && !after.Verifying && after.Status == conversation.GoalActive:
		return "Goal not complete yet. " + strings.TrimPrefix(after.Reason, "Not yet: ")
	case after.Status == conversation.GoalActive && (before.Status != conversation.GoalActive || before.Waiting && !after.Waiting):
		return "Goal resumed."
	}
	return ""
}
