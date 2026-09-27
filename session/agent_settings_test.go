package session

import (
	"strings"
	"testing"

	"crude/agent"
	"crude/tools"
	"crude/types"
)

func TestAgentSettingsRoundTripAndRuntimeRestrictions(t *testing.T) {
	cfg := &Config{Agents: DefaultAgentsConfig()}
	settings := ConfigToSettings(cfg).Agents
	settings.Categories[CategoryDefault] = types.CategorySettings{Instructions: "Explain uncertainty", DeniedTools: []string{"execute_command", "write_file", "edit_file"}}
	settings.AgentProfiles = map[string]types.AgentProfileSettings{"audit": {Category: CategoryResearcher, Instructions: "Review safety", Description: "Independent review", DeniedTools: []string{"web_fetch"}}}
	ApplyWorkspaceAgentDefaults(cfg, settings)
	got := ConfigToSettings(cfg).Agents
	if got.Categories[CategoryDefault].Instructions != "Explain uncertainty" || got.AgentProfiles["audit"].Instructions != "Review safety" {
		t.Fatalf("agent settings round-trip lost data: %+v", got)
	}
	for _, tool := range configuredOrchestratorTools(cfg, true, "gpt-test") {
		if tool.Function.Name == "exec_command" || tool.Function.Name == "write_stdin" || tool.Function.Name == "apply_patch" {
			t.Fatalf("native tool alias bypassed restrictions: %s", tool.Function.Name)
		}
	}
	runner := &SubAgentRunner{effectiveCfg: cfg}
	spec := agent.SubAgentSpec{Category: CategoryCrafter, AllowedTools: []string{"web_fetch"}, Instructions: "Task-specific instructions"}
	instructions := runner.resolveProfile("audit", CategoryCrafter, &spec)
	if spec.Category != CategoryResearcher || !strings.Contains(instructions, "Review safety") || spec.Instructions != "Task-specific instructions" {
		t.Fatalf("named profile resolution lost its role or prompt: %+v %q", spec, instructions)
	}
	for _, tool := range agent.FilterTools(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, spec.Category, true) {
		if tool.Function.Name == "web_fetch" {
			t.Fatal("model-supplied allow list bypassed configured block")
		}
	}
	if !strings.Contains(agentProfileCatalog(cfg), `name="audit"`) {
		t.Fatal("custom agent is not discoverable to delegation")
	}
}

func TestNamedAgentProfilePinsItsConfiguredConnection(t *testing.T) {
	percent, rolePercent := 72, 80
	cfg := &Config{Agents: DefaultAgentsConfig()}
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryResearcher] = CategoryConfig{Model: "role-model", AccountID: "personal", ContextCompactionPercent: &rolePercent}
	cfg.Agents.Profiles["default"] = profile
	cfg.Agents.AgentProfiles["reviewer"] = AgentProfileConfig{Category: CategoryResearcher, Model: "review-model", AccountID: "work", ContextCompactionPercent: &percent}
	runner := &SubAgentRunner{effectiveCfg: cfg}
	for _, spec := range []agent.SubAgentSpec{{}, {Model: "requested-model", AccountID: "requested-account"}} {
		runner.resolveProfile("reviewer", CategoryResearcher, &spec)
		if spec.Model != "review-model" || spec.AccountID != "work" || spec.ContextCompactionPercent == nil || *spec.ContextCompactionPercent != percent {
			t.Fatalf("named profile did not own its configured connection: %+v", spec)
		}
	}
}
