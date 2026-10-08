package prompt

import (
	"strings"
	"testing"
)

func TestPlanningLifecyclePolicy(t *testing.T) {
	for _, text := range []string{"three or more substantive outcomes", "milestone before starting the next stage", "replace=true", "Omit obsolete steps", "Before the final answer", "Never mark unverified work successful"} {
		if !strings.Contains(PlanningContract, text) {
			t.Fatalf("planning contract missing %q", text)
		}
	}
	if !strings.Contains(CurrentPlanReview, "replace=true") || !strings.Contains(CurrentPlanReview, "Do not retain old cancelled steps") {
		t.Fatal("turn-boundary plan review lost clean replacement guidance")
	}
}

func TestPlanningContractReachesRootAndChildAcrossModels(t *testing.T) {
	for _, model := range []string{"deepseek", "claude", "gpt", "ollama"} {
		for _, multi := range []bool{false, true} {
			base := BuildRoot(RootOptions{Model: model, MultiAgent: multi})
			assembled := ApplyRootOverlays(base, "Work carefully", "Agent", "")
			if strings.Count(assembled, PlanningContract) != 1 {
				t.Fatalf("%s multi=%t lost canonical plan lifecycle", model, multi)
			}
		}
		for _, category := range []string{"crafter", "researcher"} {
			system, _ := BuildChild(ChildOptions{Category: category, Model: model, ProfileInstructions: "Work carefully"})
			if strings.Count(system, PlanningContract) != 1 {
				t.Fatalf("%s %s lost canonical plan lifecycle", model, category)
			}
		}
	}
}
