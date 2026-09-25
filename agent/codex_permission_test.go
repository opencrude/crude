package agent

import (
	"context"
	"strings"
	"testing"

	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
)

func TestPatchPermissionDenialAndAlways(t *testing.T) {
	input := "*** Begin Patch\n*** Update File: a\n*** Move to: b\n-a\n+b\n*** Delete File: c\n*** End Patch"
	for _, decision := range []string{"deny", "always"} {
		t.Run(decision, func(t *testing.T) {
			a := newPermissionLifecycleAgent(t)
			a.Model = "gpt-test"
			a.Policy = safety.DefaultPolicy()
			a.configureModelTools()
			calls := 0
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				calls++
				return tools.ToolResult{Content: "Success."}, nil
			}
			ch := make(chan ttypes.BlockResponse, 1)
			granted := false
			result := a.executeOneToolCall(t.Context(), provider.ToolCall{ID: "p1", InternalID: "pi1", Type: "custom", Function: provider.FunctionCall{Name: "apply_patch", Arguments: input}}, func(event AgentEvent) {
				if event.Type == "blocking" {
					data := event.Data.(map[string]string)
					if data["patch_input"] != input || !strings.Contains(data["target"], "b") {
						t.Fatal("permission omitted operands")
					}
					ch <- ttypes.BlockResponse{RequestID: "pi1", Outcome: ttypes.BlockAnswered, Value: decision}
				}
				if event.Type == "confirmation_always" {
					granted = event.Data.(map[string]string)["patch_input"] == input
				}
			}, ch, new(string))
			if decision == "deny" && (!result.toolError || calls != 0) {
				t.Fatal("denied patch executed")
			}
			if decision == "always" && (result.toolError || calls != 1 || !granted) {
				t.Fatalf("always lost: %+v %d %t", result, calls, granted)
			}
		})
	}
}

func TestEmptyToolFilterCannotRestoreFullGPTTools(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	a.FilteredTools = FilterToolsForModel(tools.AgentTools, []string{"not-a-tool"}, nil, "", false, a.Model)
	a.configureModelTools()
	if a.FilteredTools == nil || len(a.FilteredTools) != 0 {
		t.Fatal("empty permissions restored default tools")
	}
}

func TestPatchExplicitToolSelection(t *testing.T) {
	for _, role := range []string{CatCrafter, CatAeron, "custom"} {
		defs := FilterToolsForModel(tools.AgentTools, []string{"apply_patch"}, nil, role, true, "gpt-test")
		if findToolDef("apply_patch", defs) == nil {
			t.Fatalf("explicit patch missing for %s", role)
		}
		denied := FilterToolsForModel(tools.AgentTools, nil, []string{"apply_patch"}, role, true, "gpt-test")
		if findToolDef("apply_patch", denied) != nil {
			t.Fatal("explicit tool denial ignored")
		}
	}
}
