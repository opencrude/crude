package viewmodel

import (
	"strings"
	"testing"

	"crude/types"
)

func TestGoalSummaryShowsTheBudget(t *testing.T) {
	goal := types.Goal{Objective: "Ship it", Status: types.GoalBudgetLimited, TokensUsed: 51_200, TokenBudget: 50_000, Reason: "The goal used its token budget of 50000 tokens."}
	if got := goalSummary(goal); got != "Goal limited by budget · 51.2k/50.0k tokens: Ship it — The goal used its token budget of 50000 tokens." {
		t.Fatalf("summary = %q", got)
	}
	goal.TokenBudget = 0
	goal.Status = types.GoalActive
	if got := goalSummary(goal); !strings.HasPrefix(got, "Goal active · 51.2k tokens: ") {
		t.Fatalf("unbudgeted summary = %q", got)
	}
}
