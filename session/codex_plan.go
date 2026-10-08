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
		Explanation string          `json:"explanation"`
		Objective   string          `json:"objective"`
		Status      plan.PlanStatus `json:"status"`
		Revision    *uint64         `json:"revision"`
		Replace     bool            `json:"replace"`
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
	if len(input.Plan) == 0 && current == nil && !input.Replace && input.Status == "" {
		return tools.ToolResult{Content: "Plan updated"}, nil
	}
	req := plan.UpdateRequest{PlanID: "plan-" + newMessageID(), Objective: "Task plan", Status: plan.StatusActive, Explanation: input.Explanation, Replace: input.Replace}
	if current != nil {
		req.PlanID = current.ID
		req.Revision = current.Revision
		req.Objective = current.Objective
	}
	if input.Objective != "" {
		req.Objective = input.Objective
	}
	if input.Replace {
		req.Objective = input.Objective
	}
	if input.Revision != nil {
		req.Revision = *input.Revision
	}
	used := map[string]bool{}
	complete := true
	for _, step := range input.Plan {
		if strings.TrimSpace(step.Step) == "" {
			return tools.ToolResult{}, fmt.Errorf("plan step must not be empty")
		}
		if !step.Status.Valid() {
			return tools.ToolResult{}, fmt.Errorf("invalid plan step status %q", step.Status)
		}
		id := "step-" + newMessageID()
		if current != nil && !input.Replace {
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
	if len(input.Plan) == 0 {
		// Empty native snapshots close existing work honestly, never fabricate success.
		req.Status = plan.StatusCancelled
	} else if complete {
		req.Status = plan.StatusCompleted
	}
	if input.Status != "" {
		req.Status = input.Status
	}
	updated, _, err := store.Update(ctx, req)
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Content: mustPlanJSON(updated)}, nil
}
