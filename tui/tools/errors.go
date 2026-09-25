package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolErrorReason renders a short, human reason for a tool failure whose
// payload is structured. Planning tools answer with canonical JSON addressed to
// the model; a row must never dump that payload at the user, so the failure
// reads as "<tool> failed — <reason>". Edit-tool failures carry prose plus a
// trailing "Hint: …" line addressed to the model's next call, so their row keeps
// only the first line as the reason. Returns "" when the content carries no
// recognizable reason (plain-text errors keep their own message).
func ToolErrorReason(tool, content string) string {
	if tool == "multi_tool_use.parallel" {
		var batch struct {
			Results []struct {
				Error bool `json:"error"`
			} `json:"results"`
		}
		if json.Unmarshal([]byte(content), &batch) == nil && len(batch.Results) > 0 {
			failed := 0
			for _, result := range batch.Results {
				if result.Error {
					failed++
				}
			}
			if failed > 0 {
				return fmt.Sprintf("%d failed", failed)
			}
		}
		return ""
	}
	if isEditFailureTool(tool) {
		return editFailureReason(content)
	}
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

// isEditFailureTool reports file-mutation tools whose failures pair a one-line
// reason with a model-facing hint.
func isEditFailureTool(name string) bool {
	switch name {
	case "edit_file", "write_file":
		return true
	default:
		return false
	}
}

// editFailureReason compacts an edit-tool failure into its reason line: the
// first line with the "Error: " label removed. The hint line that follows is an
// instruction for the model's retry, never row content.
func editFailureReason(content string) string {
	text := strings.TrimSpace(content)
	if !strings.HasPrefix(text, "Error:") {
		return ""
	}
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		text = text[:newline]
	}
	return CollapseLine(strings.TrimSpace(strings.TrimPrefix(text, "Error:")))
}

// CollapseLine flattens any run of whitespace — including newlines — into
// single spaces so a value destined for a one-line row can never wrap or
// smuggle a paragraph into a title.
func CollapseLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
