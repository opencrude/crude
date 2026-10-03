package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func continuationFixture(t *testing.T, endpoint string) (*Provider, ChatRequest) {
	t.Helper()
	p := FromPreset("openai-chat", "key", endpoint)
	if err := p.SetProtocolID("openai-responses"); err != nil {
		t.Fatal(err)
	}
	p.Model = "continuation-model"
	p.AccountID = "account-a"
	req := ChatRequest{Model: p.Model, ResponseContinuationScope: p.ResponseContinuationScope(p.Model),
		Messages: []ChatMessage{NewTextMessage("system", strings.Repeat("stable policy\n", 8000)), NewTextMessage("user", "inspect")}}
	message := ChatMessage{Role: "assistant", Content: TextContent(""), ToolCalls: []ToolCall{{ID: "patch", Type: "custom", Function: FunctionCall{Name: "apply_patch", Arguments: "patch"}}}}
	BindResponseReference(&message, &ResponseReference{ID: "response-1", Scope: req.ResponseContinuationScope, InputHash: ResponseInputHash(req.Messages, req.Tools)})
	req.Messages = append(req.Messages, message, ChatMessage{Role: "tool", ToolCallID: "patch", Content: TextContent("written")})
	return p, req
}

func TestResponsesContinuationSendsOnlyDeltaWithoutChangingHistory(t *testing.T) {
	_, req := continuationFixture(t, "https://api.openai.com/v1")
	before, err := json.Marshal(req.Messages)
	if err != nil {
		t.Fatal(err)
	}
	body, err := (&OpenAIResponsesStandard{}).EncodeRequest(&req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Previous string `json:"previous_response_id"`
		Input    []struct {
			Type, Output string
			CallID       string `json:"call_id"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Previous != "response-1" || len(wire.Input) != 1 || wire.Input[0].Type != "custom_tool_call_output" || wire.Input[0].CallID != "patch" || wire.Input[0].Output != "written" {
		t.Fatalf("incorrect delta: previous=%q items=%d bytes=%d", wire.Previous, len(wire.Input), len(body))
	}
	full := req
	full.ResponseContinuationScope = ""
	fullBody, err := (&OpenAIResponsesStandard{}).EncodeRequest(&full)
	if err != nil {
		t.Fatal(err)
	}
	if len(body)*100 >= len(fullBody) {
		t.Fatalf("upload reduction below 99%%: full=%d delta=%d", len(fullBody), len(body))
	}
	after, err := json.Marshal(req.Messages)
	if err != nil || string(before) != string(after) {
		t.Fatal("delta encoding mutated canonical history")
	}
}

func TestResponsesContinuationResetsOnExactContextOrConnectionChange(t *testing.T) {
	for _, change := range []string{"system", "output", "tools", "model", "account", "endpoint", "partial", "compaction", "attachment"} {
		t.Run(change, func(t *testing.T) {
			p, req := continuationFixture(t, "https://api.openai.com/v1")
			switch change {
			case "system":
				req.Messages[0].Content = TextContent("new policy")
			case "output":
				req.Messages[2].ToolCalls[0].Function.Arguments = "changed patch"
			case "tools":
				req.Tools = []ToolDef{{Type: "function", Function: FunctionDef{Name: "read_file"}}}
			case "model":
				req.Model = "another-model"
				req.ResponseContinuationScope = p.ResponseContinuationScope(req.Model)
			case "account":
				p.AccountID = "account-b"
				req.ResponseContinuationScope = p.ResponseContinuationScope(req.Model)
			case "endpoint":
				p.BaseURL = "https://other.invalid/v1"
				req.ResponseContinuationScope = p.ResponseContinuationScope(req.Model)
			case "partial":
				req.Messages = append(req.Messages, NewTextMessage("assistant", "unfinished"))
			case "compaction":
				req.Messages = req.Messages[1:]
			case "attachment":
				req.Messages[1] = NewMultiPartMessage("user", []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,YQ=="}}})
				BindResponseReference(&req.Messages[2], &ResponseReference{ID: "response-1", Scope: req.ResponseContinuationScope, InputHash: ResponseInputHash(req.Messages[:2], req.Tools)})
				req.Messages[1] = NewMultiPartMessage("user", []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,Yg=="}}})
			}
			if previous, start := responseContinuation(&req); previous != "" || start != 0 {
				t.Fatalf("unsafe %s reused %s", change, previous)
			}
		})
	}
}

func TestResponseContinuationRespectsStorageAndProtocol(t *testing.T) {
	p, _ := continuationFixture(t, "https://api.openai.com/v1")
	off, on := false, true
	for _, settings := range []*ProtocolSettings{{Store: &off}, {ZeroDataRetention: true, Store: &on}, {Truncation: &on}} {
		p.SetProtocolSettings(settings)
		if p.ResponseContinuationScope(p.Model) != "" {
			t.Fatal("retention/truncation policy was bypassed")
		}
	}
	p.SetProtocolSettings(&ProtocolSettings{ZeroDataRetention: true, Store: &on})
	p.SetExtraBody(map[string]any{"store": true})
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.encodeChatRequest(&ChatRequest{Model: p.Model})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Store bool `json:"store"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.Store {
		t.Fatal("generic extra body overrode zero data retention")
	}
	p.SetExtraBody(nil)
	p.SetProtocolSettings(nil)
	for _, protocol := range []string{"openai", "codex", "anthropic"} {
		if err := p.SetProtocolID(protocol); err != nil {
			t.Fatal(err)
		}
		if p.ResponseContinuationScope(p.Model) != "" {
			t.Fatal("unsupported protocol got a stored-response reference")
		}
	}
}

func TestResponsesContinuationOnlyAdoptsCompletedStoredResponses(t *testing.T) {
	for _, state := range []string{`"status":"completed","store":true`, `"status":"completed","store":false`, `"status":"incomplete","store":true`} {
		payload := fmt.Sprintf(`{"id":"r",%s,"output":[]}`, state)
		response, err := (&OpenAIResponsesStandard{}).DecodeResponse([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Contains(state, `"completed"`) && strings.Contains(state, `"store":true`)
		if (response.ContinuationID != "") != want {
			t.Fatalf("invalid completion anchor: %s", payload)
		}
	}
}

func TestResponsesContinuationTracksOfficialStreamingLifecycle(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	for _, event := range []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp-official","status":"in_progress","store":true,"output":[]}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"resp-official","status":"in_progress","store":true,"output":[]}}`,
	} {
		update, err := decoder.Decode(StreamEvent{Data: []byte(event)})
		if err != nil || update.ContinuationID != "" || update.Done {
			t.Fatalf("nonterminal lifecycle event was not passive: %+v err=%v", update, err)
		}
	}
	completed, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.completed","sequence_number":2,"response":{"id":"resp-official","status":"completed","store":true,"output":[]}}`)})
	if err != nil || !completed.Done || completed.ContinuationID != "resp-official" || completed.ContinuationReason != "response_id_retained" {
		t.Fatalf("official terminal event did not retain its ID: %+v err=%v", completed, err)
	}
}

func TestResponsesContinuationUsesCreatedIDWhenGatewayOmitsTerminalDuplicate(t *testing.T) {
	decoder := (&OpenAIResponsesStandard{}).NewStreamDecoder()
	if _, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.created","response":{"id":"resp-created","status":"in_progress","store":true,"output":[]}}`)}); err != nil {
		t.Fatal(err)
	}
	completed, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.completed","response":{"status":"completed","output":[]}}`)})
	if err != nil || completed.ContinuationID != "resp-created" {
		t.Fatalf("creation-event authority was lost: %+v err=%v", completed, err)
	}

	decoder = (&OpenAIResponsesStandard{}).NewStreamDecoder()
	if _, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.created","response":{"id":"first","status":"in_progress","store":true,"output":[]}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Decode(StreamEvent{Data: []byte(`{"type":"response.completed","response":{"id":"different","status":"completed","store":true,"output":[]}}`)}); err == nil {
		t.Fatal("conflicting lifecycle response IDs were accepted")
	}
}

func TestResponsesContinuationReportsWhyNoIDCanBeRetained(t *testing.T) {
	for name, wire := range map[string]string{
		"missing id": `{"type":"response.completed","response":{"status":"completed","store":true,"output":[]}}`,
		"not stored": `{"type":"response.completed","response":{"id":"response","status":"completed","store":false,"output":[]}}`,
		"incomplete": `{"type":"response.incomplete","response":{"id":"response","status":"incomplete","store":true,"output":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			update, err := (&OpenAIResponsesStandard{}).NewStreamDecoder().Decode(StreamEvent{Data: []byte(wire)})
			if err != nil || update.ContinuationID != "" || update.ContinuationReason == "" {
				t.Fatalf("silent continuation fallback: %+v err=%v", update, err)
			}
		})
	}
}

func TestResponsesContinuationRecoversMissingReferenceOnce(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var wire struct {
					Previous string            `json:"previous_response_id"`
					Input    []json.RawMessage `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
					return
				}
				if requests == 1 {
					if wire.Previous != "response-1" || len(wire.Input) != 1 {
						t.Error("initial request was not a delta")
					}
					w.WriteHeader(http.StatusNotFound)
					_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","param":"previous_response_id","message":"Reference expired"}}`)
					return
				}
				if wire.Previous != "" || len(wire.Input) != 4 {
					t.Error("recovery omitted full authoritative context")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"id":"response-2","status":"completed","store":true,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
			}))
			defer server.Close()
			p, req := continuationFixture(t, server.URL)
			client, err := p.Client(WithMaxAttempts(1))
			if err != nil {
				t.Fatal(err)
			}
			if streaming {
				stream, err := client.Stream(t.Context(), &req)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				update, err := stream.Next()
				if err != nil || update.ContentDelta != "done" || update.ContinuationID != "response-2" {
					t.Fatalf("%+v %v", update, err)
				}
			} else {
				response, err := client.Complete(t.Context(), &req)
				if err != nil || response.Choices[0].Message.ResponseReference == nil {
					t.Fatalf("%+v %v", response, err)
				}
			}
			if requests != 2 || req.ResponseContinuationScope == "" {
				t.Fatal("recovery did not stay bounded and request-owned")
			}
		})
	}
}

