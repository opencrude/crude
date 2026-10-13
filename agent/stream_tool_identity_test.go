package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"crude/provider"
	"crude/tools"
)

func TestResponsesRepeatedToolSnapshotsExecuteOneCallPerIdentity(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint("custom=", custom), func(t *testing.T) {
			name, kind, field, input := "read_file", "function_call", "arguments", `{"path":"fixture.go"}`
			if custom {
				name, kind, field, input = "apply_patch", "custom_tool_call", "input", "*** Begin Patch\n*** Add File: fixture.go\n+package fixture\n*** End Patch"
			}
			item := map[string]any{"id": "item-one", "type": kind, "call_id": "call-one", "name": name, field: input, "status": "completed"}
			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			var requests, executions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if requests.Add(1) == 1 {
					// The event position includes an omitted reasoning item. The
					// terminal response repeats the exact identity at position 0.
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":%s}\n\n", encoded)
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":%s}\n\n", encoded)
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[%s]}}\n\n", encoded)
					return
				}
				var request struct {
					Input []struct {
						Type, CallID string
					} `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				results := 0
				for _, part := range request.Input {
					if part.Type == "function_call_output" || part.Type == "custom_tool_call_output" {
						results++
					}
				}
				if results != 1 {
					t.Errorf("next provider request contains %d results for one identity", results)
				}
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n")
			}))
			defer server.Close()
			p := provider.FromPreset("openai", "key", server.URL)
			a := newTestAgent(t, p, "gpt-test")
			a.Yolo = true
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				executions.Add(1)
				return tools.ToolResult{Content: "done"}, nil
			}
			messages := a.Run(t.Context(), nil, "system", func(AgentEvent) {}, nil, "", nil)
			calls, results := 0, 0
			for _, message := range messages {
				calls += len(message.ToolCalls)
				if message.Role == "tool" {
					results++
				}
			}
			if executions.Load() != 1 || calls != 1 || results != 1 {
				t.Fatalf("one wire identity became executions=%d calls=%d results=%d", executions.Load(), calls, results)
			}
		})
	}
}

func TestToolAccumulatorPreservesDistinctCallsWhenSnapshotsMove(t *testing.T) {
	a := &Agent{}
	calls := make(map[int]*toolCallAccumulator)
	starts := 0
	start := func(string, string, string) { starts++ }
	for _, delta := range []provider.ToolCallDelta{
		{Index: 1, ID: "first", Name: "read_file", Arguments: `{"path":"same.go"}`},
		{Index: 2, ID: "second", Name: "read_file", Arguments: `{"path":"same.go"}`},
		{Index: 0, ID: "first", Name: "read_file", Arguments: `{"path":"same.go"}`, ReplaceArguments: true},
		{Index: 1, ID: "second", Name: "read_file", Arguments: `{"path":"same.go"}`, ReplaceArguments: true},
	} {
		a.accumulateToolCall(calls, delta, start, nil)
	}
	final := finalizeToolCalls(calls)
	if len(final) != 2 || starts != 2 || final[0].ID != "first" || final[1].ID != "second" || final[0].InternalID == final[1].InternalID {
		t.Fatalf("identity/order changed: starts=%d calls=%+v", starts, final)
	}
}

func TestReplacementAgentsNeverReuseToolProjectionIdentity(t *testing.T) {
	seen := make(map[string]bool)
	for range 10 {
		a := &Agent{}
		calls := make(map[int]*toolCallAccumulator)
		a.accumulateToolCall(calls, provider.ToolCallDelta{Index: 0, ID: "provider-call", Name: "apply_patch"}, nil, nil)
		id := finalizeToolCalls(calls)[0].InternalID
		if id == "" || seen[id] {
			t.Fatalf("replacement Agent reused projection identity %q", id)
		}
		seen[id] = true
	}
}
