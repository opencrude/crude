package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"crude/codexio"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/tools"
)

func TestGPTProfilesForEveryRole(t *testing.T) {
	for _, role := range []string{"", CatResearcher, CatCrafter, CatAeron, "custom"} {
		for _, model := range []string{"gpt-5.5", "gateway/GPT-5.5", "claude-test"} {
			t.Run(role+"/"+model, func(t *testing.T) {
				defs := FilterToolsForModel(tools.AgentTools, nil, nil, role, role != "", model)
				gpt := codexio.IsGPT(model)
				if role != CatResearcher && gpt && findToolDef("apply_patch", defs) == nil {
					t.Fatal("patch missing")
				}
				if gpt && (findToolDef("write_file", defs) != nil || findToolDef("edit_file", defs) != nil) {
					t.Fatal("old interface leaked")
				}
				if !gpt && findToolDef("apply_patch", defs) != nil {
					t.Fatal("non-GPT tools changed")
				}
				var prompt string
				if role == "" {
					prompt = rootprompt.BuildRoot(rootprompt.RootOptions{MultiAgent: true, WorkDir: t.TempDir(), Model: model})
				} else {
					prompt, _ = rootprompt.BuildChild(rootprompt.ChildOptions{Model: model, Category: role, WorkDir: t.TempDir()})
				}
				if !strings.Contains(prompt, `<crude_`) || !strings.Contains(prompt, rootprompt.ProjectGuidanceContract) || !strings.Contains(prompt, rootprompt.ToolContract) {
					t.Fatal("model-specific tools replaced canonical Crude policy")
				}
			})
		}
	}
}

func TestPatchMovesNotRestrictedByPrimitiveTools(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	a.Yolo = true
	a.Policy = safety.DefaultPolicy()
	a.FilteredTools = tools.ForModel(FilterTools(tools.AgentTools, []string{"edit_file"}, []string{"move_file", "delete_file"}, "", false), a.Model)
	called := false
	a.ToolFn = func(_ context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		called = true
		if name != "apply_patch" || params["input"] == "" {
			t.Fatal(name, params)
		}
		return tools.ToolResult{Content: "Success."}, nil
	}
	for _, input := range []string{
		"*** Begin Patch\n*** Update File: old\n*** Move to: new\n-a\n+b\n*** End Patch",
		"*** Begin Patch\n*** Delete File: old\n*** End Patch",
	} {
		called = false
		last := ""
		r := a.executeOneToolCall(t.Context(), provider.ToolCall{Type: "custom", Function: provider.FunctionCall{Name: "apply_patch", Arguments: input}}, func(AgentEvent) {}, nil, &last)
		if r.toolError || !called {
			t.Fatalf("restricted native patch: %s", r.result.Content)
		}
	}
}

func TestGPTNativePatchRealAgentLoop(t *testing.T) {
	const patch = "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		found := false
		for _, value := range req["tools"].([]any) {
			tool := value.(map[string]any)
			if tool["name"] == "apply_patch" {
				found = tool["type"] == "custom"
			}
			if tool["name"] == "edit_file" || tool["name"] == "write_file" {
				t.Error("old editor tool in GPT request")
			}
		}
		if !found {
			t.Error("native custom tool missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"custom_tool_call\",\"id\":\"i1\",\"call_id\":\"p1\",\"name\":\"apply_patch\",\"input\":\"\",\"status\":\"in_progress\"}}\n\ndata: {\"type\":\"response.custom_tool_call_input.delta\",\"output_index\":0,\"delta\":%q}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"custom_tool_call\",\"id\":\"i1\",\"call_id\":\"p1\",\"name\":\"apply_patch\",\"input\":%q,\"status\":\"completed\"}]}}\n\n", patch, patch)
		} else {
			foundOutput := false
			for _, value := range req["input"].([]any) {
				item := value.(map[string]any)
				if item["type"] == "custom_tool_call_output" && item["call_id"] == "p1" {
					foundOutput = true
				}
			}
			if !foundOutput {
				t.Error("custom output lost on second request")
			}
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
		}
	}))
	defer server.Close()
	p := provider.FromPreset("openai", "key", server.URL)
	p.Model = "gpt-5.5"
	a := newTestAgent(t, p, p.Model)
	a.Yolo = true
	a.Policy = safety.DefaultPolicy()
	calls := 0
	a.ToolFn = func(_ context.Context, name string, args map[string]any) (tools.ToolResult, error) {
		calls++
		if name != "apply_patch" || args["input"] != patch {
			t.Fatal(name, args)
		}
		return tools.ToolResult{Content: "Success."}, nil
	}
	history := a.Run(t.Context(), nil, rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: t.TempDir(), Model: p.Model}), func(AgentEvent) {}, nil, "", nil)
	if requests != 2 || calls != 1 || history[len(history)-1].ContentString() != "done" {
		t.Fatalf("requests %d calls %d history %v", requests, calls, history)
	}
}

