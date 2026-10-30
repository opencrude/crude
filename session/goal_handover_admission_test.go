package session

import (
	"testing"

	"crude/types/handover"
)

func TestGoalHandoverContinuationCannotConsumeAcrossExistingOwnerReservation(t *testing.T) {
	for _, kind := range []string{"turn-preparation", "history-restore"} {
		t.Run(kind, func(t *testing.T) {
			f := newHandoverFixture(t)
			f.ready(t)
			switch kind {
			case "turn-preparation":
				// A turn/compaction can own its gate before its activity
				// projection is published. A sampled zero is not exclusion.
				f.s.turnGate.Lock()
				defer f.s.turnGate.Unlock()
			case "history-restore":
				release, err := f.s.BeginHistoryRestoreLease(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			state, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target())
			if err == nil || state.Phase == handover.Consumed {
				t.Fatalf("continuation consumed during %s reservation: state=%+v err=%v", kind, state, err)
			}
			stored, err := f.s.GoalHandover(f.r.OperationID)
			if err != nil || stored.Phase != handover.Ready {
				t.Fatal("refused continuation changed its exact permit", stored, err)
			}
			if goal, _ := f.s.Goal(); !goal.Waiting {
				t.Fatal("owner reservation refusal released the handover wait")
			}
		})
	}
}
