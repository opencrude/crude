package plan

import (
	"fmt"
	"strings"
)

// Validate applies the structural invariants for one declarative snapshot
// without mutating it. Plans report work; they do not schedule it. Dependencies
// are metadata for readers and never gate which step may be started, cancelled,
// retried, or completed first.
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
	}

	if strings.TrimSpace(req.Objective) == "" {
		return fmt.Errorf("plan objective is required")
	}
	if !req.Status.Valid() {
		return fmt.Errorf("invalid plan status %q; valid statuses are active, paused, completed, cancelled", req.Status)
	}
	if len(req.Steps) == 0 {
		return fmt.Errorf("plan must contain at least one step")
	}

	seen := make(map[string]struct{}, len(req.Steps))
	for _, step := range req.Steps {
		id := strings.TrimSpace(step.ID)
		if id == "" {
			return fmt.Errorf("plan step id is required")
		}
		if _, duplicate := seen[id]; duplicate {
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

	for _, step := range req.Steps {
		for _, dependency := range step.DependsOn {
			if dependency == step.ID {
				return fmt.Errorf("plan step %q depends on itself", step.ID)
			}
			if _, ok := seen[dependency]; !ok {
				return fmt.Errorf("plan step %q depends on missing step %q", step.ID, dependency)
			}
		}
	}
	if cycle := findDependencyCycle(req.Steps); cycle != nil {
		return fmt.Errorf("plan dependency cycle: %s", strings.Join(cycle, " -> "))
	}
	return nil
}

func findDependencyCycle(steps []PlanStep) []string {
	index := make(map[string]int, len(steps))
	for i, step := range steps {
		index[step.ID] = i
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
		for _, dependency := range steps[i].DependsOn {
			switch color[dependency] {
			case gray:
				cycle = append(append([]string(nil), stack...), dependency)
				return true
			case white:
				if visit(dependency) {
					return true
				}
			}
		}
		color[id] = black
		stack = stack[:len(stack)-1]
		return false
	}
	for _, step := range steps {
		if color[step.ID] == white && visit(step.ID) {
			return cycle
		}
	}
	return nil
}
