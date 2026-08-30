package agent

import (
	"reflect"
	"strings"
	"testing"

	"crude/tools"
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

func TestSystemPromptCachePrefixExcludesVolatileEnvironment(t *testing.T) {
	t.Setenv("SHELL", "/bin/cache-prefix-one")
	first := BuildSystemPrompt(true)
	t.Setenv("SHELL", "/bin/cache-prefix-two")
	second := BuildSystemPrompt(true)

	// Shell is intentionally stable enough to belong in the prefix, so changing
	// it must change the prompt. The truly volatile fields must never appear.
	if first == second {
		t.Fatal("stable environment changes were not reflected in the system prompt")
	}
	for _, prompt := range []string{first, second} {
		for _, volatile := range []string{"- Date:", "- Git:", "- Directory:"} {
			if strings.Contains(prompt, volatile) {
				t.Fatalf("cacheable system prompt contains volatile field %q", volatile)
			}
		}
		if !strings.Contains(prompt, "Working Directory:") {
			t.Fatal("system prompt lost stable workspace identity")
		}
	}
}

func TestOrchestratorPromptExplainsBothSubAgentWaitModes(t *testing.T) {
	prompt := BuildSystemPrompt(true)
	for _, required := range []string{"wait=true (default)", "genuinely blocks", "wait=false", "remains alive after your turn ends"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("orchestrator prompt missing wait contract %q", required)
		}
	}
}

func TestAgentContractsExposeNoStepBudget(t *testing.T) {
	for _, value := range []any{Agent{}, SubAgentSpec{}} {
		if _, ok := reflect.TypeOf(value).FieldByName("MaxSteps"); ok {
			t.Fatalf("%T still exposes MaxSteps", value)
		}
	}
	for _, definition := range tools.AgentTools {
		if definition.Function.Name != "run_sub_agent" {
			continue
		}
		schema := string(definition.Function.Parameters)
		if strings.Contains(schema, "max_steps") {
			t.Fatal("run_sub_agent schema still advertises max_steps")
		}
		if strings.Contains(schema, `"required":["name"`) || !strings.Contains(schema, `"required":["description"]`) {
			t.Fatalf("run_sub_agent name is not optional: %s", schema)
		}
	}
	if strings.Contains(BuildSystemPrompt(true), "max_steps") {
		t.Fatal("orchestrator prompt still teaches a step budget")
	}
}
