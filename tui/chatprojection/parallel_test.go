package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/types"
)

func TestParallelUsesSameTreeAsIndividualTools(t *testing.T) {
	input := `{"tool_uses":[{"recipient_name":"functions.execute_command","parameters":{"command":"git status --short"}},{"recipient_name":"functions.read_file","parameters":{"path":"a.go"}}]}`
	body := `{"results":[{"recipient_name":"execute_command","result":"first line\nlast line","error":false},{"recipient_name":"read_file","result":"package main","error":false}]}`
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: "tool", ToolName: "multi_tool_use.parallel", ToolInput: input, Content: body, ToolStatus: "success", ToolPreviewPhase: "done"}}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ID != "activity:batch:0" {
		t.Fatalf("batch bypasses activity tree: %+v", rows)
	}
	r.ToggleDisclosure(rows[0].ID)
	rows, _, _ = r.ExportMessageRows(100)
	text := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(text, "git status --short") || !strings.Contains(text, "Read a.go") || strings.Contains(text, `{"results"`) {
		t.Fatalf("batch operands lost: %s", text)
	}
	r.ToggleDisclosure("batch:0")
	rows, _, _ = r.ExportMessageRows(100)
	text = strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(text, "first line") || !strings.Contains(text, "last line") || strings.Contains(text, `\n`) {
		t.Fatalf("child output not decoded: %s", text)
	}
}

func TestParallelMutationAndFailureAreIndependent(t *testing.T) {
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: "tool", ToolName: "multi_tool_use.parallel", ToolInput: `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"a.go"}},{"recipient_name":"write_file","parameters":{"path":"b.go"}},{"recipient_name":"exec_command","parameters":{"cmd":"go test ./..."}}]}`, Content: `{"results":[{"recipient_name":"read_file","result":"ok","error":false},{"recipient_name":"write_file","result":"Written 1 line\n+new content","error":false},{"recipient_name":"exec_command","result":"{\"exit_code\":1,\"output\":\"FAIL\\ndetail\"}","error":true}]}`, ToolStatus: "error", ToolPreviewPhase: "error"}}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 3 || rows[1].ID != "batch:1" || r.disclosureState(r.activityMessages["batch:1"]) != DisclosureExpanded {
		t.Fatalf("mutation buried in batch: %+v", rows)
	}
	text := strings.Join(rows[2].Lines, "\n")
	if !strings.Contains(text, "exit 1") || !strings.Contains(text, "detail") || strings.Contains(text, `\n`) {
		t.Fatalf("failure lost readable output: %s", text)
	}
}

func TestParallelCancellationStopsIncompleteChild(t *testing.T) {
	msg := types.ChatMessage{InternalID: "batch", Role: "tool", ToolName: "multi_tool_use.parallel", ToolInput: `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"a.go"}}]}`, Content: `{"results":[{"recipient_name":"read_file","phase":"running"}]}`, ToolStatus: types.ToolStatusCancelled}
	children := parallelMessages(&msg)
	if len(children) != 1 || children[0].ToolStatus != types.ToolStatusCancelled || activityRunning(children[0]) || toolRowFrameSensitive(*children[0]) {
		t.Fatalf("stopped batch left an animated child: %+v", children)
	}
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{msg}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Stopped read file a.go") {
		t.Fatalf("stopped child painted as success: %+v", rows)
	}
}
