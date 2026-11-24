package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityToolGuidanceExplainsDiscoveryAndDurableUse(t *testing.T) {
	expected := map[string][]string{
		"capability_search":   {"distinctive keywords, not a sentence", "each keyword is matched separately", "search its name", "Omit kind", "descriptors, not loaded instructions", "exact ID unchanged", "Discovery is not an access grant"},
		"capability_activate": {"not a display name or path", "user-owned application registrations", "scope \"workspace\"", "this project to own the binding", "bundled files resolve from that path", "Connected MCP tools appear on the next request", "same permission mode", "starts a background job and returns its handle at once", "do not start it again or poll", "capabilities.save_connection"},
	}
	for _, tool := range CapabilityTools {
		phrases, exists := expected[tool.Function.Name]
		if !exists {
			continue
		}
		// Field descriptions are part of the same guidance as the tool's own.
		guidance := tool.Function.Description + string(tool.Function.Parameters)
		for _, phrase := range phrases {
			if !strings.Contains(guidance, phrase) {
				t.Errorf("%s lacks lifecycle guidance %q", tool.Function.Name, phrase)
			}
		}
		delete(expected, tool.Function.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing capability definitions: %v", expected)
	}
}

func TestCapabilityActivationRequiresOnlyID(t *testing.T) {
	for _, tool := range CapabilityTools {
		if tool.Function.Name == "mcp_request_access" {
			t.Fatal("the model should not request a separate grant")
		}
		if tool.Function.Name == "capability_activate" {
			var schema struct{ Required []string }
			if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil || len(schema.Required) != 1 || schema.Required[0] != "id" {
				t.Fatal("activation requires more than a capability ID")
			}
		}
	}
}
