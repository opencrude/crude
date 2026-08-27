package plan

import (
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

func TestValidate_NewPlanValid(t *testing.T) {
	if err := Validate(nil, validReq()); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidate_NewPlanRequiresRevisionZero(t *testing.T) {
	req := validReq()
	req.Revision = 3
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected non-zero creation revision rejection")
	}
}

func TestValidate_RejectsDuplicateIDs(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepPending},
		{ID: "a", Description: "A dup", Status: StepPending},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestValidate_RejectsMissingDependency(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepPending, DependsOn: []string{"ghost"}},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestValidate_RejectsCycle(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepPending, DependsOn: []string{"b"}},
		{ID: "b", Description: "B", Status: StepPending, DependsOn: []string{"a"}},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestValidate_RejectsSelfDependency(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepPending, DependsOn: []string{"a"}},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected self-dependency error")
	}
}

func TestValidate_RequiresReasonForFailedSkippedBlockedCancelled(t *testing.T) {
	for _, status := range []StepStatus{StepFailed, StepSkipped, StepBlocked, StepCancelled} {
		req := validReq()
		req.Steps = []PlanStep{{ID: "a", Description: "A", Status: status}}
		if err := Validate(nil, req); err == nil {
			t.Fatalf("expected reason required for %s", status)
		}
	}
}

func TestValidate_InProgressRequiresSatisfiedDependencies(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepPending},
		{ID: "b", Description: "B", Status: StepInProgress, DependsOn: []string{"a"}},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected unsatisfied dependency error for in_progress step")
	}
}

func TestValidate_CompletionRequiresAllTerminal(t *testing.T) {
	req := validReq()
	req.Status = StatusCompleted
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepPending},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected completion validation error")
	}
}

func TestValidate_FailedBlocksCompletion(t *testing.T) {
	req := validReq()
	req.Status = StatusCompleted
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepFailed, Reason: "boom"},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected failed step to block completion")
	}
}

func TestValidate_ValidCompletion(t *testing.T) {
	req := validReq()
	req.Status = StatusCompleted
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepCompleted, Result: "done"},
		{ID: "b", Description: "B", Status: StepSkipped, Reason: "not needed"},
	}
	if err := Validate(nil, req); err != nil {
		t.Fatalf("expected valid completion, got %v", err)
	}
}

func TestValidate_RevisionConflict(t *testing.T) {
	existing := &Plan{ID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive}
	req := validReq()
	req.Revision = 3
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected revision conflict")
	}
}

func TestValidate_ReopenTerminalStepRequiresReason(t *testing.T) {
	existing := &Plan{
		ID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}},
	}
	req := validReq()
	req.Revision = 4
	req.Steps = []PlanStep{{ID: "a", Description: "A", Status: StepInProgress}}
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected reopen terminal step requires reason")
	}
}

func TestValidate_ReopenCompletedPlanRequiresExplanation(t *testing.T) {
	existing := &Plan{
		ID: "plan-123", Revision: 4, Objective: "x", Status: StatusCompleted,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}},
	}
	req := validReq()
	req.Revision = 4
	req.Steps = []PlanStep{{ID: "a", Description: "A", Status: StepInProgress, Reason: "new evidence"}}
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected plan reopen explanation requirement")
	}
	req.Explanation = "New evidence invalidated completion"
	if err := Validate(existing, req); err != nil {
		t.Fatalf("valid plan reopen rejected: %v", err)
	}
}

func TestValidate_RejectsOmittedExistingStep(t *testing.T) {
	existing := &Plan{
		ID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive,
		Steps: []PlanStep{
			{ID: "a", Description: "A", Status: StepCompleted},
			{ID: "b", Description: "B", Status: StepPending},
		},
	}
	req := validReq()
	req.Revision = 4
	req.Steps = []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}}
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected omitted step rejection")
	}
}

func TestValidate_CancelPlanRequiresExplanation(t *testing.T) {
	req := validReq()
	req.Status = StatusCancelled
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected cancellation explanation requirement")
	}
}

func TestValidate_RejectsDependencyOnCancelledStep(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepCancelled, Reason: "removed"},
		{ID: "b", Description: "B", Status: StepPending, DependsOn: []string{"a"}},
	}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected dependency on cancelled step rejection")
	}
}

func TestValidate_RetryFailedRequiresReason(t *testing.T) {
	existing := &Plan{
		ID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepFailed, Reason: "boom"}},
	}
	req := validReq()
	req.Revision = 4
	req.Steps = []PlanStep{{ID: "a", Description: "A", Status: StepInProgress}}
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected retry failed step requires reason")
	}
}

func TestValidate_CancellingDependencyRequiresDependentChange(t *testing.T) {
	existing := &Plan{
		ID: "plan-123", Revision: 4, Objective: "x", Status: StatusActive,
		Steps: []PlanStep{
			{ID: "a", Description: "A", Status: StepInProgress, DependsOn: []string{"base"}},
			{ID: "base", Description: "Base", Status: StepPending},
		},
	}
	req := validReq()
	req.Revision = 4
	req.Steps = []PlanStep{
		{ID: "a", Description: "A", Status: StepInProgress, DependsOn: []string{"base"}},
		{ID: "base", Description: "Base", Status: StepCancelled, Reason: "obsolete"},
	}
	if err := Validate(existing, req); err == nil {
		t.Fatal("expected dependent must change error")
	}
}

func TestValidate_EmptyObjectiveRejected(t *testing.T) {
	req := validReq()
	req.Objective = "   "
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected empty objective error")
	}
}

func TestValidate_EmptyStepIDRejected(t *testing.T) {
	req := validReq()
	req.Steps = []PlanStep{{ID: "", Description: "A", Status: StepPending}}
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected empty step id error")
	}
}

func TestValidate_NoStepsRejected(t *testing.T) {
	req := validReq()
	req.Steps = nil
	if err := Validate(nil, req); err == nil {
		t.Fatal("expected no steps error")
	}
}
