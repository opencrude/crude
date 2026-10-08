// Copyright 2025 OpenAI
// SPDX-License-Identifier: Apache-2.0
// Copied from Codex plan_spec.rs at aa380897f67b; represented in Go for Crude.
package codexio

import "encoding/json"

func UpdatePlanTool() FunctionTool {
	return FunctionTool{Name: "update_plan", Description: "Update the compact task plan with the complete desired ordered snapshot; omit obsolete steps, which remain in revision history. For a materially new goal set replace=true and provide objective and plan. Review latest intent, update at milestones before the next stage, and honestly complete or cancel before the final answer. Empty plan closes existing work as cancelled unless an explicit status is supplied. At most one step should usually be in_progress.\n", Parameters: json.RawMessage(`{
"type":"object","additionalProperties":false,"required":["plan"],"properties":{
"objective":{"type":"string","description":"Short goal; required with replace=true."},
"replace":{"type":"boolean","description":"Atomically supersede the current goal and reset step provenance; requires objective and a nonempty plan."},
"revision":{"type":"integer","minimum":0,"description":"Current revision from the previous result; a stale value fails without changing the plan. Omit to use current owner revision."},
"status":{"type":"string","enum":["active","paused","completed","cancelled"],"description":"Explicit lifecycle; use completed only for finished work and cancelled for abandoned work."},
"explanation":{"type":"string","description":"Optional explanation for this plan update."},
"plan":{"type":"array","description":"The list of steps","items":{"type":"object","additionalProperties":false,"required":["step","status"],"properties":{"step":{"type":"string","description":"Task step text."},"status":{"type":"string","enum":["pending","in_progress","completed","skipped","blocked","failed","cancelled"],"description":"Step status."}}}}
}}`)}
}
