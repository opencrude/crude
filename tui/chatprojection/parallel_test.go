package chatprojection

import (
	"encoding/json"
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

func TestParallelAgentHandlesStaySemanticThroughPreviewAndResult(t *testing.T) {
	r := newActivityRenderer()
	input := `{"tool_uses":[{"recipient_name":"poll_sub_agent","parameters":{"handle_id":"agent-8f21"}},{"recipient_name":"cancel_sub_agent","parameters":{"handle_id":"agent-39bf"}}]}`
	message := types.ChatMessage{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel",
		ToolInput: input, ToolStatus: types.ToolStatusStreaming, ToolPreviewPhase: types.ToolPreviewPreparing}
	for i, body := range []string{"", `{"results":[{"recipient_name":"poll_sub_agent","phase":"running"},null]}`,
		`{"results":[{"recipient_name":"poll_sub_agent","result":"done"},{"recipient_name":"cancel_sub_agent","result":"cancelled"}]}`} {
		message.Content = body
		if i == 2 {
			message.ToolStatus, message.ToolPreviewPhase = types.ToolStatusSuccess, types.ToolPreviewDone
		}
		r.SyncMessages([]types.ChatMessage{message}, uint64(i+1))
		rows, _, _ := r.ExportMessageRows(100)
		if len(rows) != 2 {
			t.Fatalf("preview %d: child rows=%+v", i, rows)
		}
		for j, want := range []string{"agent-8f21", "agent-39bf"} {
			text := strings.Join(rows[j].Lines, "\n")
			if !strings.Contains(text, want) || strings.Contains(text, `"handle_id"`) || strings.Contains(text, "{\\") {
				t.Fatalf("preview %d child %d leaked arguments: %q", i, j, text)
			}
		}
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

func TestParallelUnresolvedChildrenDoNotSplitOrRenumberKnownLeaves(t *testing.T) {
	r := newActivityRenderer()
	msg := types.ChatMessage{InternalID: "batch", ToolID: "call", Role: types.RoleTool,
		ToolName: "multi_tool_use.parallel", ToolStatus: types.ToolStatusStreaming,
		ToolInput: `{"tool_uses":[{}, {"recipient_name":"functions.read_file","parameters":{"path":"a.go"}}, {"parameters":{"path":"b.go"}}]}`}
	r.SyncMessages([]types.ChatMessage{msg}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ID != "activity:batch:1" || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "a.go") {
		t.Fatalf("unresolved siblings changed placement or source indices: %+v", rows)
	}
	if r.activityMessages["batch:0"] != nil || r.activityMessages["batch:2"] != nil {
		t.Fatal("unresolved siblings became display nodes")
	}
	msg.ToolInput = `{"tool_uses":[{}, {"recipient_name":"functions.read_file","parameters":{"path":"a.go"}}, {"recipient_name":"write_file","parameters":{"path":"b.go","content":"line one\nline two"}}, {"recipient_name":"read_file_extra","parameters":{}}]}`
	r.SyncMessages([]types.ChatMessage{msg}, 2)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 3 || rows[0].ID != "activity:batch:1" || rows[1].ID != "batch:2" || rows[2].ID != "batch:3" {
		t.Fatalf("mutation/unknown tool did not start in its standalone slot: %+v", rows)
	}
	if !strings.Contains(strings.Join(rows[1].Lines, "\n"), "b.go") || r.disclosureState(r.activityMessages["batch:2"]) != DisclosureExpanded {
		t.Fatal("preparation gating suppressed the mutation preview or changed its disclosure")
	}
}

func TestParallelEmptyEnvelopeHidesOnlyLivePreparation(t *testing.T) {
	for _, tc := range []struct {
		status, phase, content, want string
	}{
		{types.ToolStatusStreaming, types.ToolPreviewPreparing, "", ""},
		{types.ToolStatusStreaming, types.ToolPreviewStreaming, "", ""},
		{types.ToolStatusError, types.ToolPreviewError, "Error: malformed tool call", "malformed tool call"},
		{types.ToolStatusCancelled, types.ToolPreviewDone, "", "Stopped"},
		{"interrupted", types.ToolPreviewDone, "", "Not run"},
	} {
		t.Run(tc.status+"/"+tc.phase, func(t *testing.T) {
			r := newActivityRenderer()
			r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool,
				ToolName: "multi_tool_use.parallel", ToolStatus: tc.status, ToolPreviewPhase: tc.phase,
				ToolInput: `{"tool_uses":[{}]}`, Content: tc.content}}, 1)
			rows, _, _ := r.ExportMessageRows(100)
			if tc.want == "" {
				if len(rows) != 0 {
					t.Fatalf("empty preparation became a transcript row: %+v", rows)
				}
			} else if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), tc.want) {
				t.Fatalf("settled outcome disappeared with the empty preview: %+v", rows)
			}
		})
	}
}

func TestParallelSubAgentFailureUsesChildOutcome(t *testing.T) {
	input := `{"tool_uses":[{"recipient_name":"run_sub_agent","parameters":{"category":"researcher","description":"Inspect renderer and report","wait":true}}]}`
	result := "## Sub-Agent Result: Lily sub Dawn [researcher]\n**Agent:** child-1\n**Status:** TIMEOUT\n**Report:** report.md"
	body, err := json.Marshal(map[string]any{"results": []map[string]any{{"recipient_name": "run_sub_agent", "result": result, "error": false}}})
	if err != nil {
		t.Fatal(err)
	}
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: "tool", ToolName: "multi_tool_use.parallel", ToolInput: input, Content: string(body), ToolStatus: "success", ToolPreviewPhase: "done"}}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 {
		t.Fatalf("parallel child rows: %+v", rows)
	}
	text := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(text, "Sub-agent timed out Lily sub Dawn · Researcher") || strings.Contains(text, "Sub-agent finished") || strings.Contains(text, `"category"`) || strings.Contains(text, "**Status:**") {
		t.Fatalf("parallel child outcome rendered incorrectly: %q", text)
	}
	if !strings.Contains(text, "× ") || activityEligible(r.activityMessages["batch:0"]) {
		t.Fatalf("parallel child failure was grouped as successful work: %q", text)
	}
}
