package session

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"crude/agent"
	"crude/plan"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/tools"
	"crude/types/activity"
)

// planDriftFloor is the fewest model steps an active plan may stand still
// before a reminder: several tool batches, enough for one plan step of
// ordinary work. Once the plan has completed steps, its own pace (twice the
// steps one plan step took) raises the bar; every unheeded reminder doubles it.
const planDriftFloor = 8

// planDiscipline keeps the plan banner honest without fixed counts of tool
// calls: it measures work against the plan's own pace and asks once more
// before a final answer that would leave unfinished work on the banner.
type planDiscipline struct {
	mu sync.Mutex
	// Plan identity last observed and the work done since it changed.
	planID           string
	revision         uint64
	stepsSinceChange int
	limit            int
	// Pace: model steps spent while steps were completed.
	workSteps, completions, lastCompleted int
	// Per run: tool calls made and the plan identity when it began.
	runID           string
	runTools        int
	runPlanID       string
	runPlanRevision uint64
}

func (s *Session) planActivityFor(live *agent.Agent) *activity.Plan {
	if live.Depth > 0 {
		return s.ChildPlanActivity(string(live.AgentID))
	}
	return s.PlanActivity()
}

func openPlan(p *activity.Plan) bool {
	if p == nil || p.Status != string(plan.StatusActive) {
		return false
	}
	for _, step := range p.Steps {
		if step.Status == string(plan.StepPending) || step.Status == string(plan.StepInProgress) {
			return true
		}
	}
	return false
}

func (s *Session) bindPlanDiscipline(live *agent.Agent) {
	d := &planDiscipline{limit: planDriftFloor}
	previousPrepare, previousFn, previousFinish := live.PrepareStep, live.ToolFn, live.BeforeFinish
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		var definitions []tools.ToolDef
		var notices []provider.ChatMessage
		if previousPrepare != nil {
			definitions, notices = previousPrepare()
		} else {
			definitions = append(definitions, live.FilteredTools...)
		}
		if notice := d.step(live.RunID, s.planActivityFor(live)); notice != "" {
			notices = append(notices, runtimeMessage(notice))
		}
		return definitions, notices
	}
	live.ToolFn = func(ctx context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		d.mu.Lock()
		d.runTools++
		d.mu.Unlock()
		return previousFn(ctx, name, params)
	}
	live.BeforeFinish = func() []provider.ChatMessage {
		var notices []provider.ChatMessage
		if previousFinish != nil {
			notices = previousFinish()
		}
		if notice := d.finish(s.planActivityFor(live)); notice != "" {
			notices = append(notices, runtimeMessage(notice))
		}
		return notices
	}
}

// step runs before every model request and returns a drift reminder or "".
func (d *planDiscipline) step(runID string, p *activity.Plan) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if runID != d.runID {
		d.runID, d.runTools = runID, 0
		d.runPlanID, d.runPlanRevision = "", 0
		if p != nil {
			d.runPlanID, d.runPlanRevision = p.ID, p.Revision
		}
	}
	if !openPlan(p) {
		d.planID, d.revision, d.stepsSinceChange, d.limit = "", 0, 0, planDriftFloor
		return ""
	}
	if p.ID != d.planID || p.Revision != d.revision {
		if p.ID == d.planID && p.Completed > d.lastCompleted {
			d.completions += p.Completed - d.lastCompleted
		}
		d.planID, d.revision, d.lastCompleted = p.ID, p.Revision, p.Completed
		d.stepsSinceChange, d.limit = 0, planDriftFloor
		return ""
	}
	d.stepsSinceChange++
	d.workSteps++
	limit := d.limit
	if d.completions > 0 {
		limit = max(limit, 2*d.workSteps/d.completions)
	}
	if d.stepsSinceChange < limit {
		return ""
	}
	d.stepsSinceChange, d.limit = 0, 2*limit
	return rootprompt.RuntimePlanDrift(formatPlanBanner(p))
}

// finish asks once per run for a plan update when the run did work, never
// touched an open plan, and is about to end.
func (d *planDiscipline) finish(p *activity.Plan) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.runTools == 0 || !openPlan(p) || p.ID != d.runPlanID || p.Revision != d.runPlanRevision {
		return ""
	}
	return rootprompt.RuntimePlanReconcile(formatPlanBanner(p))
}

func formatPlanBanner(p *activity.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Current plan (revision %d): %s\n", p.Revision, p.Objective)
	for _, step := range p.Steps {
		fmt.Fprintf(&b, "%d. [%s] %s\n", step.Number, step.Status, step.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}
