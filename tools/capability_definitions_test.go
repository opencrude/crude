package tools

import (
	"encoding/json"
	"testing"
)

func TestCapabilityActivationAndAccessHaveDisjointRequiredSchemas(t *testing.T) {
	found := make(map[string]bool)
	for _, tool := range CapabilityTools {
		name := tool.Function.Name
		if name != "capability_activate" && name != "mcp_request_access" {
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		found[name] = true
		if name == "capability_activate" {
			if len(schema.Properties) != 1 || schema.Properties["id"] == nil ||
				len(schema.Required) != 1 || schema.Required[0] != "id" {
				t.Fatal("a provider requiring all properties could force MCP grant data into skill activation")
			}
		} else if len(schema.Properties) != 2 || schema.Properties["connection_id"] == nil ||
			schema.Properties["grants"] == nil || len(schema.Required) != 2 {
			t.Fatal("exact access review lacks its own required connection/grant schema")
		}
	}
	if len(found) != 2 {
		t.Fatal("activation and exact MCP access are not separately discoverable")
	}
}
