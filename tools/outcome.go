package tools

import (
	"strings"

	"crude/types/toolresult"
)

// SettleResult publishes one outcome from execution facts. It never inspects
// diagnostic wording to decide cancellation, denial or uncertain effects.
func SettleResult(name string, result ToolResult, failed bool) ToolResult {
	if result.Outcome.State == "" {
		result.Outcome.State = toolresult.Succeeded
		if failed {
			result.Outcome.State = toolresult.Failed
		}
		if command, ok := ParseCommandResult(name, result.Content); ok {
			result.Outcome.ID = command.ExecutionID
			switch {
			case command.Uncertain:
				result.Outcome.State = toolresult.Uncertain
				result.Outcome.Summary = "The command may still be running. Check its state."
			case command.Cancelled:
				result.Outcome.State = toolresult.Cancelled
			case command.Failed():
				result.Outcome.State = toolresult.Failed
			}
			if command.Error != "" && result.Outcome.Unsuccessful() && result.Outcome.Summary == "" {
				result.Outcome.Summary = ResultReason(command.Error)
			}
		}
	}
	if result.Outcome.Summary == "" && result.Outcome.Unsuccessful() {
		result.Outcome.Summary = ResultReason(result.Content)
	}
	return result
}

// ResultReason extracts only factual prose. Structured producers supply their
// own summary; arbitrary JSON is evidence, never a headline.
func ResultReason(content string) string {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return ""
	}
	line, _, _ := strings.Cut(text, "\n")
	return strings.Join(strings.Fields(strings.TrimPrefix(line, "Error:")), " ")
}
