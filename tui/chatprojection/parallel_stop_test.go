package chatprojection

import (
	"strings"
	"testing"

	"crude/types"
)

func TestStoppedParallelBatchPreservesPartialResultsAndSettlesEveryChild(t *testing.T) {
	input := `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"a.go"}},{"recipient_name":"functions.grep_files","parameters":{"pattern":"search"}},{"recipient_name":"functions.execute_command","parameters":{"command":"build"}},{"recipient_name":"functions.read_file","parameters":{"path":"b.go"}}]}`
	result := `{"results":[{"recipient_name":"read_file","result":"actual contents","error":false},{"recipient_name":"grep_files","result":"actual error","error":true},{"recipient_name":"execute_command","phase":"running","result":"partial build output"}]}`
	for _, parentStatus := range []string{types.ToolStatusCancelled, "interrupted", types.ToolStatusSuccess} {
		t.Run(parentStatus, func(t *testing.T) {
			r := newActivityRenderer()
			r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: input, Content: result, ToolStatus: "calling", ToolPreviewPhase: types.ToolPreviewStreaming}}, 1)
			if child := r.activityMessages["batch:2"]; child == nil || !activityRunning(child) {
				t.Fatalf("live batch not running: %+v", child)
			}
			r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: input, Content: result, ToolStatus: parentStatus, ToolPreviewPhase: types.ToolPreviewDone}}, 2)
			want := []struct{ status, content string }{{"success", "actual contents"}, {"error", "actual error"}, {"cancelled", "Stopped before a result was returned\npartial build output"}, {"cancelled", "No result returned"}}
			for i, expected := range want {
				child := r.activityMessages["batch:"+string(rune('0'+i))]
				if child == nil || child.ToolStatus != expected.status || !strings.Contains(child.Content, expected.content) || activityRunning(child) || toolRowFrameSensitive(*child) {
					t.Errorf("child %d = %+v; want %+v", i, child, expected)
				}
			}
			r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: input, Content: result, ToolStatus: parentStatus, ToolPreviewPhase: types.ToolPreviewDone}}, 3)
			for _, item := range r.activityMessages {
				if activityRunning(item) {
					t.Errorf("resync revived running child: %+v", item)
				}
			}
			// Session hydration starts from terminal canonical snapshots rather
			// than replaying the live transition through the renderer.
			hydrated := newActivityRenderer()
			hydrated.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: input, Content: result, ToolStatus: parentStatus, ToolPreviewPhase: types.ToolPreviewDone}}, 1)
			for i := range want {
				child := hydrated.activityMessages["batch:"+string(rune('0'+i))]
				if child == nil || activityRunning(child) || toolRowFrameSensitive(*child) {
					t.Errorf("hydration animated child %d: %+v", i, child)
				}
			}
		})
	}
}
