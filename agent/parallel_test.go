package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
)

func TestNativeParallelUsesRealToolChecksAndOrderedResults(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	a.Yolo = true
	a.configureModelTools()
	started := make(chan string, 2)
	release := make(chan struct{})
	a.ToolFn = func(ctx context.Context, name string, args map[string]any) (tools.ToolResult, error) {
		started <- getStr(args, "path")
		select {
		case <-release:
		case <-ctx.Done():
			return tools.ToolResult{}, ctx.Err()
		}
		return tools.ToolResult{Content: getStr(args, "path")}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan execResult, 1)
	go func() {
		done <- a.executeOneToolCall(ctx, provider.ToolCall{ID: "batch", InternalID: "batch", Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"a"}},{"recipient_name":"functions.read_file","parameters":{"path":"b"}}]}`}}, func(AgentEvent) {}, nil, new(string))
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("independent reads did not start concurrently")
		}
	}
	close(release)
	r := <-done
	if r.toolError {
		t.Fatal(r.result.Content)
	}
	var output struct {
		Results []struct {
			Result string `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(r.result.Content), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Results) != 2 || output.Results[0].Result != "a" || output.Results[1].Result != "b" {
		t.Fatal(r.result.Content)
	}
	a.FilteredTools = FilterToolsForModel(tools.AgentTools, []string{"read_file"}, nil, "", false, a.Model)
	a.configureModelTools()
	r = a.executeOneToolCall(ctx, provider.ToolCall{Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: `{"tool_uses":[{"recipient_name":"apply_patch","parameters":{"input":"ignored"}}]}`}}, func(AgentEvent) {}, nil, new(string))
	if !r.toolError || !strings.Contains(r.result.Content, "not available") {
		t.Fatalf("permission bypass: %s", r.result.Content)
	}
}

func TestNativeParallelReportsChildFailure(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	a.configureModelTools()
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "Error: missing"}, fmt.Errorf("missing")
	}
	r := a.executeOneToolCall(t.Context(), provider.ToolCall{Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"missing"}}]}`}}, func(AgentEvent) {}, nil, new(string))
	if !r.toolError || !strings.Contains(r.result.Content, `"error":true`) {
		t.Fatal(r)
	}
}

func TestParallelProgressPublishesOrderedPartialResults(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	a.Yolo = true
	a.configureModelTools()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	release := make(chan struct{})
	started := make(chan struct{})
	a.ToolFn = func(ctx context.Context, _ string, args map[string]any) (tools.ToolResult, error) {
		if getStr(args, "path") == "a.go" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return tools.ToolResult{}, ctx.Err()
			}
		} else {
			select {
			case <-started:
			case <-ctx.Done():
				return tools.ToolResult{}, ctx.Err()
			}
		}
		return tools.ToolResult{Content: getStr(args, "path")}, nil
	}
	updates := make(chan string, 16)
	done := make(chan execResult, 1)
	go func() {
		done <- a.executeOneToolCall(ctx, provider.ToolCall{ID: "batch", InternalID: "batch", Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"a.go"}},{"recipient_name":"read_file","parameters":{"path":"b.go"}}]}`}}, func(event AgentEvent) {
			if event.Type == "tool_call_streaming" {
				data, _ := event.Data.(map[string]any)
				content, _ := data["content"].(string)
				updates <- content
			}
		}, nil, new(string))
	}()
	found := false
	for !found {
		select {
		case body := <-updates:
			var v struct {
				Results []map[string]any `json:"results"`
			}
			if err := json.Unmarshal([]byte(body), &v); err != nil {
				t.Fatal(err)
			}
			if len(v.Results) == 2 && v.Results[1]["result"] == "b.go" {
				found = true
				if v.Results[0]["phase"] != "running" {
					t.Fatalf("lost first child's active state: %s", body)
				}
			}
		case <-ctx.Done():
			t.Fatal("no partial batch result before slow child finished")
		}
	}
	close(release)
	if result := <-done; result.toolError {
		t.Fatal(result.result.Content)
	}
}

