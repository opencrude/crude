package turnloop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent/codexio"
	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/safety"
	"crude/agent/tools"
	"crude/internal/providersim"
)

func TestGPTProfilesForEveryRole(t *testing.T) {
	for _, role := range []string{"", CatResearcher, CatCrafter, CatErrand, "custom"} {
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
	server := providersim.New(t)
	cut := strings.Index(patch, "+hello")
	server.Script(providersim.Turn{}.WithToolArgumentDeltas("p1", "apply_patch", patch[:cut], patch[cut:]), providersim.Text("done"))
	p := provider.FromPreset("openai", "key", server.BaseURL(providersim.Responses))
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
	requests := server.Requests()
	if len(requests) != 2 || calls != 1 || history[len(history)-1].ContentString() != "done" {
		t.Fatalf("requests %d calls %d history %v", len(requests), calls, history)
	}
	for _, r := range requests {
		found := false
		for _, tool := range r.Tools {
			if tool.Name == "edit_file" || tool.Name == "write_file" {
				t.Error("old editor tool in GPT request")
			}
			if tool.Name == "apply_patch" {
				var decl struct{ Type string }
				if err := json.Unmarshal(tool.Raw, &decl); err != nil {
					t.Fatal(err)
				}
				found = decl.Type == "custom"
			}
		}
		if !found {
			t.Error("native custom tool missing")
		}
	}
	foundOutput := false
	for _, msg := range requests[1].Messages {
		for _, part := range msg.Parts {
			if part.Kind != providersim.PartToolResult || part.CallID != "p1" {
				continue
			}
			var item struct{ Type string }
			if err := json.Unmarshal(part.Raw, &item); err != nil {
				t.Fatal(err)
			}
			foundOutput = item.Type == "custom_tool_call_output"
		}
	}
	if !foundOutput {
		t.Error("custom output lost on second request")
	}

}

func TestGPTPatchThroughChatCompletionsGateway(t *testing.T) {
	const patch = "*** Begin Patch\n*** Add File: hello.txt\n+hello \"gateway\"\n*** End Patch"
	args, err := json.Marshal(map[string]string{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.Index(string(args), "+hello")
	if cut < 0 {
		t.Fatal("patch arguments have no split point")
	}
	server := providersim.New(t)
	server.Script(providersim.Turn{}.WithToolArgumentDeltas("p1", "apply_patch", string(args[:cut]), string(args[cut:])), providersim.Text("done"))
	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
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
	requests := server.Requests()
	if len(requests) != 2 || calls != 1 || history[len(history)-1].ContentString() != "done" {
		t.Fatalf("requests %d calls %d history %v", len(requests), calls, history)
	}
	for _, r := range requests {
		found := false
		for _, tool := range r.Tools {
			var decl struct {
				Type   string
				Custom json.RawMessage
			}
			if err := json.Unmarshal(tool.Raw, &decl); err != nil || decl.Type != "function" || decl.Custom != nil {
				t.Errorf("unsupported gateway declaration %s: %v", tool.Raw, err)
			}
			found = found || tool.Name == "apply_patch"
		}
		if !found {
			t.Error("patch tool not offered to gateway")
		}
	}
	// Inspect the function wrapper as received, not just the canonical tool history.
	var replay struct {
		Messages []struct {
			ToolCalls []struct {
				Type     string
				Function struct{ Name, Arguments string }
			} `json:"tool_calls"`
		}
	}
	if err := json.Unmarshal(requests[1].Raw, &replay); err != nil {
		t.Fatal(err)
	}
	if n := len(replay.Messages); n < 2 || len(replay.Messages[n-2].ToolCalls) != 1 {
		t.Fatal("missing patch call replay")
	} else {
		call := replay.Messages[n-2].ToolCalls[0]
		var wrap map[string]string
		if err := json.Unmarshal([]byte(call.Function.Arguments), &wrap); err != nil || call.Type != "function" || call.Function.Name != "apply_patch" || wrap["input"] != patch {
			t.Errorf("patch replay rejected by gateway: %+v (%v)", call, err)
		}
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
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		found := false
		for _, decl := range r.Tools {
			var wire struct{ Type string }
			if err := json.Unmarshal(decl.Raw, &wire); err != nil {
				t.Error(err)
			}
			if wire.Type == "namespace" || decl.Name == "multi_tool_use.parallel" {
				t.Error("multi_tool_use is a reserved namespace")
			}
			found = found || decl.Name == "multi_tool_use_parallel"
		}
		if !found {
			t.Error("parallel execution tool missing from request")
		}
		for _, m := range r.Messages {
			for _, p := range m.Parts {
				if p.Kind != providersim.PartToolCall {
					continue
				}
				if r.Wire == providersim.Chat {
					if p.Call.Name != "multi_tool_use_parallel" || p.Call.Arguments != args {
						t.Errorf("invalid Chat batch replay: %+v", p.Call)
					}
					continue
				}
				var item struct{ Namespace string }
				if err := json.Unmarshal(p.Raw, &item); err != nil {
					t.Error(err)
				}
				if item.Namespace != "" || p.Call.Name != "multi_tool_use_parallel" || p.Call.Arguments != args {
					t.Errorf("invalid Responses batch replay: %s", p.Raw)
				}
			}
		}
		if r.Index%2 == 1 {
			return providersim.CallTools(providersim.ToolCall{ID: fmt.Sprintf("p%d", r.Index), Name: "multi_tool_use_parallel", Arguments: args})
		}
		return providersim.Text("done")
	})
	var executed atomic.Int32
	var history []provider.ChatMessage
	for _, protocol := range []string{"openai", "openai-responses", "codex", "openai"} {
		p := provider.FromPreset("openai-chat", "key", server.BaseURL(supervisedWires[protocol].wire))
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
	requests := len(server.Requests())
	if requests != 8 || executed.Load() != 8 {
		t.Fatalf("requests=%d executions=%d", requests, executed.Load())
	}
}
