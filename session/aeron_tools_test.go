package session

import (
	"slices"
	"testing"

	"crude/types"
)

// The workspace Aeron installs tools, runs their version commands, researches
// installation on the web and writes LSP settings. A shipped role default that
// narrows its tools makes every setup task fail, and once the application
// config is saved that stale narrowing is stored and outlives the code change.
// The check therefore goes through the stored settings form and the root
// session tool resolver, not only the role's agent-side default.
func TestAeronRootSessionKeepsTheMainAgentToolSet(t *testing.T) {
	stored := ConfigToSettings(&Config{Agents: DefaultAgentsConfig()}).Agents
	names := func(selected string, settings types.AgentsSettings) []string {
		cfg := &Config{Agents: DefaultAgentsConfig(), SelectedAgent: selected}
		ApplyWorkspaceAgentDefaults(cfg, settings)
		var out []string
		for _, def := range configuredOrchestratorTools(cfg, true, "test-model") {
			out = append(out, def.Function.Name)
		}
		return out
	}
	for label, settings := range map[string]types.AgentsSettings{
		"fresh": {},
		"saved": stored,
		"reset": {Enabled: true, Categories: types.DefaultAgentRoleCategories()},
	} {
		t.Run(label, func(t *testing.T) {
			main, aeronTools := names("", settings), names(CategoryAeron, settings)
			// Web tools join later, per provider, for every root alike.
			for _, need := range []string{"execute_command", "async_execute_command", "update_plan", "question", "delete_file"} {
				if !slices.Contains(aeronTools, need) {
					t.Errorf("Aeron root session lacks %s", need)
				}
			}
			if !slices.Equal(main, aeronTools) {
				t.Fatalf("Aeron root tools differ from the Main Agent:\nmain:  %v\naeron: %v", main, aeronTools)
			}
		})
	}
}
