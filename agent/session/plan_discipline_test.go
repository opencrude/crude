package session

import (
	"strings"
	"testing"

	"crude/types/activity"
)

func activePlan(revision uint64, completed int) *activity.Plan {
	return &activity.Plan{ID: "p", Revision: revision, Objective: "Ship it", Status: "active", Completed: completed, Steps: []activity.Step{
		{Number: 1, Text: "Reproduce", Status: "completed"}, {Number: 2, Text: "Fix", Status: "in_progress"}, {Number: 3, Text: "Verify", Status: "pending"},
	}}
}

// A plan that stands still while work continues is mentioned once at the
// floor, then only after twice as long; an update resets the cadence.
func TestPlanDriftReminderBacksOffAndResets(t *testing.T) {
	d := &planDiscipline{limit: planDriftFloor}
	p := activePlan(1, 1)
	var at []int
	for step := 1; step <= 1+planDriftFloor*3; step++ {
		if notice := d.step("run", p); notice != "" {
			at = append(at, step)
			if !strings.Contains(notice, "2. [in_progress] Fix") {
				t.Fatalf("reminder lacks the banner: %s", notice)
			}
		}
	}
	// Step 1 observes the plan; reminders follow 8 and then 16 unchanged steps.
	if len(at) != 2 || at[0] != 1+planDriftFloor || at[1] != 1+3*planDriftFloor {
		t.Fatalf("reminders at steps %v", at)
	}
	d.step("run", activePlan(2, 2))
	for i := 1; i < planDriftFloor; i++ {
		if d.step("run", activePlan(2, 2)) != "" {
			t.Fatal("reminded before the floor after an update")
		}
	}
	if d.step("run", &activity.Plan{ID: "p", Revision: 3, Status: "completed"}) != "" {
		t.Fatal("reminded about a closed plan")
	}
}

// A plan that took many steps per completed step earns a proportionally
// longer quiet period.
func TestPlanDriftFollowsThePlansOwnPace(t *testing.T) {
	d := &planDiscipline{limit: planDriftFloor}
	d.step("run", activePlan(1, 0))
	for i := 0; i < 20; i++ {
		_ = d.step("run", activePlan(1, 0)) // one reminder fires inside this stretch
	}
	d.step("run", activePlan(2, 1)) // 20 steps per completed step so far
	for i := 1; i < 2*20; i++ {
		if d.step("run", activePlan(2, 1)) != "" {
			t.Fatalf("reminded after %d steps, below the plan's pace", i)
		}
	}
}

func TestPlanReconcileOnlyForWorkThatLeftThePlanUntouched(t *testing.T) {
	d := &planDiscipline{limit: planDriftFloor}
	d.step("run-1", activePlan(1, 1))
	if d.finish(activePlan(1, 1)) != "" {
		t.Fatal("a run without tool calls was asked to reconcile")
	}
	d.runTools = 3
	if notice := d.finish(activePlan(1, 1)); !strings.Contains(notice, "Before your final answer") {
		t.Fatalf("untouched plan after work = %q", notice)
	}
	if d.finish(activePlan(2, 2)) != "" {
		t.Fatal("a run that updated its plan was asked again")
	}
	d.step("run-2", nil)
	d.runTools = 5
	if d.finish(activePlan(1, 1)) != "" {
		t.Fatal("a plan created during the run was asked to reconcile")
	}
}
