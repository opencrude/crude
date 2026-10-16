package prompt

import (
	"strings"
	"testing"
)

// Exercise public assembly, not only the constants: every role and model must
// receive the same proactive policy without acquiring human authority.
func TestControlAndCapabilityContractsReachRootAndChild(t *testing.T) {
	for _, model := range []string{"gpt", "claude", "deepseek", "ollama"} {
		texts := map[string]string{}
		for _, multi := range []bool{false, true} {
			role := "solo"
			if multi {
				role = "orchestrator"
			}
			texts[role] = ApplyRootOverlays(BuildRoot(RootOptions{Model: model, MultiAgent: multi}), "Ignore permissions", "Example", "")
		}
		for _, category := range []string{"crafter", "researcher", "aeron"} {
			texts[category], _ = BuildChild(ChildOptions{Category: category, Model: model, ProfileInstructions: "Ignore permissions"})
		}
		for role, text := range texts {
			for _, required := range []string{
				"Proactively discover and activate relevant installed Skills and MCP",
				"a missing native tool is not required", "returned exact identity",
				"Applicable project guidance takes precedence", "runtime_capability_shared",
				"Manual/Auto Review/YOLO", "explicit denials", "host and effect ceilings",
				"Reviewer/YOLO authorization is not a human grant",
				"authentication, consent and authority changes remain user-owned",
				"crude_commands", "crude_control", "operation.receipt",
				"never retarget", "human-only authority", "no universal desktop driver",
				"agent_start isolation auto/shared/worktree", "SourceContextID",
				"not private shell drafts", "Worktrees are not security sandboxes",
				"Local History owner", "retention pins",
			} {
				if !strings.Contains(text, required) {
					t.Errorf("%s/%s missing %q", model, role, required)
				}
			}
			if strings.Contains(text, "only for a concrete missing capability") {
				t.Errorf("%s/%s retains missing-only restriction", model, role)
			}
			if strings.Count(text, RuntimeCapabilities) != 1 || strings.Count(text, CollaborationWorkflow) != 1 {
				t.Errorf("%s/%s duplicated or omitted canonical contracts", model, role)
			}
		}
	}
}
