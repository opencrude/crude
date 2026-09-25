package plan

import (
	"context"

	"crude/types/activity"
)

// ActivitySnapshot is an immutable, already-derived view of the active plan.
// Reading it is safe on presentation threads and never touches SQLite.
func (s *Store) ActivitySnapshot() *activity.Plan { return s.activity.Load() }

// RestoreActivity is called at the session's off-UI resume boundary, before any
// actor starts. The same owner serializes later mutations and publication.
func (s *Store) RestoreActivity(ctx context.Context) error {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	p, err := s.Active(ctx, s.sessionID)
	if err != nil {
		return err
	}
	if p == nil {
		s.activity.Store(nil)
	} else {
		s.publishActivity(*p)
	}
	return nil
}

func (s *Store) publishActivity(p Plan) {
	if p.Status == StatusCompleted || p.Status == StatusCancelled {
		if current := s.activity.Load(); current != nil && current.ID == p.ID {
			s.activity.Store(nil)
		}
		return
	}
	view := &activity.Plan{ID: p.ID, Revision: p.Revision, Objective: p.Objective, Status: string(p.Status), Total: len(p.Steps)}
	var blocked []activity.Step
	for i, step := range p.Steps {
		item := activity.Step{Number: i + 1, Text: step.Description, Status: string(step.Status), Reason: step.Reason}
		switch step.Status {
		case StepCompleted:
			view.Completed++
		case StepSkipped:
			view.Skipped++
		case StepCancelled:
			view.Cancelled++
		case StepInProgress:
			view.Current = append(view.Current, item)
		case StepBlocked, StepFailed:
			blocked = append(blocked, item)
		case StepPending:
			if view.Next == nil {
				view.Next = &item
			}
		}
	}
	if len(view.Current) == 0 {
		view.Current = blocked
	}
	s.activity.Store(view)
}
