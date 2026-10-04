package agent

import (
	"reflect"
	"strings"
	"testing"

	rootprompt "crude/prompt"
	"crude/tools"
)

func TestOrchestratorKeepsStandardToolsUsable(t *testing.T) {
	standard := map[string]bool{"read_file": true, "write_file": true, "edit_file": true, "delete_file": true, "move_file": true, "copy_file": true, "glob_files": true, "grep_files": true, "list_directory": true, "web_search": true, "web_fetch": true, "arxiv_search": true}
	originals := make(map[string]tools.ToolDef)
	for _, definition := range tools.AgentTools {
		originals[definition.Function.Name] = definition
	}
	for _, definition := range ApplyOrchestratorOverrides(tools.AgentTools) {
		name := definition.Function.Name
		if standard[name] && !reflect.DeepEqual(definition, originals[name]) {
			t.Errorf("orchestrator altered standard tool %s instead of preserving its operational contract", name)
		}
		for _, forbidden := range []string{"Delegate ALL", "Fallback-only", "Only for reading agent reports", "never need to check who is running"} {
			if strings.Contains(definition.Function.Description, forbidden) {
				t.Errorf("%s retains restrictive guidance %q", name, forbidden)
			}
		}
	}
}

func TestHarnessAllowsDirectWorkAndRequiresIndependentBatching(t *testing.T) {
	orchestrator := rootprompt.BuildRoot(rootprompt.RootOptions{MultiAgent: true})
	for _, required := range []string{"Delegation is optional", "when the user asks you to work yourself", "Never spawn an agent merely to access a standard tool", "Respect live multi-agent settings"} {
		if !strings.Contains(orchestrator, required) {
			t.Errorf("orchestrator missing %q", required)
		}
	}
	leaf, _ := rootprompt.BuildChild(rootprompt.ChildOptions{Category: CatCrafter})
	for _, prompt := range []string{orchestrator, rootprompt.BuildRoot(rootprompt.RootOptions{}), leaf} {
		for _, required := range []string{"Use read/search/edit/write tools", "Batch independent tool calls", "including launches, cancellations", "edits and writes to different files", "especially edits to the same file", "one needs another's result", "async_execute_command"} {
			if !strings.Contains(prompt, required) {
				t.Errorf("tool contract missing %q", required)
			}
		}
		if strings.Contains(prompt, "run_async_command") {
			t.Error("prompt advertises nonexistent command tool")
		}
	}
	researcher, _ := rootprompt.BuildChild(rootprompt.ChildOptions{Category: CatResearcher})
	if !strings.Contains(researcher, "Do not edit files.") {
		t.Error("direct tool guidance relaxed researcher permission boundary")
	}
}

func TestAgentContractsExposeNoStepBudget(t *testing.T) {
	for _, value := range []any{Agent{}, SubAgentSpec{}} {
		if _, ok := reflect.TypeOf(value).FieldByName("MaxSteps"); ok {
			t.Fatalf("%T still exposes MaxSteps", value)
		}
	}
	for _, definition := range tools.AgentTools {
		if definition.Function.Name != "agent_start" {
			continue
		}
		schema := string(definition.Function.Parameters)
		if strings.Contains(schema, "max_steps") {
			t.Fatal("agent_start schema still advertises max_steps")
		}
		if strings.Contains(schema, `"required":["name"`) || !strings.Contains(schema, `"required":["description"`) {
			t.Fatalf("agent_start name is not optional: %s", schema)
		}
	}
}
