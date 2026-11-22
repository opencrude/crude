package chatprojection

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/types/command"
	"crude/types/conversation"
	"crude/types/toolresult"
)

func commandIdentityFixture() (conversation.ChatMessage, conversation.ChatMessage) {
	start := conversation.ChatMessage{InternalID: "start", Role: conversation.RoleTool, ToolName: "async_execute_command",
		ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone,
		ToolOutcome: toolresult.Outcome{State: toolresult.Succeeded, ID: "process:41:1000000000"},
		Content:     `{"execution_id":"process:41:1000000000","command":"build","pid":41,"output_descriptor":{"handle":"reused-name","status":"running","total_bytes":0},"output":""}`}
	settled := conversation.ChatMessage{InternalID: "settled", Role: conversation.RoleUser,
		Collaboration: &provider.CollaborationAttribution{Kind: "command_outcome", EventID: "settlement",
			Command: &command.Outcome{Handle: "reused-name", Command: "build", PID: 41, StartedAt: time.Unix(1, 0),
				Status: "failed", ExitCode: 7, ExitKnown: true, Error: "compiler failed", OutputBytes: 20,
				Output: &command.Result{OutputDescriptor: command.OutputDescriptor{Status: command.Failed, TotalBytes: 20}, Output: "retained compiler evidence"}}}}
	return start, settled
}

func TestCommandSettlementUpdatesOnlyItsExactOriginAndKeepsBothResults(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprint(parallel), func(t *testing.T) {
			start, settled := commandIdentityFixture()
			originID := start.InternalID
			if parallel {
				body, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"recipient_name": start.ToolName, "result": start.Content, "outcome": start.ToolOutcome}}})
				start.ToolName, start.ToolInput, start.Content = "multi_tool_use.parallel", `{"tool_uses":[{"recipient_name":"async_execute_command","parameters":{"command":"build","handle":"reused-name"}}]}`, string(body)
				start.ToolOutcome = toolresult.Outcome{}
				originID += ":0"
			}
			live := newActivityRenderer()
			live.AppendMessage(start)
			live.ExportMessageRows(100) // materialize before the late result
			live.AppendMessage(settled)
			wire, _ := json.Marshal([]conversation.ChatMessage{start, settled})
			var restored []conversation.ChatMessage
			if err := json.Unmarshal(wire, &restored); err != nil {
				t.Fatal(err)
			}
			cold := newActivityRenderer()
			cold.SyncMessages(restored, 1, 0)
			for _, expanded := range []bool{false, true} {
				if expanded {
					live.ToggleDisclosure(originID)
					cold.ToggleDisclosure(originID)
				}
				left, _, _ := live.ExportMessageRows(100)
				right, _, _ := cold.ExportMessageRows(100)
				if len(left) != 1 || len(right) != 1 || left[0].ID != originID {
					t.Fatalf("late result duplicated or changed after resume: live=%+v cold=%+v", left, right)
				}
				// The live renderer has painted an extra source revision. Its
				// cache sequence is not presentation; all text, ink and hits are.
				lrow, rrow := left[0], right[0]
				lrow.Version, rrow.Version = 0, 0
				if !reflect.DeepEqual(lrow, rrow) {
					t.Fatalf("late result presentation changed after resume:\nlive=%+v\ncold=%+v", lrow, rrow)
				}
				text := strings.Join(left[0].Lines, "\n")
				if !strings.Contains(text, "Command failed") {
					t.Fatalf("late failure still claims the process is running: %s", text)
				}
				if expanded && (!strings.Contains(text, "Initial result:") || !strings.Contains(text, "running") || !strings.Contains(text, "retained compiler evidence") || !strings.Contains(text, "PID: 41")) {
					t.Fatalf("joining a result lost audit evidence: %s", text)
				}
			}
			if len(live.sync.Messages) != 2 || live.sync.Messages[0].Role != conversation.RoleTool {
				t.Fatal("presentation changed the canonical audit")
			}
			// Removing the origin must make its remaining evidence readable again.
			live.SyncMessages([]conversation.ChatMessage{settled}, 3, 0)
			rows, _, _ := live.ExportMessageRows(100)
			if len(rows) != 1 || rows[0].ID != "settled" {
				t.Fatal("origin removal hid the standalone settlement")
			}
		})
	}
}

func TestRepeatedHandlesAndIdenticalErrorTextNeverMergeIndependentOperations(t *testing.T) {
	start, settled := commandIdentityFixture()
	settled.Collaboration.Command.StartedAt = time.Unix(2, 0)
	r := newActivityRenderer()
	r.SyncMessages([]conversation.ChatMessage{start, settled}, 1, 0)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 2 {
		t.Fatal("a reused handle merged different process lifetimes")
	}
	failure := conversation.ChatMessage{InternalID: "a", Role: conversation.RoleTool, ToolName: "edit_file",
		ToolStatus: conversation.ToolStatusError, Content: "Error: same reason"}
	other := failure
	other.InternalID = "b"
	r.SyncMessages([]conversation.ChatMessage{failure, other}, 2, 0)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 2 || rows[0].ID == rows[1].ID {
		t.Fatal("matching text hid independent failures")
	}
}

func BenchmarkDenseCommandSettlementPresentedFrame(b *testing.B) {
	r := newActivityRenderer()
	messages := make([]conversation.ChatMessage, 0, 4001)
	for i := range 2000 {
		start, settled := commandIdentityFixture()
		start.InternalID, settled.InternalID = fmt.Sprintf("start-%d", i), fmt.Sprintf("settled-%d", i)
		start.ToolOutcome.ID = fmt.Sprintf("process:41:%d", int64(i+1)*1e9)
		settled.Collaboration.Command.StartedAt = time.Unix(int64(i+1), 0)
		messages = append(messages, start, settled)
	}
	messages = append(messages, conversation.ChatMessage{InternalID: "active", Role: conversation.RoleTool,
		ToolName: "exec_command", ToolCommand: "test", ToolStatus: conversation.ToolStatusCalling})
	r.SyncMessages(messages, 1, 0)
	r.ExportMessageRows(100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.SetSpinFrame(i * 400)
		r.ExportMessageRows(100)
	}
}
