package tools

import (
	"testing"

	"crude/types/toolresult"
)

func TestOutcomeUsesExecutionFactsNotDiagnosticWords(t *testing.T) {
	for _, tc := range []struct {
		name, tool, content string
		toolError           bool
		want                toolresult.State
	}{
		{"error mentioning cancellation", "edit_file", "Error: the cancelled field was not found", true, toolresult.Failed},
		{"successful literal error text", "read_file", "Error: a line in the source file", false, toolresult.Succeeded},
		{"observed failed process", "check_async_command", `{"output_descriptor":{"status":"failed","total_bytes":0},"exit_code":7,"output":""}`, false, toolresult.Failed},
		{"stopped process", "exec_command", `{"cancelled":true,"exit_code":1,"output":""}`, true, toolresult.Cancelled},
		{"uncertain tree", "execute_command", `{"uncertain":true,"output_descriptor":{"status":"failed","total_bytes":0},"output":""}`, true, toolresult.Uncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := SettleResult(tc.tool, ToolResult{Content: tc.content}, tc.toolError)
			if out.Outcome.State != tc.want || out.Content != tc.content {
				t.Fatalf("outcome changed the evidence or guessed from its wording: %+v", out)
			}
		})
	}
}