func TestParallelArgumentStreamingExposesPreparingChildren(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	var seen []string
	callback := a.makeStreamingDeltaCallback(func(event AgentEvent) {
		data, _ := event.Data.(map[string]any)
		if input, _ := data["batch_input"].(string); input != "" {
			seen = append(seen, input)
		}
	}, make(map[string]string))
	callback("batch", "batch", "multi_tool_use.parallel", `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"provider/first.go"}}`)
	callback("batch", "batch", "multi_tool_use.parallel", `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"provider/first.go"}},{"recipient_name":"read_file","parameters":{"path":"provider/second.go"}}]}`)
	if len(seen) != 2 || !strings.Contains(seen[0], "first.go") || !strings.Contains(seen[1], "second.go") {
		t.Fatalf("batch preparation stayed opaque: %v", seen)
	}
}

func TestParallelPreviewWaitsOnlyForToolNameBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, input, boundary, want string
	}{
		{"read", `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"src/first.go"}}]}`, `"functions.read_file"`, "functions.read_file"},
		{"known prefix of unknown tool", `{"tool_uses":[{"recipient_name":"read_file_extra","parameters":{"path":"src/first.go"}}]}`, `"read_file_extra"`, "read_file_extra"},
		{"escaped name", `{"tool_uses":[{"recipient_name":"r\u0065ad_file","parameters":{"path":"src/first.go"}}]}`, `"r\u0065ad_file"`, "read_file"},
		{"parameters first", `{"tool_uses":[{"parameters":{"content":"recipient_name: read_file","path":"a.go"},"recipient_name":"write_file"}]}`, `"write_file"`, "write_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
			var published map[string]any
			callback := a.makeStreamingDeltaCallback(func(event AgentEvent) {
				data, _ := event.Data.(map[string]any)
				if input, _ := data["batch_input"].(string); input != "" {
					if err := json.Unmarshal([]byte(input), &published); err != nil {
						t.Fatal(err)
					}
				}
			}, make(map[string]string))
			boundary := strings.Index(tc.input, tc.boundary) + len(tc.boundary)
			for end := 0; end <= len(tc.input); end++ {
				callback("batch", "batch", "multi_tool_use.parallel", tc.input[:end])
				got := ""
				if uses, ok := published["tool_uses"].([]any); ok && len(uses) > 0 {
					if use, ok := uses[0].(map[string]any); ok {
						got, _ = use["recipient_name"].(string)
					}
				}
				want := ""
				if end >= boundary {
					want = tc.want
				}
				if got != want {
					t.Fatalf("prefix %q: name=%q, want %q", tc.input[:end], got, want)
				}
			}
		})
	}
	// A closed name must not make us wait for the parameter string, object,
	// child, or batch to finish before publishing the useful streaming preview.
	for _, suffix := range []string{"src/fi", "src/first.go"} {
		preview := ExtractPreview("batch", "multi_tool_use.parallel", `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"`+suffix, nil)
		uses, _ := preview.Params["tool_uses"].([]any)
		if len(uses) != 1 {
			t.Fatalf("named streaming child missing: %+v", preview)
		}
		use, _ := uses[0].(map[string]any)
		params, _ := use["parameters"].(map[string]any)
		if use["recipient_name"] != "read_file" || params["path"] != suffix || preview.Complete {
			t.Fatalf("partial operand stopped streaming: %+v", preview)
		}
	}
}

func TestToolCallEventCarriesExactArguments(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai", "key", ""), "gpt-test")
	a.Yolo = true
	a.configureModelTools()
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "ok"}, nil
	}
	args, _ := json.Marshal(map[string]any{"tool_uses": []map[string]any{{"recipient_name": "functions.read_file", "parameters": map[string]string{"path": strings.Repeat("directory/", 12) + "name with spaces.go"}}}})
	seen := ""
	result := a.executeOneToolCall(t.Context(), provider.ToolCall{ID: "batch", Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: string(args)}}, func(event AgentEvent) {
		if event.Type == "tool_call" {
			data, _ := event.Data.(map[string]any)
			if data["tool_id"] == "batch" {
				seen, _ = data["args"].(string)
			}
		}
	}, nil, new(string))
	if result.toolError || seen != string(args) {
		t.Fatalf("execution event shortened the original arguments: %q; result %s", seen, result.result.Content)
	}
}
