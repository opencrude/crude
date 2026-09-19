package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolErrorReason renders a short, human reason for a tool failure whose
// payload is structured. Planning tools answer with canonical JSON addressed to
// the model; a row must never dump that payload at the user, so the failure
// reads as "<tool> failed — <reason>". Returns "" when the content carries no
// recognizable reason (plain-text errors keep their own message).
func ToolErrorReason(tool, content string) string {
	if !IsPlanTool(tool) {
		return ""
	}
	var payload struct {
		Error    string `json:"error"`
		Message  string `json:"message"`
		Expected uint64 `json:"expected_revision"`
		Received uint64 `json:"received_revision"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(content)), &payload) != nil {
		return ""
	}
	switch payload.Error {
	case "revision_conflict":
		return fmt.Sprintf("revision conflict (expected %d, received %d)", payload.Expected, payload.Received)
	case "invalid_plan_update":
		if message := CollapseLine(payload.Message); message != "" {
			return message
		}
		return "invalid plan update"
	case "no_active_plan":
		return "no active plan"
	case "plan_not_found":
		return "plan not found"
	default:
		return ""
	}
}

// CollapseLine flattens any run of whitespace — including newlines — into
// single spaces so a value destined for a one-line row can never wrap or
// smuggle a paragraph into a title.
func CollapseLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
