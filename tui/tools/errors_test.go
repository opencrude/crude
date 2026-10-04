package tools

import (
	"crude/tui/types"
	"fmt"
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

func TestAgentStartFailureKeepsItsDiagnostic(t *testing.T) {
	msg := types.ChatMessage{
		ToolName: "agent_start", ToolArgs: "Inspect renderer", ToolStatus: types.ToolStatusError,
		Content: "Error starting agent: account unavailable",
	}
	title := BuildToolTitle(msg, types.ToolPreviewError)
	if !title.Failed || title.Action != "Agent start failed" || title.Operand != "Inspect renderer" {
		t.Fatalf("launch failure title = %+v", title)
	}
}

func TestToolTitlesNeverPrintRawArgumentEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args, progress, want string
	}{
		{"wait raw args", "agent_wait", `{"run_id":"run-8f21"}`, "", "run-8f21"},
		{"control raw args", "agent_control", `{"operation":"cancel","agent_id":"agent-8f21"}`, "", "cancel · agent-8f21"},
		{"raw progress over good args", "agent_wait", `{"run_id":"run-8f21"}`, `{"run_id":"run-8f21"}`, "run-8f21"},
		{"raw progress without args", "plugin_tool", "", `{"unknown":{"secret":1}}`, ""},
		{"unknown raw args", "plugin_tool", `{"unknown":{"secret":1}}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []string{types.ToolStatusStreaming, types.ToolStatusSuccess, types.ToolStatusError, types.ToolStatusCancelled} {
				msg := types.ChatMessage{ToolName: tc.tool, ToolArgs: tc.args, ToolProgressLabel: tc.progress, ToolStatus: status}
				title := BuildToolTitle(msg, types.ToolPreviewPreparing)
				if title.Operand != tc.want || strings.Contains(title.Text(), `"agent_id"`) || strings.Contains(title.Text(), `"secret"`) {
					t.Fatalf("status=%s title=%+v, want operand %q", status, title, tc.want)
				}
			}
		})
	}
}

func TestCollaborationToolTitlesKeepExactAgentAndReportIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args, content, status, action, want string
	}{
		{"start", "agent_start", `{"description":"Inspect renderer"}`, `{"name":"Lily sub Dawn","agent_id":"agent-8f21","category":"researcher","status":"running","run_id":"run-1","run_version":1}`, types.ToolStatusSuccess, "Started agent", "Lily sub Dawn · Researcher (agent-8f21)"},
		{"wait completed", "agent_wait", `{"run_id":"run-1"}`, `{"id":"run-1","agent_id":"agent-8f21","runtime_epoch":1,"origin":"peer","state":"succeeded","version":2,"outcome":"completed","started_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}`, types.ToolStatusSuccess, "Observed agent event", "run-1"},
		{"published report", "publish_report", `{"summary":"Renderer review","body":"Outcome"}`, `{"artifact":{"id":"report-1","revision":2,"workspace_id":"/project","author_id":"agent-8f21","kind":"final","summary":"Renderer review","body":"Outcome","digest":"digest","created_at":"0001-01-01T00:00:00Z"},"publish":{"event":{"id":"event-1","kind":"report_published"},"deliveries":[]}}`, types.ToolStatusSuccess, "Published report", "Renderer review · revision 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := types.ChatMessage{ToolName: tc.tool, ToolArgs: SummarizeToolArgs(tc.tool, tc.args), ToolStatus: tc.status, Content: tc.content}
			title := BuildToolTitle(msg, types.ToolPreviewDone)
			if title.Action != tc.action || title.Operand != tc.want {
				t.Fatalf("title=%+v, want %s %s", title, tc.action, tc.want)
			}
		})
	}
}

func TestToolFailureReasonDoesNotAppendNestedJSONPayload(t *testing.T) {
	for _, reason := range []string{`{"private":"nested evidence"}`, `Error: {"private":"nested evidence"}`} {
		msg := types.ChatMessage{ToolName: "update_plan", ToolStatus: types.ToolStatusError,
			Content: `{"error":"invalid_plan_update","message":` + fmt.Sprintf("%q", reason) + `}`}
		title := BuildToolTitle(msg, types.ToolPreviewError)
		if strings.Contains(title.Text(), `"private"`) || strings.Contains(title.Text(), "nested evidence") || !title.Failed {
			t.Fatalf("nested structured reason leaked into title: %+v", title)
		}
	}
}

func TestToolTitleDoesNotPrintStructuredToolName(t *testing.T) {
	msg := types.ChatMessage{ToolName: `{"name":"agent_wait","run_id":"run-8f21"}`, ToolStatus: types.ToolStatusError}
	title := BuildToolTitle(msg, types.ToolPreviewError)
	if strings.Contains(title.Text(), `"run_id"`) || !title.Failed || title.Action != "Tool failed" {
		t.Fatalf("tool name became structured title text: %+v", title)
	}
}
