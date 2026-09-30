package tools

import (
	"crude/tui/types"
	"strings"
	"testing"
	"time"
)

func TestToolErrorReasonSummarizesPlanFailures(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		content string
		want    string
	}{
		{
			name:    "revision conflict",
			tool:    "update_plan",
			content: `{"error":"revision_conflict","expected_revision":1,"received_revision":2,"current_plan":{"id":"p"}}`,
			want:    "revision conflict (expected 1, received 2)",
		},
		{
			name:    "invalid update",
			tool:    "update_plan",
			content: `{"error":"invalid_plan_update","message":"plan step \"a\" requires a reason when skipped"}`,
			want:    `plan step "a" requires a reason when skipped`,
		},
		{
			name:    "no active plan",
			tool:    "get_plan",
			content: `{"error":"no_active_plan"}`,
			want:    "no active plan",
		},
		{
			name:    "unknown payload",
			tool:    "update_plan",
			content: `{"unexpected":true}`,
			want:    "",
		},
		{
			name:    "plain text is not structured",
			tool:    "update_plan",
			content: "Error: update_plan: 'plan_id' is required",
			want:    "",
		},
		{
			name:    "other tools keep their own message",
			tool:    "execute_command",
			content: `{"error":"revision_conflict"}`,
			want:    "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToolErrorReason(c.tool, c.content); got != c.want {
				t.Fatalf("ToolErrorReason() = %q, want %q", got, c.want)
			}
		})
	}
}

// An edit-tool failure carries prose plus a model-facing hint; the row keeps the
// reason line and never the hint, so the failure reads as one compact red row.
func TestToolErrorReasonCompactsEditFailures(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		content string
		want    string
	}{
		{
			name:    "no-change patch",
			tool:    "edit_file",
			content: "Error: no changes to make: old_string and new_string are exactly the same\nHint: match the file's current bytes exactly.",
			want:    "no changes to make: old_string and new_string are exactly the same",
		},
		{
			name:    "ambiguous patch keeps its location detail",
			tool:    "edit_file",
			content: "Error: op[0]: search text matched 2 locations after whitespace normalisation\nHint: use read_file to verify the exact content before retrying.",
			want:    "op[0]: search text matched 2 locations after whitespace normalisation",
		},
		{
			name:    "write failure",
			tool:    "write_file",
			content: "Error: write x.go: permission denied",
			want:    "write x.go: permission denied",
		},
		{
			name:    "content without the error label is left alone",
			tool:    "edit_file",
			content: "Deleted: x.go",
			want:    "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ToolErrorReason(c.tool, c.content)
			if got != c.want {
				t.Fatalf("ToolErrorReason() = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "Hint:") {
				t.Fatalf("hint leaked into the row reason: %q", got)
			}
		})
	}
}

// The compact failure row is one line: the reason rides the title, so no body
// payload is rendered below it.
func TestEditFailureRowKeepsOneLine(t *testing.T) {
	got := BuildErrorLabelWithReason("edit_file", "tools/dispatcher.go", "2ms",
		ToolErrorReason("edit_file", "Error: no changes to make: old_string and new_string are exactly the same\nHint: read_file first."))
	want := "Edit File failed tools/dispatcher.go 2ms — no changes to make: old_string and new_string are exactly the same"
	if got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

func TestBuildErrorLabelWithReasonKeepsOneLine(t *testing.T) {
	got := BuildErrorLabelWithReason("update_plan", "plan-1", "1s", "revision conflict (expected 1, received 2)")
	want := "Update plan failed plan-1 1s — revision conflict (expected 1, received 2)"
	if got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
	if plain := BuildErrorLabel("update_plan", "", "1s"); plain != "Update plan failed 1s" {
		t.Fatalf("reasonless label = %q", plain)
	}
}

func TestToolTitleDoesNotInferRolesOrFailureFromOperandText(t *testing.T) {
	command := "rg 'Command failed 1s' source.go"
	msg := types.ChatMessage{ToolName: "execute_command", ToolCommand: command, ToolStatus: types.ToolStatusSuccess, ToolDuration: time.Second}
	title := BuildToolTitle(msg, types.ToolPreviewDone)
	if title.Action != "Ran" || title.Operand != command || title.Metadata != "1s" || title.Failed {
		t.Fatalf("operand text changed title semantics: %+v", title)
	}
	msg.ToolStatus = types.ToolStatusError
	title = BuildToolTitle(msg, types.ToolPreviewError)
	if !title.Failed || title.Action != "Command failed" || title.Operand != command || title.Metadata != "1s" {
		t.Fatalf("failure changed operand or metadata semantics: %+v", title)
	}
}

func TestSubAgentLaunchFailureKeepsItsDiagnostic(t *testing.T) {
	msg := types.ChatMessage{
		ToolName: "run_sub_agent", ToolArgs: "Inspect renderer", ToolStatus: types.ToolStatusError,
		Content: "Error launching sub-agent: account unavailable",
	}
	title := BuildToolTitle(msg, types.ToolPreviewError)
	if !title.Failed || title.Action != "Failed to spawn sub-agent" || title.Operand != "Inspect renderer" {
		t.Fatalf("launch failure title = %+v", title)
	}
}
