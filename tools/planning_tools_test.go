package tools

import (
	"encoding/json"
	"strings"
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
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
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
		if !strings.Contains(string(schema.Properties["replace"]), `"boolean"`) || strings.Contains(tool.Function.Description, "retain every prior step") || !strings.Contains(tool.Function.Description, "omit obsolete steps") {
			t.Fatal("replacement/clean snapshot interface missing")
		}
		return
	}
	t.Fatal("update_plan tool missing")
}

// A step is its description plus a status: the schema must not advertise a
// result field, or agents will keep narrating outcomes into the plan card.
func TestUpdatePlanSchemaHasNoStepResultField(t *testing.T) {
	for _, tool := range AgentTools {
		if tool.Function.Name != "update_plan" {
			continue
		}
		var schema struct {
			Properties struct {
				Steps struct {
					Items struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"items"`
				} `json:"steps"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		stepProps := schema.Properties.Steps.Items.Properties
		if _, ok := stepProps["result"]; ok {
			t.Fatal("update_plan step schema still advertises a result field")
		}
		if reason, ok := stepProps["reason"]; !ok {
			t.Fatal("update_plan step schema lost its reason field")
		} else if strings.Contains(string(reason), "required") {
			t.Fatal("update_plan schema still describes lifecycle reasons as required")
		}
		if depends, ok := stepProps["depends_on"]; !ok {
			t.Fatal("update_plan step schema lost dependencies")
		} else if !strings.Contains(string(depends), "does not restrict update order") {
			t.Fatal("update_plan dependency schema still implies execution gating")
		}
		if strings.Contains(tool.Function.Description, "completed steps carry a one-line result") {
			t.Fatal("update_plan description still teaches result narration")
		}
		if !strings.Contains(tool.Function.Description, "not a notebook") {
			t.Fatal("update_plan description lost the banner contract")
		}
		if !strings.Contains(tool.Function.Description, "declarative reporting, not execution gates") {
			t.Fatal("update_plan description still implies ordered execution enforcement")
		}
		return
	}
	t.Fatal("update_plan tool missing")
}
