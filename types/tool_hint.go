package types

import "strings"

// ToolHintSeparator divides a tool failure from its trailing model-facing
// hint. The hint text is owned by prompt (prompt.WithToolHint); this is the
// presentation side of the same contract.
const ToolHintSeparator = "\n\nHint: "

// StripToolHint removes a trailing model-facing hint from tool output, so
// rows show the failure the user cares about, never instructions addressed
// to the model.
func StripToolHint(content string) string {
	if index := strings.LastIndex(content, ToolHintSeparator); index >= 0 {
		return strings.TrimRight(content[:index], " \t\r\n")
	}
	return content
}
