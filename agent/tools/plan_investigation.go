package tools

import "encoding/json"

// OpenPlanInvestigationTool is offered only on root Plan mode turns.
var OpenPlanInvestigationTool = ToolDef{
	Type: "function",
	Function: ToolFuncDef{
		Name:        "open_plan_investigation",
		Description: "Open or reuse a separate checkout for a small Plan mode experiment. Returns the folder to read. Plan file changes use only this checkout. Running project commands needs the user's separate permission.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	},
}
