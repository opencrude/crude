package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptUsesCanonicalPlanningContract(t *testing.T) {
	prompt := BuildSystemPrompt(true)
	for _, required := range []string{"update_plan", "get_plan", "omit unchanged objective/status/steps", "revision conflict"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("system prompt missing %q", required)
		}
	}
	for _, removed := range []string{"mark_step_complete", "mark_step_doing", "report_blocker", "list_goal_status"} {
		if strings.Contains(prompt, removed) {
			t.Fatalf("system prompt still names removed tool %q", removed)
		}
	}
}

func TestSubAgentPlanningPromptMatchesToolAvailability(t *testing.T) {
	researcher, _ := BuildSubAgentPrompt(SubAgentSpec{Category: CatResearcher}, "")
	if !strings.Contains(researcher, "# Planning Contract") {
		t.Fatal("researcher prompt missing planning contract")
	}
	aeron, _ := BuildSubAgentPrompt(SubAgentSpec{Category: CatAeron}, "")
	if strings.Contains(aeron, "# Planning Contract") {
		t.Fatal("aeron prompt advertises unavailable planning tools")
	}
}
