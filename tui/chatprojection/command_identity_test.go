package chatprojection

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/types"
	"crude/types/command"
	"crude/types/toolresult"
)

func commandIdentityFixture() (types.ChatMessage, types.ChatMessage) {
	start := types.ChatMessage{InternalID: "start", Role: types.RoleTool, ToolName: "async_execute_command",
		ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone,
		ToolOutcome: toolresult.Outcome{State: toolresult.Succeeded, ID: "process:41:1000000000"},
		Content:     `{"execution_id":"process:41:1000000000","command":"build","pid":41,"output_descriptor":{"handle":"reused-name","status":"running","total_bytes":0},"output":""}`}
	settled := types.ChatMessage{InternalID: "settled", Role: types.RoleUser,
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
			wire, _ := json.Marshal([]types.ChatMessage{start, settled})
			var restored []types.ChatMessage
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
			if len(live.sync.Messages) != 2 || live.sync.Messages[0].Role != types.RoleTool {
				t.Fatal("presentation changed the canonical audit")
			}
			// Removing the origin must make its remaining evidence readable again.
			live.SyncMessages([]types.ChatMessage{settled}, 3, 0)
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
	r.SyncMessages([]types.ChatMessage{start, settled}, 1, 0)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 2 {
		t.Fatal("a reused handle merged different process lifetimes")
	}
	failure := types.ChatMessage{InternalID: "a", Role: types.RoleTool, ToolName: "edit_file",
		ToolStatus: types.ToolStatusError, Content: "Error: same reason"}
	other := failure
	other.InternalID = "b"
	r.SyncMessages([]types.ChatMessage{failure, other}, 2, 0)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 2 || rows[0].ID == rows[1].ID {
		t.Fatal("matching text hid independent failures")
	}
}

func BenchmarkDenseCommandSettlementPresentedFrame(b *testing.B) {
	r := newActivityRenderer()
	messages := make([]types.ChatMessage, 0, 4001)
	for i := range 2000 {
		start, settled := commandIdentityFixture()
		start.InternalID, settled.InternalID = fmt.Sprintf("start-%d", i), fmt.Sprintf("settled-%d", i)
		start.ToolOutcome.ID = fmt.Sprintf("process:41:%d", int64(i+1)*1e9)
		settled.Collaboration.Command.StartedAt = time.Unix(int64(i+1), 0)
		messages = append(messages, start, settled)
	}
	messages = append(messages, types.ChatMessage{InternalID: "active", Role: types.RoleTool,
		ToolName: "exec_command", ToolCommand: "test", ToolStatus: types.ToolStatusCalling})
	r.SyncMessages(messages, 1, 0)
	r.ExportMessageRows(100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.SetSpinFrame(i * 400)
		r.ExportMessageRows(100)
	}
}
