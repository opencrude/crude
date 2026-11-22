package viewmodel

import (
	"strings"
	"testing"

	"crude/types/conversation"
)

func TestGoalDetailsShowsTheBudget(t *testing.T) {
	goal := conversation.Goal{Objective: "Ship it", Status: conversation.GoalBudgetLimited, TokensUsed: 51_200, TokenBudget: 50_000, Reason: "The goal used its token budget of 50000 tokens."}
	got := goalDetails(goal, false)
	for _, want := range []string{"Status: limited by budget", "Tokens: 51.2k / 50.0k", "Ship it", "The goal used its token budget of 50000 tokens."} {
		if !strings.Contains(got, want) {
			t.Fatalf("goal details omit %q: %q", want, got)
		}
	}
	goal.TokenBudget = 0
	goal.Status = conversation.GoalActive
	if got := goalDetails(goal, false); !strings.Contains(got, "Status: active") || !strings.Contains(got, "Tokens: 51.2k\n") {
		t.Fatalf("unbudgeted goal details = %q", got)
	}
}
