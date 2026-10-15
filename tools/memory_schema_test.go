package tools

import (
	"encoding/json"
	"slices"
	"testing"
)

// The memory evidence schema must advertise every field memory.validateEvidence
// enforces; an under-specified schema made valid-looking calls fail.
func TestMemoryEvidenceSchemaMatchesValidation(t *testing.T) {
	for _, name := range []string{"remember", "update_memory", "memory_feedback"} {
		var definition *ToolFuncDef
		for index := range AgentTools {
			if AgentTools[index].Function.Name == name {
				definition = &AgentTools[index].Function
			}
		}
		if definition == nil {
			t.Fatalf("%s not defined", name)
		}
		var schema map[string]any
		if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
			t.Fatalf("%s schema is invalid JSON: %v", name, err)
		}
		properties := schema["properties"].(map[string]any)
		evidence, ok := properties["evidence"]
		if name == "memory_feedback" {
			item := properties["items"].(map[string]any)["items"].(map[string]any)
			evidence, ok = item["properties"].(map[string]any)["evidence"]
		}
		if !ok {
			t.Fatalf("%s has no evidence schema", name)
		}
		item := evidence.(map[string]any)["items"].(map[string]any)
		var required []string
		for _, field := range item["required"].([]any) {
			required = append(required, field.(string))
		}
		for _, field := range []string{"kind", "summary", "outcome"} {
			if !slices.Contains(required, field) {
				t.Fatalf("%s evidence schema does not require %s: %v", name, field, required)
			}
		}
		if slices.Contains(required, "session_id") {
			t.Fatalf("%s evidence must not require session_id the model cannot know", name)
		}
	}
}
