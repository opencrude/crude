package tools

import (
	"encoding/json"
	"testing"
)

func TestPlanningToolSurfaceIsCanonicalOnly(t *testing.T) {
	names := make(map[string]bool, len(AgentTools))
	for _, tool := range AgentTools {
		names[tool.Function.Name] = true
	}
	for _, required := range []string{"update_plan", "get_plan"} {
		if !names[required] {
			t.Fatalf("canonical planning tool %q missing", required)
		}
	}
	for _, removed := range []string{
		"plan", "mark_step_complete", "mark_step_doing", "mark_step_failed",
		"report_blocker", "resolve_blocker", "list_goal_status",
	} {
		if names[removed] {
			t.Fatalf("removed planning tool %q is still advertised", removed)
		}
	}
}

func TestUpdatePlanSchemaRequiresOnlyPlanIDAtTransportBoundary(t *testing.T) {
	var schema struct {
		Required []string `json:"required"`
	}
	for _, tool := range AgentTools {
		if tool.Function.Name != "update_plan" {
			continue
		}
		if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		if len(schema.Required) != 1 || schema.Required[0] != "plan_id" {
			t.Fatalf("update_plan transport required fields = %v", schema.Required)
		}
		return
	}
	t.Fatal("update_plan tool missing")
}
