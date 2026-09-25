package provider

import (
	"encoding/json"
	"fmt"
)

// ToolCallParams is the single execution/presentation decoder. Custom inputs
// remain raw bytes; never JSON-repair or quote a freeform patch.
func ToolCallParams(call ToolCall) (map[string]any, error) {
	if call.Type == "custom" {
		return map[string]any{"input": call.Function.Arguments}, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &params); err != nil {
		return nil, err
	}
	if params == nil {
		return nil, fmt.Errorf("tool arguments must be an object")
	}
	return params, nil
}
