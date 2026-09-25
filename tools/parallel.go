package tools

import "encoding/json"

// ParallelTool exposes the namespace used by the upstream GPT instructions.
// Calls still traverse normal per-agent availability, policy, and permission
// checks; the batch is not a second tool authority.
func ParallelTool() ToolDef {
	return ToolDef{Type: "function", Function: ToolFuncDef{Name: "multi_tool_use.parallel", Description: "Run independent tool calls in parallel. Each recipient_name names an available tool; parameters are its arguments. For a freeform tool, supply its raw text in the input field.", Parameters: json.RawMessage(`{
"type":"object","required":["tool_uses"],"properties":{"tool_uses":{"type":"array","minItems":1,"items":{"type":"object","required":["recipient_name","parameters"],"properties":{"recipient_name":{"type":"string"},"parameters":{"type":"object"}},"additionalProperties":false}}},"additionalProperties":false
}`)}}
}
