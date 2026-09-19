package plan

import (
	"fmt"
	"strings"
)

// Validate applies every pre-mutation invariant to a complete snapshot without
// mutating it. It is pure so an invalid request can never partially alter the
// persisted plan. existing is nil for a new plan; non-nil for an update (its
// Revision must match, and reopening/terminating rules consult it).
//
// The validation set mirrors the handoff's "Atomic validation rules" list:
//  1. existing updates require matching revision
//  2. objective is non-empty
//  3. step IDs are stable and unique
//  4. dependency IDs exist
//  5. no self-dependency or cycle
//  6. in_progress dependencies are satisfied
//  7. completed may include result
//  8. skipped/blocked/failed/cancelled require reason
//  9. cancelling a depended-on step requires dependents to change in same update
//  10. terminal step reopening requires explanation
//  11. plan completion requires every step completed/skipped/validly cancelled
//  12. failed/blocked steps prevent completion
//  13. no automatic plan completion (the caller never flips status implicitly)
//  14. the entire request either commits or rolls back (enforced by the store)
func Validate(existing *Plan, req UpdateRequest) error {
	if existing == nil {
		if req.Revision != 0 {
			return fmt.Errorf("new plan revision must be 0, got %d", req.Revision)
		}
	} else {
		if existing.Revision != req.Revision {
			return &RevisionConflictError{Current: *existing, Requested: req.Revision}
		}
		if existing.ID != req.PlanID {
			return fmt.Errorf("plan id mismatch: %q vs %q", existing.ID, req.PlanID)
		}
		if existing.Status == StatusCancelled {
			return fmt.Errorf("plan %q is cancelled", existing.ID)
		}
		if existing.Status == StatusCompleted && req.Status != StatusCompleted && strings.TrimSpace(req.Explanation) == "" {
			return fmt.Errorf("reopening completed plan %q requires an explanation", existing.ID)
		}
	}

	objective := strings.TrimSpace(req.Objective)
	if objective == "" {
		return fmt.Errorf("plan objective is required")
	}

	if !req.Status.Valid() {
		return fmt.Errorf("invalid plan status %q; valid statuses are active, paused, completed, cancelled", req.Status)
	}

	if len(req.Steps) == 0 {
		return fmt.Errorf("plan must contain at least one step")
	}

	// Step ID uniqueness and empty-ID rejection.
	seen := make(map[string]struct{}, len(req.Steps))
	for _, step := range req.Steps {
		id := strings.TrimSpace(step.ID)
		if id == "" {
			return fmt.Errorf("plan step id is required")
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate plan step id %q", id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(step.Description) == "" {
			return fmt.Errorf("plan step %q has an empty description", id)
		}
		if !step.Status.Valid() {
			return fmt.Errorf("plan step %q has invalid status %q; valid statuses are pending, in_progress, completed, skipped, blocked, failed, cancelled", id, step.Status)
		}
	}
	if req.Status == StatusCancelled && strings.TrimSpace(req.Explanation) == "" {
		return fmt.Errorf("cancelling plan %q requires an explanation", req.PlanID)
	}
	if existing != nil {
		for _, prior := range existing.Steps {
			if _, ok := seen[prior.ID]; !ok {
				return fmt.Errorf("plan step %q was omitted; retain it with status cancelled", prior.ID)
			}
		}
	}

	// Dependency existence + self/cycle detection.
	if cycle := findDependencyCycle(req.Steps); cycle != nil {
		return fmt.Errorf("plan dependency cycle: %s", strings.Join(cycle, " -> "))
	}

	for i := range req.Steps {
		step := &req.Steps[i]
		for _, dep := range step.DependsOn {
			if dep == step.ID {
				return fmt.Errorf("plan step %q depends on itself", step.ID)
			}
			if _, ok := seen[dep]; !ok {
				return fmt.Errorf("plan step %q depends on missing step %q", step.ID, dep)
			}
			if depPlanStep := depStep(req.Steps, dep); depPlanStep != nil && depPlanStep.Status == StepCancelled {
				return fmt.Errorf("plan step %q depends on cancelled step %q", step.ID, dep)
			}
		}

		if err := validateStep(req.Steps, existing, *step); err != nil {
			return err
		}
	}

	return validatePlanTerminalInvariants(req)
}

