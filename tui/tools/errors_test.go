package tools

import "testing"

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
