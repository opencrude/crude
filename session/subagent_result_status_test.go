package session

import (
	"context"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
)

func TestSubAgentTerminalResultNeedsDeliverable(t *testing.T) {
	cases := []struct {
		name, reason string
		messages     []provider.ChatMessage
		authored     bool
		errors       bool
		cancelled    bool
		want         agent.SubAgentStatus
	}{
		{name: "only assignment", want: agent.SubAgentFailedPolicy},
		{name: "tool calls then terminal stream error", reason: "error", errors: true,
			messages: []provider.ChatMessage{
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
				provider.NewTextMessage("tool", "file contents"),
				provider.NewTextMessage("assistant", "partial stream output"),
			}, want: agent.SubAgentFailedError},
		{name: "no report and no final answer despite tool success", messages: []provider.ChatMessage{
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
			provider.NewTextMessage("tool", "file contents"),
		}, want: agent.SubAgentFailedPolicy},
		{name: "earlier answer does not cover later dangling call", messages: []provider.ChatMessage{
			provider.NewTextMessage("assistant", "earlier progress"),
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
		}, want: agent.SubAgentFailedPolicy},
		{name: "recovered error with authored report", reason: "completed", errors: true, authored: true, want: agent.SubAgentCompleted},
		{name: "recovered error with final answer but no report", reason: "completed", errors: true,
			messages: []provider.ChatMessage{provider.NewTextMessage("assistant", "Final findings and outcome.")}, want: agent.SubAgentFailedPolicy},
		{name: "final answer without report", reason: "completed",
			messages: []provider.ChatMessage{provider.NewTextMessage("assistant", "Final findings and outcome.")}, want: agent.SubAgentFailedPolicy},
		{name: "authored report with no final answer", reason: "completed", authored: true, want: agent.SubAgentCompleted},
		{name: "cancelled dominates missing deliverable", cancelled: true, want: agent.SubAgentCancelled},
		{name: "quoted quota text does not invent terminal cause", messages: []provider.ChatMessage{provider.NewTextMessage("tool", "Error: 429 quota exceeded")}, want: agent.SubAgentFailedPolicy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{}
			runner := NewSubAgentRunner(s, 1)
			t.Cleanup(runner.SessionCancel)
			ctx := context.Background()
			if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: "child", Name: "child"}, Agent: &agent.Agent{Ctx: ctx}, StartedAt: time.Now()}
			handle.SetTermReason(tc.reason)
			if tc.errors {
				handle.AddError("provider stream failed")
			}
			if tc.authored {
				handle.SetReportArtifact("report-1", 1)
			}
			messages := append([]provider.ChatMessage{provider.NewTextMessage("system", "instructions"), provider.NewTextMessage("user", "assignment")}, tc.messages...)
			result := runner.buildSubAgentResult(handle, messages)
			if result.Status != tc.want {
				t.Fatalf("status = %q, want %q (reason %q, report %v)", result.Status, tc.want, tc.reason, tc.authored)
			}
			if !tc.authored && handle.ReportDone() {
				t.Fatal("diagnostic report existed before completion")
			}
		})
	}
}
