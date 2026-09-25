package codexio

import (
	"encoding/json"
	"testing"
)

func TestNativeFunctionSchemas(t *testing.T) {
	for _, def := range []FunctionTool{ExecCommandTool(), WriteStdinTool(), UpdatePlanTool()} {
		var schema map[string]any
		if err := json.Unmarshal(def.Parameters, &schema); err != nil {
			t.Fatalf("%s: %v", def.Name, err)
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("%s: %s", def.Name, def.Parameters)
		}
	}
}