func TestContinuationUnavailableRequiresStructuredReferenceEvidence(t *testing.T) {
	for _, err := range []error{
		&StatusError{StatusCode: http.StatusNotFound, Param: "previous_response_id"},
		&StatusError{StatusCode: http.StatusBadRequest, APICode: "previous_response_not_found"},
		&APIError{Code: "previous_response_not_found"},
		&APIError{Param: "previous_response_id"},
	} {
		if !continuationUnavailable(err) {
			t.Errorf("structured rejected reference was not recoverable: %+v", err)
		}
	}
	for _, err := range []error{
		&StatusError{StatusCode: http.StatusNotFound, Message: "previous response not found"},
		&StatusError{StatusCode: http.StatusBadRequest, Param: "model"},
		&APIError{Message: "previous_response_id unavailable"},
	} {
		if continuationUnavailable(err) {
			t.Errorf("unstructured or unrelated error triggered recovery: %+v", err)
		}
	}
}

func TestResponsesContinuationNeverReplaysAcceptedOutputOrCancellation(t *testing.T) {
	for _, scenario := range []string{"partial", "cancelled", "unrelated-error", "repeated-reference-error"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if scenario == "partial" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"error\",\"error\":{\"param\":\"previous_response_id\",\"message\":\"expired\"}}\n\n")
				} else {
					w.WriteHeader(http.StatusBadRequest)
					param := "previous_response_id"
					if scenario == "unrelated-error" {
						param = "model"
					}
					_, _ = fmt.Fprintf(w, `{"error":{"param":%q,"message":"not usable"}}`, param)
				}
			}))
			defer server.Close()
			p, req := continuationFixture(t, server.URL)
			client, err := p.Client(WithMaxAttempts(1))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			stream, err := client.Stream(ctx, &req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for err == nil {
				_, err = stream.Next()
			}
			if err == io.EOF {
				t.Fatal("invalid request was accepted")
			}
			want := 1
			if scenario == "cancelled" {
				want = 0
			}
			if scenario == "repeated-reference-error" {
				want = 2
			}
			if requests != want {
				t.Fatalf("unsafe replay: requests=%d want=%d", requests, want)
			}
		})
	}
}
