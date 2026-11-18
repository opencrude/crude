package tools

import "encoding/json"

// GoalTools let the main agent of a session read and settle its /goal. Only
// the root agent has them; the user owns pausing, resuming and replacing.
var GoalTools = []ToolDef{
	capabilityTool("get_goal", "Get this session's goal: objective, status, the reason for that status (blockers or verifier findings), whether a verifier is running, the time and tokens spent, and the token budget and remaining tokens when it has one.", `{"type":"object","properties":{},"additionalProperties":false}`),
	capabilityTool("create_goal", "Start a goal only when the user explicitly asks for one in this conversation; never infer a goal from an ordinary task. Set token_budget only when the user explicitly asks for a token budget. Fails while an unfinished goal exists: the user replaces goals with /goal.", `{"type":"object","properties":{"objective":{"type":"string","description":"The concrete objective, in the user's terms."},"token_budget":{"type":"integer","minimum":1,"description":"Positive token budget for the goal. Omit unless the user explicitly asked for one."}},"required":["objective"],"additionalProperties":false}`),
	capabilityTool("update_goal", "Settle the active goal. status \"complete\" claims the objective is achieved: it starts an independent verifier that checks the current state; the goal completes only on its verdict, and otherwise its findings come back to you. Claim complete only after your own audit proves every requirement, never because work is slow or you are stopping. status \"blocked\" only after the same blocker repeated for three consecutive goal turns and you cannot progress without the user or an external change. status \"paused\" only at the user's explicit request. You cannot resume, budget-limit or replace a goal; the user and the runtime own those.", `{"type":"object","properties":{"status":{"type":"string","enum":["complete","blocked","paused"]},"evidence":{"type":"string","description":"For complete: what you did and how you verified each requirement, for the verifier to check."},"reason":{"type":"string","description":"For blocked or paused: one short sentence the user reads."}},"required":["status"],"additionalProperties":false}`),
}

// WithGoalVerdict gives a goal verifier's publish_report a required verdict,
// so the runtime reads a typed answer instead of prose. The executor moves
// it into the report's goal_verdict metadata.
func WithGoalVerdict(defs []ToolDef) []ToolDef {
	out := make([]ToolDef, len(defs))
	copy(out, defs)
	for index, def := range out {
		if def.Function.Name != "publish_report" {
			continue
		}
		var schema map[string]any
		if json.Unmarshal(def.Function.Parameters, &schema) != nil {
			continue
		}
		properties, _ := schema["properties"].(map[string]any)
		if properties == nil {
			continue
		}
		properties["verdict"] = map[string]any{
			"type": "string", "enum": []string{"achieved", "not_achieved"},
			"description": "Your verdict on the goal: achieved only when the evidence proves every requirement, otherwise not_achieved.",
		}
		required, _ := schema["required"].([]any)
		schema["required"] = append(required, "verdict")
		raw, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		def.Function.Parameters = raw
		out[index] = def
	}
	return out
}
