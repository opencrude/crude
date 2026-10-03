package session

import (
	"context"
	"encoding/json"

	"crude/plan"
	rootprompt "crude/prompt"
)

// activePlanTurnContext reads the canonical revision when a human message is
// admitted. The activity view omits stable step IDs and some statuses; use the
// plan store instead of asking the model to reconstruct a stale plan from chat.
func (s *Session) activePlanTurnContext(ctx context.Context) (string, error) {
	// No plan store means no plan has been created in this live session. Resume
	// restores it before accepting turns, so the common path needs no DB read.
	if s.plans.Load() == nil {
		return "", nil
	}
	store, err := s.planStore()
	if err != nil {
		return "", err
	}
	current, err := store.Active(ctx, s.sessionID)
	if err != nil || current == nil {
		return "", err
	}
	return formatActivePlanContext(*current)
}

func formatActivePlanContext(current plan.Plan) (string, error) {
	type stepView struct {
		ID          string          `json:"id"`
		Description string          `json:"description"`
		Status      plan.StepStatus `json:"status"`
		DependsOn   []string        `json:"depends_on,omitempty"`
		Reason      string          `json:"reason,omitempty"`
	}
	view := struct {
		ID        string          `json:"id"`
		Revision  uint64          `json:"revision"`
		Status    plan.PlanStatus `json:"status"`
		Objective string          `json:"objective"`
		Steps     []stepView      `json:"steps"`
	}{ID: current.ID, Revision: current.Revision, Status: current.Status, Objective: current.Objective}
	for _, step := range current.Steps {
		view.Steps = append(view.Steps, stepView{
			ID: step.ID, Description: step.Description, Status: step.Status,
			DependsOn: step.DependsOn, Reason: step.Reason,
		})
	}
	data, err := json.Marshal(view)
	if err != nil {
		return "", err
	}
	return rootprompt.ActivePlan(string(data)), nil
}
