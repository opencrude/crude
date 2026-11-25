package turnloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crude/agent/mcpclient"
	"crude/agent/tools"
	captypes "crude/types/capability"
	"crude/types/conversation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPFormThroughToolQuestionLane(t *testing.T) {
	const text = "Leila\nQ2: not another answer"
	received := make(chan *mcp.ElicitResult, 1)
	server := mcp.NewServer(&mcp.Implementation{Name: "form-peer", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "profile", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req.Params.InputResponses == nil {
			return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"profile": &mcp.ElicitParams{Message: "Choose your public name.", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)}}}, nil
		}
		received <- req.Params.InputResponses["profile"].(*mcp.ElicitResult)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "profile receipt"}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(endpoint.Close)
	manager := mcpclient.New(t.TempDir())
	t.Cleanup(manager.Close)
	manager.Configure(map[string]captypes.ConnectionConfig{"peer": {Name: "Form peer", Transport: "http", URL: endpoint.URL}})
	if err := manager.Connect(t.Context(), "peer"); err != nil {
		t.Fatal(err)
	}
	tool := manager.Catalog()[0].Tools[0]
	for _, mode := range []string{"human", "skip", "decline", "cancel", "agent", "external", "excluded", "no-lane"} {
		t.Run(mode, func(t *testing.T) {
			a := newPermissionLifecycleAgent(t)
			a.Yolo = true // This must not supply a human answer.
			a.FilteredTools = []tools.ToolDef{{Type: "function", Function: tools.ToolFuncDef{Name: tool.Name}}}
			if mode != "excluded" {
				a.FilteredTools = append(a.FilteredTools, tools.ToolDef{Type: "function", Function: tools.ToolFuncDef{Name: "question"}})
			}
			a.ToolMetadata = func(string) (tools.ToolMetadata, bool) {
				return tools.ToolMetadata{Name: tool.Name, Scope: tool.Scope, Dynamic: true, Effect: tool.Effect, Fingerprint: tool.Fingerprint}, true
			}
			a.ToolFn = func(ctx context.Context, _ string, args map[string]any) (tools.ToolResult, error) {
				r, err := manager.Call(ctx, tool, args)
				if err != nil {
					return tools.ToolResult{}, err
				}
				if len(r.Content) != 1 {
					return tools.ToolResult{}, errors.New("missing receipt")
				}
				return tools.ToolResult{Content: r.Content[0].Text}, nil
			}
			ch := make(chan conversation.BlockResponse, 2)
			if mode == "no-lane" {
				ch = nil
			}
			call := editPermissionCall()
			call.Function.Name, call.Function.Arguments = tool.Name, `{}`
			asked := 0
			result := a.executeOneToolCall(t.Context(), call, func(event AgentEvent) {
				if event.Type != "blocking" {
					return
				}
				asked++
				d := event.Data.(map[string]any)
				var qs []normalizedQuestion
				if d["operation"] != "questions" || json.Unmarshal([]byte(d["questions_json"].(string)), &qs) != nil || len(qs) != 2 || !strings.Contains(qs[0].Question, `MCP server "peer", tool "profile"`) || !strings.Contains(qs[0].Question, `"name"`) || qs[1].Options[0] != "Decline" {
					t.Fatalf("bad question: %+v", d)
				}
				id := d["block_id"].(string)
				if id == call.InternalID || !strings.HasPrefix(id, call.InternalID+":form:") {
					t.Fatalf("unscoped question %q", id)
				}
				ch <- conversation.BlockResponse{RequestID: call.InternalID, Value: "allow", Outcome: conversation.BlockAnswered}
				resp := conversation.BlockResponse{RequestID: id, Outcome: conversation.BlockAnswered, Value: "unstructured reply must not reach the server"}
				resp.HumanAnswers = [4]string{text, "Send answers"}
				switch mode {
				case "skip":
					resp.HumanAnswers[1] = ""
				case "decline":
					resp.HumanAnswers[1] = "Decline"
				case "cancel":
					resp.Outcome = conversation.BlockUserCancelled
				case "agent", "external":
					resp.AttributionKind = mode
				}
				ch <- resp
			}, ch, new(string))
			if mode == "excluded" || mode == "no-lane" {
				if asked != 0 || !result.toolError {
					t.Fatalf("noninteractive call: prompts=%d result=%+v", asked, result)
				}
				return
			}
			if result.toolError || asked != 1 || result.result.Content != "profile receipt" {
				t.Fatalf("prompts=%d result=%+v", asked, result)
			}
			answer := <-received
			want := "cancel"
			if mode == "human" {
				want = "accept"
			} else if mode == "decline" {
				want = "decline"
			}
			if answer.Action != want || want == "accept" && answer.Content["name"] != text || want != "accept" && len(answer.Content) != 0 {
				t.Fatalf("on-wire answer=%+v", answer)
			}
		})
	}
}

