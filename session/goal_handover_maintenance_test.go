package session

import (
	"errors"
	"sync"
	"testing"

	"crude/types/handover"
)

func TestGoalHandoverSuccessorMaintenanceIsAnExplicitUnconsumedWait(t *testing.T) {
	f := newHandoverFixture(t)
	before := f.ready(t)
	_, ok := f.s.memoryJobs.begin()
	if !ok {
		t.Fatal("fixture could not acquire actual maintenance ownership")
	}
	f.s.memoryJobs.maintaining.Store(true)
	var finish sync.Once
	done := func() {
		finish.Do(func() {
			f.s.memoryJobs.maintaining.Store(false)
			f.s.memoryJobs.done()
		})
	}
	t.Cleanup(done)
	for range 2 {
		state, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target())
		if !errors.Is(err, handover.ErrMaintenancePending) || state.Phase != handover.Ready || state.LifecycleEpoch != before.LifecycleEpoch {
			t.Fatalf("finite owned maintenance consumed or invalidated the permit: %+v %v", state, err)
		}
		if goal, _ := f.s.Goal(); !goal.Waiting {
			t.Fatal("waiting for maintenance admitted an automatic goal turn")
		}
	}
	done()
	state, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target())
	if err != nil || state.Phase != handover.Consumed {
		t.Fatalf("joined maintenance prevented the exact fresh continuation: %+v %v", state, err)
	}
}

func TestGoalHandoverMaintenanceWaitCannotOverrideNewUserPause(t *testing.T) {
	f := newHandoverFixture(t)
	f.ready(t)
	_, ok := f.s.memoryJobs.begin()
	if !ok {
		t.Fatal("fixture could not acquire maintenance ownership")
	}
	t.Cleanup(f.s.memoryJobs.done)
	if _, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target()); !errors.Is(err, handover.ErrMaintenancePending) {
		t.Fatal(err)
	}
	if _, err := f.s.PauseGoal("New explicit user pause while maintenance joins."); err != nil {
		t.Fatal(err)
	}
	state, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target())
	if err == nil || errors.Is(err, handover.ErrMaintenancePending) || state.Phase == handover.Consumed {
		t.Fatalf("an invalidated authorization was retried as temporary work: %+v %v", state, err)
	}
}