// validateStep applies the per-step transition rules. requestSteps are the
// complete submitted snapshot (the authority for dependency satisfaction);
// existing is the prior persisted plan (used only for reopen/retry and
// dependent-cancel rules).
func validateStep(requestSteps []PlanStep, existing *Plan, step PlanStep) error {
	switch step.Status {
	case StepInProgress:
		for _, dep := range step.DependsOn {
			if !depStep(requestSteps, dep).Status.DependencySatisfied() {
				return fmt.Errorf("plan step %q depends on %q which is not completed/skipped", step.ID, dep)
			}
		}
	case StepSkipped, StepBlocked, StepFailed, StepCancelled:
		if strings.TrimSpace(step.Reason) == "" {
			return fmt.Errorf("plan step %q requires a reason when %s", step.ID, step.Status)
		}
	}

	if existing != nil {
		prior := findStep(existing, step.ID)
		if prior != nil {
			// Retrying a failed step moves it back to in_progress and must
			// explain why the retry is worthwhile.
			if prior.Status == StepFailed && step.Status == StepInProgress && strings.TrimSpace(step.Reason) == "" {
				return fmt.Errorf("retrying plan step %q requires a reason", step.ID)
			}
			// Reopening terminal step work (completed/skipped/cancelled) back to
			// pending/in_progress requires an explanation.
			if prior.Status.Terminal() && !step.Status.Terminal() && strings.TrimSpace(step.Reason) == "" {
				return fmt.Errorf("reopening terminal plan step %q requires a reason", step.ID)
			}
		}

		// Cancelling a step that others depend on forces those dependents to
		// change in the same update (unless they are already terminal/blocked).
		if step.Status == StepCancelled {
			for _, dep := range dependentsOf(existing, step.ID) {
				if reqStep, ok := requestStep(requestSteps, dep); ok {
					if !reqStep.Status.Terminal() && reqStep.Status != StepBlocked {
						return fmt.Errorf("cancelling step %q requires dependent step %q to change in the same update", step.ID, dep)
					}
				}
			}
		}
	}
	return nil
}

func validatePlanTerminalInvariants(req UpdateRequest) error {
	if req.Status != StatusCompleted {
		return nil
	}
	failed := 0
	blocked := 0
	terminal := 0
	for _, step := range req.Steps {
		switch step.Status {
		case StepCompleted, StepSkipped, StepCancelled:
			terminal++
		case StepFailed:
			failed++
		case StepBlocked:
			blocked++
		}
	}
	if failed > 0 || blocked > 0 {
		return fmt.Errorf("plan cannot be completed with %d failed and %d blocked steps", failed, blocked)
	}
	if terminal != len(req.Steps) {
		return fmt.Errorf("plan cannot be completed: %d/%d steps are terminal", terminal, len(req.Steps))
	}
	return nil
}

func dependentsOf(p *Plan, stepID string) []string {
	var out []string
	for _, s := range p.Steps {
		for _, dep := range s.DependsOn {
			if dep == stepID {
				out = append(out, s.ID)
			}
		}
	}
	return out
}

func depStep(steps []PlanStep, id string) *PlanStep {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
	}
	return nil
}

func requestStep(steps []PlanStep, id string) (PlanStep, bool) {
	for _, s := range steps {
		if s.ID == id {
			return s, true
		}
	}
	return PlanStep{}, false
}

func findStep(p *Plan, id string) *PlanStep {
	if p == nil {
		return nil
	}
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}

func findDependencyCycle(steps []PlanStep) []string {
	index := make(map[string]int, len(steps))
	for i, s := range steps {
		index[s.ID] = i
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(steps))
	var stack []string
	var cycle []string
	var visit func(string) bool
	visit = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		i, ok := index[id]
		if !ok {
			color[id] = black
			stack = stack[:len(stack)-1]
			return false
		}
		for _, dep := range steps[i].DependsOn {
			switch color[dep] {
			case gray:
				cycle = append(append([]string(nil), stack...), dep)
				return true
			case white:
				if visit(dep) {
					return true
				}
			}
		}
		color[id] = black
		stack = stack[:len(stack)-1]
		return false
	}
	for _, s := range steps {
		if color[s.ID] == white && visit(s.ID) {
			return cycle
		}
	}
	return nil
}
