package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"crude/plan"
	"crude/tools"
)

// updateCodexPlan translates native plan snapshots at the domain boundary. The
// same persisted plan remains authoritative for UI, restore, and get_plan.
func updateCodexPlan(ctx context.Context, store *plan.Store, sessionID string, params map[string]any) (tools.ToolResult, error) {
	var input struct {
		Plan []struct {
			Step   string          `json:"step"`
			Status plan.StepStatus `json:"status"`
		} `json:"plan"`
		Explanation string `json:"explanation"`
	}
	data, err := json.Marshal(params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return tools.ToolResult{}, err
	}
	current, err := store.Active(ctx, sessionID)
	if err != nil {
		return tools.ToolResult{}, err
	}
	if len(input.Plan) == 0 && current == nil {
		return tools.ToolResult{Content: "Plan updated"}, nil
	}
	req := plan.UpdateRequest{PlanID: "plan-" + newMessageID(), Objective: "Task plan", Status: plan.StatusActive, Explanation: input.Explanation}
	if current != nil {
		req.PlanID = current.ID
		req.Revision = current.Revision
		req.Objective = current.Objective
	}
	used := map[string]bool{}
	complete := true
	for _, step := range input.Plan {
		if strings.TrimSpace(step.Step) == "" {
			return tools.ToolResult{}, fmt.Errorf("plan step must not be empty")
		}
		if step.Status != plan.StepPending && step.Status != plan.StepInProgress && step.Status != plan.StepCompleted {
			return tools.ToolResult{}, fmt.Errorf("invalid plan step status %q", step.Status)
		}
		id := "step-" + newMessageID()
		if current != nil {
			for _, prior := range current.Steps {
				if prior.Description == step.Step && !used[prior.ID] {
					id = prior.ID
					break
				}
			}
		}
		used[id] = true
		req.Steps = append(req.Steps, plan.PlanStep{ID: id, Description: step.Step, Status: step.Status})
		complete = complete && step.Status == plan.StepCompleted
	}
	// Preserve removed steps as cancelled in the audit log, not as execution
	// dependencies or an instruction to the model to keep obsolete work.
	if current != nil {
		for _, prior := range current.Steps {
			if !used[prior.ID] {
				prior.Status = plan.StepCancelled
				req.Steps = append(req.Steps, prior)
			}
		}
	}
	if complete {
		req.Status = plan.StatusCompleted
	}
	updated, _, err := store.Update(ctx, req)
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Content: mustPlanJSON(updated)}, nil
}
