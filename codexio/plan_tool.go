// Copyright 2025 OpenAI
// SPDX-License-Identifier: Apache-2.0
// Copied from Codex plan_spec.rs at aa380897f67b; represented in Go for Crude.
package codexio

import "encoding/json"

func UpdatePlanTool() FunctionTool {
	return FunctionTool{Name: "update_plan", Description: "Updates the task plan.\nProvide an optional explanation and a list of plan items, each with a step and status.\nAt most one step can be in_progress at a time.\n", Parameters: json.RawMessage(`{
"type":"object","additionalProperties":false,"required":["plan"],"properties":{
"explanation":{"type":"string","description":"Optional explanation for this plan update."},
"plan":{"type":"array","description":"The list of steps","items":{"type":"object","additionalProperties":false,"required":["step","status"],"properties":{"step":{"type":"string","description":"Task step text."},"status":{"type":"string","enum":["pending","in_progress","completed"],"description":"Step status."}}}}
}}`)}
}
