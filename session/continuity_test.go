package session

import (
	"context"
	"strings"
	"testing"

	"crude/agent"
	"crude/plan"
	"crude/sessiondb"
)

func TestSessionContinuityUsesDurableTurnAndCurrentPlanOwners(t *testing.T) {
	s := newTestSessionForPlan(t)
	defer s.Close()
	if err := sessiondb.BeginAgentTurn(s.db, s.sessionID, "old-orchestrator", 4, "human"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.FinishAgentTurn(s.db, s.sessionID, "old-orchestrator", 4, sessiondb.TurnCompleted); err != nil {
		t.Fatal(err)
	}
	category := s.cfg.GetActiveDefaultCategory()
	provider, err := s.cfg.providerForCategory(category)
	if err != nil {
		t.Fatal(err)
	}
	live := s.newAgent(provider, true, category.ModelModalities)
	live.AgentID = agent.AgentID("replacement-orchestrator")
	data, err := live.ContinuationContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data.StateJSON, `"agent_id":"old-orchestrator"`) || !strings.Contains(data.StateJSON, `"state":"completed"`) ||
		!data.LatestHumanCompleted || !data.IncludePlan || data.PlanContext != "" {
		t.Fatalf("continuity lost durable answered-turn state: %+v", data)
	}
	store, err := s.planStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Update(context.Background(), plan.UpdateRequest{PlanID: "continue", Objective: "Continue safely", Status: plan.StatusActive,
		Steps: []plan.PlanStep{{ID: "observe", Description: "Observe existing build", Status: plan.StepInProgress}}}); err != nil {
		t.Fatal(err)
	}
	data, err = live.ContinuationContext(context.Background())
	if err != nil || !strings.Contains(data.PlanContext, "Saved session state") || !strings.Contains(data.PlanContext, "Observe existing build") {
		t.Fatalf("continuity lost current plan owner: %+v (%v)", data, err)
	}
}
