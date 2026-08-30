package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOllamaEncodeRequestTextAndOptions(t *testing.T) {
	s := &OllamaStandard{}
	body, err := s.EncodeRequest(&ChatRequest{
		Model:  "llama3",
		Stream: true,
		Messages: []ChatMessage{
			{Role: "user", Content: TextContent("hello")},
			{Role: "assistant", Content: TextContent("hi")},
		},
		MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["model"] != "llama3" {
		t.Errorf("model = %v", got["model"])
	}
	if got["stream"] != true {
		t.Errorf("stream = %v", got["stream"])
	}
	if _, ok := got["max_tokens"]; ok {
		t.Errorf("ollama must not send top-level max_tokens: %#v", got)
	}
	opts, ok := got["options"].(map[string]any)
	if !ok || opts["num_predict"] != float64(128) {
		t.Errorf("options.num_predict missing/wrong: %#v", got["options"])
	}
	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hello" {
		t.Errorf("first message = %#v", first)
	}
}

func TestOllamaEncodeRequestMultipartImagesStripped(t *testing.T) {
	s := &OllamaStandard{}
	body, err := s.EncodeRequest(&ChatRequest{
		Model: "llava",
		Messages: []ChatMessage{
			NewMultiPartMessage("user", []ContentPart{
				{Type: "text", Text: "what is this?"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,QUJDQGc="}},
			}),
		},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	msg := got["messages"].([]any)[0].(map[string]any)
	if msg["content"] != "what is this?" {
		t.Errorf("content = %v", msg["content"])
	}
	imgs, ok := msg["images"].([]any)
	if !ok || len(imgs) != 1 || imgs[0] != "QUJDQGc=" {
		t.Errorf("images = %#v (want base64 payload without data: prefix)", msg["images"])
	}
}

func TestOllamaDecodeResponseTextUsageAndDoneReason(t *testing.T) {
	s := &OllamaStandard{}
	resp, err := s.DecodeResponse([]byte(`{
		"model":"llama3",
		"message":{"role":"assistant","content":"hi there"},
		"done":true,"done_reason":"stop",
		"prompt_eval_count":7,"eval_count":3
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d", len(resp.Choices))
	}
	c := resp.Choices[0]
	if c.Message.Role != "assistant" || c.Message.Content.String() != "hi there" {
		t.Errorf("message = %+v", c.Message)
	}
	if c.FinishReason != "stop" {
		t.Errorf("finish_reason = %q", c.FinishReason)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 3 ||
		resp.Usage.TotalTokens != 10 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestOllamaDecodeResponseToolCallsFinishReason(t *testing.T) {
	s := &OllamaStandard{}
	resp, err := s.DecodeResponse([]byte(`{
		"model":"qwen3",
		"message":{"role":"assistant","content":"",
		"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"sf\"}"}}]},
		"done":true
	}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	c := resp.Choices[0]
	if c.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", c.FinishReason)
	}
	if len(c.Message.ToolCalls) != 1 || c.Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool calls = %+v", c.Message.ToolCalls)
	}
}

func TestOllamaDecodeResponseErrorPropagates(t *testing.T) {
	s := &OllamaStandard{}
	_, err := s.DecodeResponse([]byte(`{"error":"model not found"}`))
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestOllamaStreamDecoderAccumulatesAndTerminates(t *testing.T) {
	s := &OllamaStandard{}
	dec := s.NewStreamDecoder()
	lines := []string{
		`{"model":"llama3","message":{"role":"assistant","content":"Hel"}}`,
		`{"model":"llama3","message":{"role":"assistant","content":"lo"}}`,
		`{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":4,"eval_count":2}`,
	}
	var content strings.Builder
	var finish string
	var usage *TokenUsage
	for _, line := range lines {
		update, err := dec.Decode(StreamEvent{Data: []byte(line)})
		if err != nil {
			t.Fatalf("decode line: %v", err)
		}
		content.WriteString(update.ContentDelta)
		if update.Done {
			finish = update.FinishReason
			usage = update.Usage
		}
	}
	if content.String() != "Hello" {
		t.Errorf("content = %q", content.String())
	}
	if finish != "stop" {
		t.Errorf("finish = %q", finish)
	}
	if usage == nil || usage.PromptTokens != 4 || usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestOllamaDecodeModelsFromTags(t *testing.T) {
	s := &OllamaStandard{}
	page, err := s.DecodeModelsPage([]byte(`{"models":[
		{"name":"llama3:8b","model":"llama3:8b","details":{"parameter_size":"8B"}},
		{"name":"","model":""},
		{"model":"mistral:7b"}
	]}`))
	if err != nil {
		t.Fatalf("DecodeModelsPage: %v", err)
	}
	models := page.Models
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ID != "llama3:8b" || models[1].ID != "mistral:7b" {
		t.Errorf("models = %+v", models)
	}
}

func TestOllamaRequestExtrasThinkingToggle(t *testing.T) {
	s := &OllamaStandard{}
	if got := s.RequestExtras("qwen3", "", ProtocolSettings{}); got != nil {
		t.Errorf("RequestExtras(empty) = %#v, want nil", got)
	}
	if got := s.RequestExtras("qwen3", "none", ProtocolSettings{}); got != nil {
		t.Errorf("RequestExtras(none) = %#v, want nil", got)
	}
	got := s.RequestExtras("qwen3", "high", ProtocolSettings{})
	if got["think"] != true {
		t.Errorf("RequestExtras(high) = %#v, want think:true", got)
	}
}

func TestOllamaDecodeResponseFoldsNDJSONBody(t *testing.T) {
	// Gateways that label NDJSON streams "application/json" hand the whole
	// chunk sequence to DecodeResponse. The chunks must fold into one
	// response rather than fail with "invalid character '{'".
	s := &OllamaStandard{}
	body := strings.Join([]string{
		`{"model":"qwen3","message":{"role":"assistant","content":"Hel"}}`,
		`{"model":"qwen3","message":{"role":"assistant","content":"lo","thinking":"hmm"}}`,
		`{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}]}}`,
		`{"model":"qwen3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":9,"eval_count":4}`,
		"",
	}, "\n")
	resp, err := s.DecodeResponse([]byte(body))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if resp.Model != "qwen3" {
		t.Errorf("model = %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d", len(resp.Choices))
	}
	c := resp.Choices[0]
	if c.Message.Content.String() != "Hello" {
		t.Errorf("content = %q, want folded %q", c.Message.Content.String(), "Hello")
	}
	if c.Message.Reasoning != "hmm" {
		t.Errorf("reasoning = %q", c.Message.Reasoning)
	}
	if len(c.Message.ToolCalls) != 1 || c.Message.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("tool calls = %+v", c.Message.ToolCalls)
	}
	if c.FinishReason != "stop" {
		t.Errorf("finish reason = %q", c.FinishReason)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 9 || resp.Usage.CompletionTokens != 4 ||
		resp.Usage.TotalTokens != 13 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestOllamaDecodeResponseNDJSONDefaultsTypeAndRole(t *testing.T) {
	s := &OllamaStandard{}
	resp, err := s.DecodeResponse([]byte(
		`{"message":{"role":"assistant","tool_calls":[{"id":"c1","function":{"name":"f","arguments":"{}"}}]}}` + "\n" +
			`{"done":true,"done_reason":"stop"}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	c := resp.Choices[0]
	if c.Message.Role != "assistant" {
		t.Errorf("role = %q", c.Message.Role)
	}
	if len(c.Message.ToolCalls) != 1 || c.Message.ToolCalls[0].Type != "function" {
		t.Errorf("tool calls = %+v", c.Message.ToolCalls)
	}
}

func TestOllamaDecodeResponseNDJSONErrorChunkPropagates(t *testing.T) {
	s := &OllamaStandard{}
	_, err := s.DecodeResponse([]byte(
		`{"message":{"role":"assistant","content":"par"}}` + "\n" +
			`{"error":"model requires more context"}`))
	if err == nil || !strings.Contains(err.Error(), "model requires more context") {
		t.Fatalf("err = %v", err)
	}
}

func TestOllamaDecodeResponseNDJSONWithoutChunksFails(t *testing.T) {
	s := &OllamaStandard{}
	_, err := s.DecodeResponse([]byte("\n  \n"))
	if err == nil {
		t.Fatal("whitespace-only body must fail")
	}
}

func TestOllamaStreamThroughMislabeledJSONContent(t *testing.T) {
	// End-to-end regression for the reported failure: an Ollama-compatible
	// gateway streams NDJSON but declares Content-Type "application/json".
	// The framing sniff must route by body shape, not by header.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, strings.Join([]string{
			`{"model":"llama3","message":{"role":"assistant","content":"He"}}`,
			`{"model":"llama3","message":{"role":"assistant","content":"y"}}`,
			`{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":2}`,
			"",
		}, "\n"))
	}))
	defer server.Close()

	p := FromPreset("ollama-cloud", "key", server.URL)
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "llama3", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var content strings.Builder
	var done bool
	for {
		u, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		content.WriteString(u.ContentDelta)
		if u.Done {
			done = true
			if u.FinishReason != "stop" {
				t.Errorf("finish reason = %q", u.FinishReason)
			}
			if u.Usage == nil || u.Usage.PromptTokens != 3 || u.Usage.CompletionTokens != 2 {
				t.Errorf("usage = %+v", u.Usage)
			}
		}
	}
	if content.String() != "Hey" {
		t.Errorf("content = %q, want %q", content.String(), "Hey")
	}
	if !done {
		t.Error("stream never reported done")
	}
}

func TestOllamaStreamDecoderObjectArguments(t *testing.T) {
	// Ollama's native protocol carries tool-call arguments as a JSON object,
	// not an OpenAI-style string. This is the shape ollama.com streams.
	s := &OllamaStandard{}
	dec := s.NewStreamDecoder()
	update, err := dec.Decode(StreamEvent{Data: []byte(
		`{"model":"glm-5.3-flash","message":{"role":"assistant","content":"","tool_calls":[` +
			`{"function":{"name":"read_file","arguments":{"path":"a.go","offset":3}}}]},` +
			`"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":6,` +
			`"total_duration":900,"load_duration":100,"prompt_eval_duration":200,"eval_duration":300}`)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !update.Done || update.FinishReason != "stop" {
		t.Fatalf("update = %+v", update)
	}
	if len(update.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", update.ToolCalls)
	}
	call := update.ToolCalls[0]
	if call.Name != "read_file" {
		t.Errorf("name = %q", call.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("arguments %q is not JSON: %v", call.Arguments, err)
	}
	if args["path"] != "a.go" || args["offset"] != float64(3) {
		t.Errorf("arguments = %v", args)
	}
	if update.Usage == nil || update.Usage.TotalDuration != 900*time.Nanosecond ||
		update.Usage.PromptEvalDuration != 200*time.Nanosecond {
		t.Fatalf("timing usage = %+v", update.Usage)
	}
}

func TestOllamaStreamDecoderStringArgumentsStillDecode(t *testing.T) {
	// Gateways that proxy the OpenAI shape send arguments as a string; both
	// forms must decode.
	s := &OllamaStandard{}
	dec := s.NewStreamDecoder()
	update, err := dec.Decode(StreamEvent{Data: []byte(
		`{"message":{"role":"assistant","tool_calls":[{"function":{"name":"f","arguments":"{\"k\":1}"}}]}}`)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(update.ToolCalls) != 1 || update.ToolCalls[0].Arguments != `{"k":1}` {
		t.Fatalf("tool calls = %+v", update.ToolCalls)
	}
}

func TestOllamaEncodeRequestToolCallsUseObjectArguments(t *testing.T) {
	// Conversation history replayed to Ollama must carry arguments in the
	// native object form, or the daemon rejects the message.
	s := &OllamaStandard{}
	body, err := s.EncodeRequest(&ChatRequest{
		Model: "glm-5.3-flash",
		Messages: []ChatMessage{
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					Type: "function",
					Function: FunctionCall{
						Name: "read_file", Arguments: `{"path":"a.go","offset":3}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: TextContent("contents")},
		},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg := got["messages"].([]any)[0].(map[string]any)
	call := msg["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	args, ok := call["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("arguments = %#v, want object form", call["arguments"])
	}
	if args["path"] != "a.go" || args["offset"] != float64(3) {
		t.Errorf("arguments = %v", args)
	}
}

func TestOllamaStreamThroughMislabeledJSONWithToolCalls(t *testing.T) {
	// Full pipeline: ollama.com labels its NDJSON stream "application/json"
	// and the model answers with a tool call carrying object arguments.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, strings.Join([]string{
			`{"model":"glm-5.3-flash","message":{"role":"assistant","content":"","thinking":"need the file"}}`,
			`{"model":"glm-5.3-flash","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"read_file","arguments":{"path":"a.go"}}}]}}`,
			`{"model":"glm-5.3-flash","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
			"",
		}, "\n"))
	}))
	defer server.Close()

	p := FromPreset("ollama-cloud", "key", server.URL)
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	stream, err := client.Stream(t.Context(), &ChatRequest{Model: "glm-5.3-flash", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var reasoning strings.Builder
	var calls []ToolCallDelta
	var done bool
	for {
		u, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		reasoning.WriteString(u.ReasoningDelta)
		calls = append(calls, u.ToolCalls...)
		if u.Done {
			done = true
		}
	}
	if reasoning.String() != "need the file" {
		t.Errorf("reasoning = %q", reasoning.String())
	}
	if len(calls) != 1 || calls[0].Name != "read_file" || calls[0].Arguments != `{"path":"a.go"}` {
		t.Errorf("tool calls = %+v", calls)
	}
	if !done {
		t.Error("stream never reported done")
	}
}
