package provider

import (
	"encoding/json"
	"strings"
	"testing"
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
	models, err := s.DecodeModels([]byte(`{"models":[
		{"name":"llama3:8b","model":"llama3:8b","details":{"parameter_size":"8B"}},
		{"name":"","model":""},
		{"model":"mistral:7b"}
	]}`))
	if err != nil {
		t.Fatalf("DecodeModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ID != "llama3:8b" || models[1].ID != "mistral:7b" {
		t.Errorf("models = %+v", models)
	}
}

func TestOllamaExtraBodyThinkingToggle(t *testing.T) {
	s := &OllamaStandard{}
	if got := s.ExtraBody("qwen3", ""); got != nil {
		t.Errorf("ExtraBody(empty) = %#v, want nil", got)
	}
	if got := s.ExtraBody("qwen3", "none"); got != nil {
		t.Errorf("ExtraBody(none) = %#v, want nil", got)
	}
	got := s.ExtraBody("qwen3", "high")
	if got["think"] != true {
		t.Errorf("ExtraBody(high) = %#v, want think:true", got)
	}
}
