package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/types/conversation"
)

// denseCommandHistory is a resumed history large enough for the bulk path:
// successful and failed command results, running background starts and
// parallel batches, between user turns.
func denseCommandHistory(turns int) []conversation.ChatMessage {
	var messages []conversation.ChatMessage
	add := func(msg conversation.ChatMessage) {
		msg.InternalID = fmt.Sprintf("m%d", len(messages))
		msg.ToolID = msg.InternalID
		messages = append(messages, msg)
	}
	for turn := range turns {
		add(conversation.ChatMessage{Role: conversation.RoleUser, Content: fmt.Sprintf("turn %d", turn)})
		for call := range 6 {
			exit := 0
			if (turn+call)%5 == 0 {
				exit = 1
			}
			add(conversation.ChatMessage{Role: conversation.RoleTool, ToolName: "exec_command", ToolStatus: conversation.ToolStatusSuccess,
				ToolInput: fmt.Sprintf(`{"cmd":"go test ./pkg%d"}`, call),
				Content:   fmt.Sprintf(`{"exit_code":%d,"output_descriptor":{"status":"completed","total_bytes":12},"output":"%s"}`, exit, strings.Repeat("ok line ", 40))})
		}
		add(conversation.ChatMessage{Role: conversation.RoleTool, ToolName: "async_execute_command", ToolStatus: conversation.ToolStatusSuccess,
			ToolInput: `{"command":"go build ./...","handle":"build"}`,
			Content:   `{"output_descriptor":{"handle":"build","status":"running","total_bytes":0},"output":""}`})
		add(conversation.ChatMessage{Role: conversation.RoleTool, ToolName: "multi_tool_use.parallel", ToolStatus: conversation.ToolStatusSuccess,
			ToolInput: `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"a.go"}},{"recipient_name":"exec_command","parameters":{"cmd":"git status"}}]}`,
			Content:   `{"results":[{"recipient_name":"read_file","result":"package a","error":false},{"recipient_name":"exec_command","result":"{\"exit_code\":0,\"output\":\"clean\"}","error":false}]}`})
		add(conversation.ChatMessage{Role: conversation.RoleAssistant, Content: fmt.Sprintf("done %d", turn)})
	}
	return messages
}

func exportedRows(r *Renderer) string {
	rows, _, _ := r.ExportMessageRows(100)
	var out strings.Builder
	for _, row := range rows {
		out.WriteString(row.ID)
		out.WriteString(": ")
		out.WriteString(strings.Join(row.Lines, " / "))
		out.WriteString("\n")
	}
	return out.String()
}

// Opening a dense session derives tool eligibility and parallel batches for
// thousands of messages at once, on several cores. The transcript must group
// exactly as one built message by message, where every derivation runs on
// the caller's goroutine.
func TestBulkSyncGroupsLikeIncrementalSync(t *testing.T) {
	history := denseCommandHistory(40)
	if tools := len(history) * 8 / 10; tools < 4*deriveParallelMin {
		t.Fatalf("history too small for the bulk path: %d messages", len(history))
	}
	bulk := newActivityRenderer()
	bulk.MaterializeBudget = -1
	bulk.SyncMessages(history, 1, 0)

	incremental := newActivityRenderer()
	incremental.MaterializeBudget = -1
	for i := range history {
		incremental.SyncMessages(history[:i+1], uint64(i+1), 0)
	}
	got, want := exportedRows(bulk), exportedRows(incremental)
	if got != want {
		t.Fatalf("bulk sync grouped differently from message-by-message sync\nbulk:\n%s\nincremental:\n%s", got, want)
	}
	if !strings.Contains(got, "activity:") {
		t.Fatalf("the dense history formed no activity groups:\n%s", got)
	}
	for i := range history {
		msg := &history[i]
		if msg.Role != conversation.RoleTool || msg.ToolName == "multi_tool_use.parallel" {
			continue
		}
		if bulk.activityEligibleCached(bulk.sync.MsgByID[msg.InternalID]) != activityEligible(msg) {
			t.Fatalf("%s: bulk eligibility differs from a direct decision", msg.InternalID)
		}
	}
}
