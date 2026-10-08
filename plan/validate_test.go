package plan

import (
	"strings"
	"testing"
)

func validReq() UpdateRequest {
	return UpdateRequest{
		PlanID:    "plan-123",
		Objective: "Fix prompt-history persistence",
		Status:    StatusActive,
		Steps: []PlanStep{
			{ID: "audit", Description: "Audit flow", Status: StepPending},
			{ID: "verify", Description: "Add tests", Status: StepPending, DependsOn: []string{"audit"}},
		},
	}
}

func TestValidateNewPlanValid(t *testing.T) {
	if err := Validate(nil, validReq()); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateRejectsMalformedSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*UpdateRequest)
	}{
		{name: "nonzero creation revision", mutate: func(req *UpdateRequest) { req.Revision = 3 }},
		{name: "empty objective", mutate: func(req *UpdateRequest) { req.Objective = "   " }},
		{name: "no steps", mutate: func(req *UpdateRequest) { req.Steps = nil }},
		{name: "duplicate ids", mutate: func(req *UpdateRequest) {
			req.Steps = []PlanStep{{ID: "a", Description: "A", Status: StepPending}, {ID: "a", Description: "B", Status: StepPending}}
		}},
		{name: "empty id", mutate: func(req *UpdateRequest) { req.Steps = []PlanStep{{Description: "A", Status: StepPending}} }},
		{name: "empty description", mutate: func(req *UpdateRequest) { req.Steps = []PlanStep{{ID: "a", Status: StepPending}} }},
		{name: "invalid status", mutate: func(req *UpdateRequest) { req.Steps[0].Status = "mystery" }},
		{name: "missing dependency", mutate: func(req *UpdateRequest) { req.Steps[0].DependsOn = []string{"ghost"} }},
		{name: "self dependency", mutate: func(req *UpdateRequest) { req.Steps[0].DependsOn = []string{"audit"} }},
		{name: "dependency cycle", mutate: func(req *UpdateRequest) {
			req.Steps[0].DependsOn = []string{"verify"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := validReq()
			test.mutate(&req)
			if err := Validate(nil, req); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateAcceptsDeclarativeLifecycleStatesInAnyOrder(t *testing.T) {
	tests := []struct {
		name   string
		status PlanStatus
		steps  []PlanStep
	}{
		{
			name: "dependent starts before prerequisite",
			steps: []PlanStep{
				{ID: "a", Description: "A", Status: StepPending},
				{ID: "b", Description: "B", Status: StepInProgress, DependsOn: []string{"a"}},
			},
		},
		{
			name: "dependent remains pending after prerequisite cancellation",
			steps: []PlanStep{
				{ID: "a", Description: "A", Status: StepCancelled},
				{ID: "b", Description: "B", Status: StepPending, DependsOn: []string{"a"}},
			},
		},
		{
			name: "all exceptional states may omit reasons",
			steps: []PlanStep{
				{ID: "a", Description: "A", Status: StepSkipped},
				{ID: "b", Description: "B", Status: StepBlocked},
				{ID: "c", Description: "C", Status: StepFailed},
				{ID: "d", Description: "D", Status: StepCancelled},
			},
		},
		{
			name:   "completed plan may accurately retain mixed step states",
			status: StatusCompleted,
			steps: []PlanStep{
				{ID: "a", Description: "A", Status: StepCompleted},
				{ID: "b", Description: "B", Status: StepFailed},
				{ID: "c", Description: "C", Status: StepPending},
			},
		},
		{
			name:   "cancelled plan needs no explanation",
			status: StatusCancelled,
			steps:  []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := validReq()
			req.Steps = test.steps
			if test.status != "" {
				req.Status = test.status
			}
			if err := Validate(nil, req); err != nil {
				t.Fatalf("declarative snapshot rejected: %v", err)
			}
		})
	}
}

func TestValidateAcceptsReopenRetryAndCancelledPlanResumeWithoutReasons(t *testing.T) {
	for _, priorStatus := range []PlanStatus{StatusActive, StatusCompleted, StatusCancelled} {
		t.Run(string(priorStatus), func(t *testing.T) {
			existing := &Plan{
				ID: "plan-123", Revision: 4, Objective: "x", Status: priorStatus,
				Steps: []PlanStep{
					{ID: "a", Description: "A", Status: StepCompleted},
					{ID: "b", Description: "B", Status: StepFailed},
					{ID: "c", Description: "C", Status: StepCancelled},
				},
			}
			req := UpdateRequest{
				PlanID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive,
				Steps: []PlanStep{
					{ID: "c", Description: "C", Status: StepInProgress},
					{ID: "a", Description: "A", Status: StepPending},
					{ID: "b", Description: "B", Status: StepInProgress},
				},
			}
			if err := Validate(existing, req); err != nil {
				t.Fatalf("free lifecycle transition rejected: %v", err)
			}
		})
	}
}

func TestValidateExistingSnapshotRequiresRevisionButAllowsRemovedSteps(t *testing.T) {
	existing := &Plan{
		ID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}, {ID: "b", Description: "B", Status: StepPending}},
	}
	req := validReq()
	req.Revision = 3
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected revision conflict")
	}
	req.Revision = 4
	req.Steps = []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}}
	if err := Validate(existing, req); err != nil {
		t.Fatalf("clean desired snapshot rejected: %v", err)
	}
}

func TestValidateAcceptsLongObjective(t *testing.T) {
	req := validReq()
	req.Objective = strings.Repeat("describe the outcome without an arbitrary cap ", 20)
	if err := Validate(nil, req); err != nil {
		t.Fatalf("long objective rejected: %v", err)
	}
}
