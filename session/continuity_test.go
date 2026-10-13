package session

import (
	"context"
	"encoding/json"
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

func TestSessionContinuityTracksConcurrentLiveSettings(t *testing.T) {
	s := newTestSessionForPlan(t)
	defer s.Close()
	category := s.cfg.GetActiveDefaultCategory()
	p, err := s.cfg.providerForCategory(category)
	if err != nil {
		t.Fatal(err)
	}
	live := s.newAgent(p, true, category.ModelModalities)
	live.AgentID = "continuity-settings-owner"
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		for ctx.Err() == nil {
			if _, err := live.ContinuationContext(t.Context()); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 30; i++ {
		enabled := i%2 == 0
		settings := s.SettingsSnapshot()
		settings.SessionMultiAgent = &enabled
		if err := s.HandleSettingsSave(settings); err != nil {
			t.Fatal(err)
		}
		data, err := live.ContinuationContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			MultiAgent bool `json:"multi_agent_enabled"`
		}
		if err := json.Unmarshal([]byte(data.StateJSON), &state); err != nil {
			t.Fatal(err)
		}
		if state.MultiAgent != enabled {
			t.Fatalf("continuity retained stale delegation: got=%v want=%v", state.MultiAgent, enabled)
		}
	}
}
