package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
)

// Exercise the terminal transcript/result boundary without treating an earlier
// tool call as proof of completion.
func TestSubAgentDiagnosticReportDoesNotBecomeAuthored(t *testing.T) {
	home := t.TempDir()
	s, err := NewWithSessionDir("parent", testConfig(), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	runner := NewSubAgentRunner(s, 1)
	t.Cleanup(runner.SessionCancel)
	handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: "failed-child", Name: "failed child", Description: "read source"}, Agent: &agent.Agent{Ctx: context.Background()}, StartedAt: time.Now()}
	handle.SetTermReason("error")
	handle.AddError("terminal provider stream failure")
	messages := []provider.ChatMessage{
		provider.NewTextMessage("system", "instructions"), provider.NewTextMessage("user", "read source"),
		{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
		provider.NewTextMessage("tool", "read completed"),
	}
	result := runner.buildResultFromMessages(handle, messages)
	runner.autoGenerateReport(handle, messages, &result)
	if result.Status != agent.SubAgentFailedError || result.ResultPath == "" || handle.ReportDone() {
		t.Fatalf("diagnostic changed terminal completion semantics: result=%+v reportDone=%v", result, handle.ReportDone())
	}
	content, err := os.ReadFile(result.ResultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Status: failed_error") {
		t.Fatalf("diagnostic report lost failure status: %s", content)
	}
}

func TestSubAgentTerminalResultNeedsDeliverable(t *testing.T) {
	cases := []struct {
		name, reason string
		messages     []provider.ChatMessage
		authored     bool
		errors       bool
		cancelled    bool
		want         agent.SubAgentStatus
	}{
		{name: "only assignment", want: agent.SubAgentFailedError},
		{name: "tool calls then terminal stream error", reason: "error", errors: true,
			messages: []provider.ChatMessage{
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
				provider.NewTextMessage("tool", "file contents"),
				provider.NewTextMessage("assistant", "partial stream output"),
			}, want: agent.SubAgentFailedError},
		{name: "no report and no final answer despite tool success", messages: []provider.ChatMessage{
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
			provider.NewTextMessage("tool", "file contents"),
		}, want: agent.SubAgentFailedError},
		{name: "earlier answer does not cover later dangling call", messages: []provider.ChatMessage{
			provider.NewTextMessage("assistant", "earlier progress"),
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read-1", Function: provider.FunctionCall{Name: "read_file"}}}},
		}, want: agent.SubAgentFailedError},
		{name: "recovered error with authored report", reason: "completed", errors: true, authored: true, want: agent.SubAgentCompleted},
		{name: "recovered error with final answer", reason: "completed", errors: true,
			messages: []provider.ChatMessage{provider.NewTextMessage("assistant", "Final findings and outcome.")}, want: agent.SubAgentCompleted},
		{name: "final answer without report", reason: "completed",
			messages: []provider.ChatMessage{provider.NewTextMessage("assistant", "Final findings and outcome.")}, want: agent.SubAgentCompleted},
		{name: "authored report with no final answer", reason: "completed", authored: true, want: agent.SubAgentCompleted},
		{name: "cancelled dominates missing deliverable", cancelled: true, want: agent.SubAgentCancelled},
		{name: "quota dominates missing deliverable", messages: []provider.ChatMessage{provider.NewTextMessage("tool", "Error: 429 quota exceeded")}, want: agent.SubAgentFailedQuota},
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
				handle.SetReportPath(filepath.Join(t.TempDir(), "authored.md"))
			}
			messages := append([]provider.ChatMessage{provider.NewTextMessage("system", "instructions"), provider.NewTextMessage("user", "assignment")}, tc.messages...)
			result := runner.buildResultFromMessages(handle, messages)
			if result.Status != tc.want {
				t.Fatalf("status = %q, want %q (reason %q, report %v)", result.Status, tc.want, tc.reason, tc.authored)
			}
			if !tc.authored && handle.ReportDone() {
				t.Fatal("diagnostic report existed before completion")
			}
		})
	}
}
