package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"crude/plan"
	ccrude "crude/tools"
)

// planStore lazily constructs the session-scoped plan store. It is safe for
// concurrent reads/writes because plan.Store serializes on *sql.DB (MaxOpenConns=1).
func (s *Session) planStore() (*plan.Store, error) {
	if s.db == nil {
		return nil, fmt.Errorf("plan: no session database")
	}
	return plan.NewStore(s.db, s.sessionID), nil
}

// executePlanTool handles the declarative planning tools. Both the main agent
// and sub-agents route here, so planning behaviour is identical regardless of
// caller. The canonical JSON tool result is the single live/hydration source.
func (s *Session) executePlanTool(ctx context.Context, tool string, params map[string]any) (ccrude.ToolResult, error) {
	store, err := s.planStore()
	if err != nil {
		return ccrude.ToolResult{}, err
	}
	return executePlanToolWithStore(ctx, store, s.sessionID, tool, params)
}

func executePlanToolWithStore(ctx context.Context, store *plan.Store, sessionID, tool string, params map[string]any) (ccrude.ToolResult, error) {
	switch tool {
	case "update_plan":
		return updatePlanTool(ctx, store, params)
	case "get_plan":
		return getPlanTool(ctx, store, sessionID, params)
	default:
		return ccrude.ToolResult{}, fmt.Errorf("unknown planning tool: %s", tool)
	}
}

func updatePlanTool(ctx context.Context, store *plan.Store, params map[string]any) (ccrude.ToolResult, error) {
	req, err := decodeUpdateRequest(params)
	if err != nil {
		return ccrude.ToolResult{}, err
	}

	if req.PlanID == "" {
		return ccrude.ToolResult{}, fmt.Errorf("update_plan: 'plan_id' is required")
	}

	updatedPlan, _, err := store.Update(ctx, req)
	if err != nil {
		var conflict *plan.RevisionConflictError
		if errors.As(err, &conflict) {
			return ccrude.ToolResult{Content: mustJSON(map[string]any{
				"error":             "revision_conflict",
				"expected_revision": conflict.Current.Revision,
				"received_revision": conflict.Requested,
				"current_plan":      conflict.Current,
			})}, err
		}
		return ccrude.ToolResult{Content: mustJSON(map[string]any{
			"error":   "invalid_plan_update",
			"message": err.Error(),
		})}, err
	}

	return ccrude.ToolResult{Content: mustPlanJSON(updatedPlan)}, nil
}

func getPlanTool(ctx context.Context, store *plan.Store, sessionID string, params map[string]any) (ccrude.ToolResult, error) {
	planID := getStrParam(params, "plan_id")
	includeHistory, _ := params["include_history"].(bool)
	if planID == "" {
		if includeHistory {
			plans, lerr := store.List(ctx, sessionID)
			if lerr != nil {
				return ccrude.ToolResult{}, lerr
			}
			return ccrude.ToolResult{Content: mustJSON(map[string]any{"plans": plans})}, nil
		}
		// No ID: return the active plan if any.
		active, aerr := store.Active(ctx, sessionID)
		if aerr != nil {
			return ccrude.ToolResult{}, aerr
		}
		if active == nil {
			return ccrude.ToolResult{Content: `{"error":"no_active_plan"}`}, nil
		}
		return ccrude.ToolResult{Content: mustPlanJSON(*active)}, nil
	}
	p, gerr := store.Get(ctx, planID)
	if gerr != nil {
		return ccrude.ToolResult{Content: `{"error":"plan_not_found"}`}, nil
	}
	if includeHistory {
		events, eerr := store.Events(ctx, planID)
		if eerr != nil {
			return ccrude.ToolResult{}, eerr
		}
		return ccrude.ToolResult{Content: mustJSON(map[string]any{"plan": p, "events": events})}, nil
	}
	return ccrude.ToolResult{Content: mustPlanJSON(p)}, nil
}

func mustPlanJSON(p plan.Plan) string {
	return mustJSON(p)
}

func mustJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf(`{"error":"marshal plan: %s"}`, err.Error())
	}
	return string(b)
}

func decodeUpdateRequest(params map[string]any) (plan.UpdateRequest, error) {
	var req plan.UpdateRequest
	req.PlanID = getStrParam(params, "plan_id")
	req.Objective = getStrParam(params, "objective")
	req.Explanation = getStrParam(params, "explanation")
	req.Revision = uint64(getIntParam(params, "revision"))
	req.Status = plan.PlanStatus(getStrParam(params, "status"))

	rawSteps, ok := params["steps"]
	if !ok {
		return req, nil
	}
	stepsJSON, err := json.Marshal(rawSteps)
	if err != nil {
		return req, fmt.Errorf("update_plan: marshal steps: %w", err)
	}
	var steps []map[string]any
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return req, fmt.Errorf("update_plan: invalid steps: %w", err)
	}
	req.Steps = make([]plan.PlanStep, 0, len(steps))
	for _, raw := range steps {
		step := plan.PlanStep{
			ID:          strFromAny(raw["id"]),
			Description: strFromAny(raw["description"]),
			Status:      plan.StepStatus(strFromAny(raw["status"])),
			Result:      strFromAny(raw["result"]),
			Reason:      strFromAny(raw["reason"]),
		}
		if step.Status == "" {
			step.Status = plan.StepPending
		}
		if root, ok := raw["depends_on"].([]any); ok {
			for _, d := range root {
				if ds, ok := d.(string); ok && ds != "" {
					step.DependsOn = append(step.DependsOn, ds)
				}
			}
		}
		req.Steps = append(req.Steps, step)
	}
	return req, nil
}

func strFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