func TestMCPFormCompletesLargeHumanApprovedWorkflow(t *testing.T) {
	// Six rounds, five simultaneous requests, and a 20-field, >16 KiB form.
	// Only the question presentation has a page size; it is not an MCP limit.
	props := map[string]any{
		"tokenCount": map[string]string{"type": "integer", "description": "Enter your token count."},
		"palette":    map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"a, b", "c"}}},
		"region":     map[string]any{"type": "string", "enum": []string{"one", "two", "three", "four", "five", "six"}},
	}
	for i := 0; i < 17; i++ {
		props[fmt.Sprint("note", i)] = map[string]string{"type": "string"}
	}
	props["note0"] = map[string]string{"type": "string", "description": strings.Repeat("Public note. ", 1400)}
	server := mcp.NewServer(&mcp.Implementation{Name: "peer", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "profile", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		round := 0
		if req.Params.RequestState != nil {
			if _, err := fmt.Sscanf(*req.Params.RequestState, "opaque-%d", &round); err != nil || len(req.Params.InputResponses) != 5 {
				return nil, errors.New("wrong round or missing answer")
			}
			for i := range 5 {
				r, ok := req.Params.InputResponses[fmt.Sprint(i)].(*mcp.ElicitResult)
				if !ok || r.Action != "accept" || r.Content["region"] != "six" || r.Content["tokenCount"] != float64(97) || fmt.Sprint(r.Content["palette"]) != "[a, b c]" || len(r.Content) != 3 {
					return nil, errors.New("answers changed in transit")
				}
			}
		}
		if round == 6 {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "finished six rounds"}}}, nil
		}
		list := mcp.InputRequestMap{}
		for i := range 5 {
			list[fmt.Sprint(i)] = &mcp.ElicitParams{Message: "Choose the public profile values.", RequestedSchema: map[string]any{"type": "object", "properties": props}}
		}
		return &mcp.CallToolResult{RequestState: new(fmt.Sprintf("opaque-%d", round+1)), InputRequests: list}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(endpoint.Close)
	m := mcpclient.New(t.TempDir())
	t.Cleanup(m.Close)
	m.Configure(map[string]captypes.ConnectionConfig{"peer": {Name: "Peer", Transport: "http", URL: endpoint.URL}})
	if err := m.Connect(t.Context(), "peer"); err != nil {
		t.Fatal(err)
	}
	tool := m.Catalog()[0].Tools[0]
	responses := make(chan conversation.BlockResponse, 1)
	shares, pages := 0, 0
	ask := formQuestionLane("call", tool.Name, func(e AgentEvent) {
		if e.Type != "blocking" {
			return
		}
		pages++
		d := e.Data.(map[string]any)
		var qs []normalizedQuestion
		if err := json.Unmarshal([]byte(d["questions_json"].(string)), &qs); err != nil || len(qs) > 4 {
			t.Fatalf("wrong question page: %v", err)
		}
		r := conversation.BlockResponse{RequestID: d["block_id"].(string), Outcome: conversation.BlockAnswered}
		for i, q := range qs {
			switch {
			case q.Header == "Share answers":
				r.HumanAnswers[i] = "Send answers"
				shares++
			case strings.Contains(q.Question, `asks for "tokenCount"`):
				r.HumanAnswers[i] = "97"
			case strings.Contains(q.Question, `asks for "palette"`):
				if len(q.Options) != 0 || !strings.Contains(q.Question, "JSON list") {
					t.Fatal("ambiguous choice separator")
				}
				r.HumanAnswers[i] = `["a, b","c"]`
			case strings.Contains(q.Question, `asks for "region"`):
				if len(q.Options) != 0 || !strings.Contains(q.Question, "quoted JSON string") {
					t.Fatal("large enum lost its choices")
				}
				r.HumanAnswers[i] = `"six"`
			}
		}
		responses <- r
	}, responses)
	r, err := m.Call(captypes.WithFormHandler(t.Context(), tool.Name, ask), tool, nil)
	if err != nil || r.IsError || shares != 30 || pages != 180 || len(r.Content) != 1 || r.Content[0].Text != "finished six rounds" {
		t.Fatalf("pages=%d shared=%d result=%+v err=%v", pages, shares, r, err)
	}
}