func TestGPTPatchThroughChatCompletionsGateway(t *testing.T) {
	const patch = "*** Begin Patch\n*** Add File: hello.txt\n+hello \"gateway\"\n*** End Patch"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct {
			Tools []struct {
				Type     string `json:"type"`
				Custom   any    `json:"custom"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				ToolCalls []struct {
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		found := false
		for _, tool := range req.Tools {
			if tool.Custom != nil || tool.Type != "function" {
				t.Errorf("gateway rejected unsupported tool: %+v", tool)
			}
			if tool.Function.Name == "apply_patch" {
				found = true
			}
		}
		if !found {
			t.Error("patch tool not offered to gateway")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			args, _ := json.Marshal(map[string]string{"input": patch})
			cut := strings.Index(string(args), "+hello")
			if cut < 0 {
				t.Fatal("patch arguments have no split point")
			}
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"p1\",\"type\":\"function\",\"function\":{\"name\":\"apply_patch\",\"arguments\":%q}}]}}]}\n\n", string(args[:cut]))
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(args[cut:]))
		} else {
			if len(req.Messages) < 2 || len(req.Messages[len(req.Messages)-2].ToolCalls) != 1 {
				t.Errorf("missing patch call replay: %+v", req.Messages)
			} else {
				call := req.Messages[len(req.Messages)-2].ToolCalls[0]
				var args map[string]string
				if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || call.Type != "function" || call.Function.Name != "apply_patch" || args["input"] != patch {
					t.Errorf("patch replay rejected by gateway: %+v (%v)", call, err)
				}
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "router/gpt-5.5"
	a := newTestAgent(t, p, p.Model)
	a.Yolo = true
	a.Policy = safety.DefaultPolicy()
	calls := 0
	a.ToolFn = func(_ context.Context, name string, args map[string]any) (tools.ToolResult, error) {
		calls++
		if name != "apply_patch" || args["input"] != patch {
			t.Errorf("patch execution lost freeform input: %s %v", name, args)
		}
		return tools.ToolResult{Content: "Success."}, nil
	}
	streamedPatchPath := false
	history := a.Run(t.Context(), nil, rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: t.TempDir(), Model: p.Model}), func(event AgentEvent) {
		if event.Type == "tool_call_streaming" {
			data, _ := event.Data.(map[string]any)
			if data["path"] == "hello.txt" {
				streamedPatchPath = true
			}
		}
	}, nil, "", nil)
	if requests != 2 || calls != 1 || history[len(history)-1].ContentString() != "done" {
		t.Fatalf("requests %d calls %d history %v", requests, calls, history)
	}
	if !streamedPatchPath {
		t.Fatal("chunked function patch did not update the agent view")
	}
	for _, msg := range history {
		for _, call := range msg.ToolCalls {
			if call.Function.Name == "apply_patch" && (call.Type != "custom" || call.Function.Arguments != patch) {
				t.Fatalf("patch history not canonical: %+v", call)
			}
		}
	}
}

func TestChatPatchPreviewUnwrapsFunctionInput(t *testing.T) {
	const patch = "*** Begin Patch\n*** Add File: a.txt\n+quoted \"text\"\n*** End Patch"
	args, err := json.Marshal(map[string]string{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	preview := ExtractPreview("p1", "apply_patch", string(args), nil)
	if !preview.Complete || preview.Params["input"] != patch || preview.Params["path"] != "a.txt" {
		t.Fatalf("function patch preview = %+v", preview)
	}
	_, _, added := EstimateStreamingLines("apply_patch", string(args))
	if added != 1 {
		t.Fatalf("function patch added lines = %d", added)
	}
	incomplete := ExtractPreview("p1", "apply_patch", string(args[:len(args)-1]), nil)
	if incomplete.Complete {
		t.Fatalf("unfinished JSON wrapper accepted as complete: %+v", incomplete)
	}
}

func TestParallelBatchSurvivesOpenAIProtocolSwitches(t *testing.T) {
	const args = `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"a.go"}},{"recipient_name":"functions.read_file","parameters":{"path":"b.go"}}]}`
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var wire struct {
			Tools []struct {
				Type, Name string
				Function   struct{ Name string }
			} `json:"tools"`
			Input    []struct{ Type, Name, Namespace, CallID, Arguments string } `json:"input"`
			Messages []struct {
				ToolCalls []struct {
					Function provider.FunctionCall `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
			return
		}
		found := false
		for _, tool := range wire.Tools {
			if tool.Type == "namespace" || tool.Name == "multi_tool_use.parallel" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"error":{"message":"multi_tool_use is a reserved namespace"}}`)
				return
			}
			found = found || tool.Name == "multi_tool_use_parallel" || tool.Function.Name == "multi_tool_use_parallel"
		}
		if !found {
			t.Error("parallel execution tool missing from request")
		}
		for _, item := range wire.Input {
			if item.Type == "function_call" && (item.Name != "multi_tool_use_parallel" || item.Namespace != "" || item.Arguments != args) {
				t.Errorf("invalid Responses batch replay: %+v", item)
			}
		}
		for _, message := range wire.Messages {
			for _, call := range message.ToolCalls {
				if call.Function.Name != "multi_tool_use_parallel" || call.Function.Arguments != args {
					t.Errorf("invalid Chat batch replay: %+v", call)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			if requests%2 == 1 {
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"name\":\"multi_tool_use_parallel\",\"call_id\":\"p%d\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\ndata: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"name\":\"multi_tool_use_parallel\",\"arguments\":%q}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"name\":\"multi_tool_use_parallel\",\"call_id\":\"p%d\",\"arguments\":%q,\"status\":\"completed\"}]}}\n\n", requests, args, requests, args)
			} else {
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
			}
		} else if requests%2 == 1 {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"p%d\",\"type\":\"function\",\"function\":{\"name\":\"multi_tool_use_parallel\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", requests, args)
		} else {
			writeChatSSE(w, "done", "stop")
		}
	}))
	defer server.Close()
	var executed atomic.Int32
	var history []provider.ChatMessage
	for _, protocol := range []string{"openai", "openai-responses", "codex", "openai"} {
		p := provider.FromPreset("openai-chat", "key", server.URL)
		if err := p.SetProtocolID(protocol); err != nil {
			t.Fatal(err)
		}
		p.Model = "gpt-test"
		a := newTestAgent(t, p, p.Model)
		a.FilteredTools = FilterToolsForModel(tools.AgentTools, []string{"read_file"}, nil, "", false, p.Model)
		a.ToolFn = func(_ context.Context, name string, params map[string]any) (tools.ToolResult, error) {
			executed.Add(1)
			if name != "read_file" {
				t.Errorf("wrong execution identity: %s", name)
			}
			return tools.ToolResult{Content: getStr(params, "path")}, nil
		}
		before, err := json.Marshal(history)
		if err != nil {
			t.Fatal(err)
		}
		next := a.Run(t.Context(), history, rootprompt.BuildRoot(rootprompt.RootOptions{}), nil, nil, "", nil)
		after, err := json.Marshal(history)
		if err != nil || string(before) != string(after) {
			t.Fatal("protocol switch rewrote canonical history")
		}
		if next[len(next)-1].ContentString() != "done" {
			t.Fatalf("%s request failed: %+v", protocol, next)
		}
		history = next[1:]
		for _, message := range history {
			for _, call := range message.ToolCalls {
				if call.Function.Name != "multi_tool_use.parallel" || call.Function.Arguments != args {
					t.Fatal("wire name leaked into persisted execution identity")
				}
			}
		}
	}
	if requests != 8 || executed.Load() != 8 {
		t.Fatalf("requests=%d executions=%d", requests, executed.Load())
	}
}
