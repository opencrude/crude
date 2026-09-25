package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"crude/plan"
	ccrude "crude/tools"
	"crude/types/activity"
)

// planStore lazily constructs the session-scoped plan store. It is safe for
// concurrent reads/writes because plan.Store serializes on *sql.DB (MaxOpenConns=1).
func (s *Session) planStore() (*plan.Store, error) {
	if s.db == nil {
		return nil, fmt.Errorf("plan: no session database")
	}
	if store := s.plans.Load(); store != nil {
		return store, nil
	}
	store := plan.NewStore(s.db, s.sessionID)
	if s.plans.CompareAndSwap(nil, store) {
		return store, nil
	}
	return s.plans.Load(), nil
}

// PlanActivity reads the plan owner's committed projection, never tool output
// or a database. Normal tool-result events already notify presentation clients.
func (s *Session) PlanActivity() *activity.Plan {
	if store := s.plans.Load(); store != nil {
		return store.ActivitySnapshot()
	}
	return nil
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
		if _, native := params["plan"]; native {
			return updateCodexPlan(ctx, store, sessionID, params)
		}
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
	req.Objective = firstStrParam(params, "objective", "goal", "title")
	req.Explanation = firstStrParam(params, "explanation", "note", "notes")
	req.Revision = uint64(getIntParam(params, "revision"))
	req.Status = plan.PlanStatus(firstStrParam(params, "status", "state"))

	rawSteps, ok := firstParam(params, "steps", "todos", "tasks")
	if !ok {
		return req, nil
	}
	entries, err := normalizeStepEntries(rawSteps)
	if err != nil {
		return req, err
	}
	steps := make([]plan.PlanStep, 0, len(entries))
	for i, entry := range entries {
		step, err := decodePlanStep(entry)
		if err != nil {
			return req, err
		}
		if step.ID == "" {
			step.ID = slugStepID(step.Description, i)
		}
		steps = append(steps, step)
	}
	req.Steps = steps
	return req, nil
}

// decodePlanStep accepts one step object. Models legitimately reach for field
// aliases (a step "content" or "title" instead of "description"), so the
// obvious spellings are repaired rather than rejected — a rejection costs a
// whole turn and teaches nothing the schema did not already say. Step
// annotations are the exception: a result/note/outcome field is dropped, never
// stored. The card shows the step banner, not a progress log — narration
// belongs in conversation, and only a deviation status carries a reason.
func decodePlanStep(raw any) (plan.PlanStep, error) {
	if text, ok := raw.(string); ok {
		if strings.TrimSpace(text) == "" {
			return plan.PlanStep{}, fmt.Errorf("update_plan: steps cannot contain an empty string; give each step an id (e.g. \"audit\") and a short description")
		}
		return plan.PlanStep{Description: strings.TrimSpace(text), Status: plan.StepPending}, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return plan.PlanStep{}, fmt.Errorf("update_plan: each step must be an object like {\"id\":\"audit\",\"description\":\"Audit repo\",\"status\":\"pending\"}, got %T", raw)
	}
	step := plan.PlanStep{
		ID:          firstStrParam(object, "id", "step_id", "key"),
		Description: firstStrParam(object, "description", "content", "title", "name", "text", "action"),
		Status:      plan.StepStatus(firstStrParam(object, "status", "state")),
		Reason:      firstStrParam(object, "reason", "why"),
	}
	if step.ID == "" && step.Description == "" {
		return plan.PlanStep{}, fmt.Errorf("update_plan: every step needs a stable id and a short description (a few imperative words); received %s", describeStepKeys(object))
	}
	if step.Description == "" {
		step.Description = step.ID
	}
	if step.ID == "" {
		step.ID = slugStepID(step.Description, 0)
	}
	if step.Status == "" {
		step.Status = plan.StepPending
	}
	step.DependsOn = stringListParam(object, "depends_on", "depends", "blocked_by", "blockedBy", "prerequisites")
	return step, nil
}

// normalizeStepEntries repairs the shapes a step list arrives in: a plain
// array, a JSON-encoded string, an object wrapper ({"items": [...]}), or a
// single step object sent where the array belongs.
func normalizeStepEntries(raw any) ([]any, error) {
	switch value := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		return value, nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, nil
		}
		var decoded []any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, fmt.Errorf("update_plan: 'steps' was sent as a string; pass the array itself, e.g. [{\"id\":\"audit\",\"description\":\"Audit repo\"}]")
		}
		return decoded, nil
	case map[string]any:
		for _, key := range []string{"steps", "todos", "tasks", "items"} {
			if nested, ok := value[key]; ok {
				return normalizeStepEntries(nested)
			}
		}
		return []any{value}, nil
	default:
		return nil, fmt.Errorf("update_plan: 'steps' must be an array of step objects, got %T", raw)
	}
}

// slugStepID derives a stable step id from the step text so a missing id never
// fails the update: the first few meaningful words, lowercased and hyphenated.
func slugStepID(description string, index int) string {
	var parts []string
	for _, word := range strings.Fields(description) {
		clean := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				return r
			case r >= 'A' && r <= 'Z':
				return r + ('a' - 'A')
			default:
				return -1
			}
		}, word)
		if clean == "" {
			continue
		}
		parts = append(parts, clean)
		if len(parts) == 4 {
			break
		}
	}
	slug := strings.Join(parts, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		return fmt.Sprintf("step-%d", index+1)
	}
	return slug
}

func describeStepKeys(object map[string]any) string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "an empty object"
	}
	return "fields: " + strings.Join(keys, ", ")
}

// firstStrParam returns the first non-empty string among the aliases of a
// single logical field.
func firstStrParam(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			if text := strFromAny(value); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

// stringListParam collects a string list from the first alias that carries one,
// accepting a bare string where the schema expects an array.
func stringListParam(params map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := params[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return []string{strings.TrimSpace(typed)}
			}
		case []any:
			var out []string
			for _, item := range typed {
				if text := strings.TrimSpace(strFromAny(item)); text != "" {
					out = append(out, text)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

// firstParam returns the first present alias of a parameter, so callers can
// accept e.g. "steps", "todos", or "tasks" for the same logical field.
func firstParam(params map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			return value, true
		}
	}
	return nil, false
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
