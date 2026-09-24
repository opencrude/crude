package agent

import (
	"reflect"
	"strings"
	"testing"

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
	orchestrator := BuildSystemPrompt(true)
	for _, required := range []string{"Delegation is optional", "when the user asks you to work yourself", "Never spawn an agent merely to access a standard tool", "Respect live multi-agent settings"} {
		if !strings.Contains(orchestrator, required) {
			t.Errorf("orchestrator missing %q", required)
		}
	}
	leaf, _ := BuildSubAgentPrompt(SubAgentSpec{Category: CatCrafter}, "")
	for _, prompt := range []string{orchestrator, BuildSystemPrompt(false), leaf} {
		for _, required := range []string{"Prefer standard read/search/edit/write tools", "Batch independent tool calls", "including launches, cancellations", "one needs another's result", "async_execute_command"} {
			if !strings.Contains(prompt, required) {
				t.Errorf("tool contract missing %q", required)
			}
		}
		if strings.Contains(prompt, "run_async_command") {
			t.Error("prompt advertises nonexistent command tool")
		}
	}
	researcher, _ := BuildSubAgentPrompt(SubAgentSpec{Category: CatResearcher}, "")
	if !strings.Contains(researcher, "Do not edit files.") {
		t.Error("direct tool guidance relaxed researcher permission boundary")
	}
}
