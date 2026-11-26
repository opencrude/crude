package turnloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent/provider"
	"crude/internal/logger"
	"crude/internal/providersim"
)

func newTestAgent(t *testing.T, p *provider.Provider, model string) *Agent {
	t.Helper()
	lg, err := logger.New(logger.Options{
		HomeDir:   t.TempDir(),
		SessionID: strings.ReplaceAll(t.Name(), "/", "-"),
	})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	agent := NewAgent(p, model, lg)
	agent.MaxRetries = 1
	// These fixtures answer one section per request; combined single-pass
	// extraction has its own tests.
	if err := provider.RecordRequestLimit("", p, model, provider.LearnedSectioned, 1, provider.InferredLimitLifetime); err != nil {
		t.Fatal(err)
	}
	return agent
}

func completeForTest(t *testing.T, agent *Agent) (completionResult, error) {
	t.Helper()
	return agent.completeStream(
		context.Background(),
		[]provider.ChatMessage{provider.NewTextMessage("user", "hello")},
		nil,
		nil, nil, nil, nil, nil, nil, nil,
	)
}

func TestProviderDisplayNamePrefersConfiguredName(t *testing.T) {
	agent := &Agent{Provider: &provider.Provider{ID: "custom-id", Name: "Custom Gateway"}}
	if got := agent.providerDisplayName(); got != "Custom Gateway" {
		t.Fatalf("provider display name = %q", got)
	}
	agent.Provider.Name = ""
	if got := agent.providerDisplayName(); got != "custom-id" {
		t.Fatalf("provider ID fallback = %q", got)
	}
}

func TestCompleteStreamOpenAIChatPreservesUsageOnlyChunk(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.Unmarshal(r.Raw, &request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if _, ok := request["max_tokens"]; !ok {
			t.Errorf("request lacks max_tokens: %#v", request)
		}
		if _, ok := request["max_completion_tokens"]; ok {
			t.Errorf("request contains max_completion_tokens: %#v", request)
		}

		return providersim.Text("hello").WithUsage(providersim.Usage{Input: 12, Output: 3, Cached: 4})
	})

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)

	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "hello" || result.FinishReason != "stop" {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage.PromptTokens != 12 || result.Usage.CompletionTokens != 3 ||
		result.Usage.PromptTokensCached != 4 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

func TestCompleteStreamAnthropicToolUse(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Path != "/v1/messages" {
			t.Errorf("path = %q", r.Path)
		}
		if r.Header.Get("x-api-key") != "key" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version")
		}
		return providersim.CallTools(providersim.ToolCall{ID: "toolu_1", Name: "read_file", Arguments: `{"path":"a.go"}`}).WithUsage(providersim.Usage{Input: 12, Output: 7, Cached: 2})
	})

	p := provider.FromPreset("anthropic", "key", server.BaseURL(providersim.Messages))
	p.Model = "claude-test"
	agent := newTestAgent(t, p, p.Model)

	var startedID, startedName string
	result, err := agent.completeStream(
		context.Background(),
		[]provider.ChatMessage{provider.NewTextMessage("user", "hello")},
		nil, nil, nil,
		func(_ string, id string, name string) {
			startedID, startedName = id, name
		},
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	call := result.ToolCalls[0]
	if call.ID != "toolu_1" || call.Function.Name != "read_file" ||
		call.Function.Arguments != `{"path":"a.go"}` {
		t.Fatalf("tool call = %+v", call)
	}
	if startedID != "toolu_1" || startedName != "read_file" {
		t.Fatalf("start callback id=%q name=%q", startedID, startedName)
	}
	if result.Usage.PromptTokens != 12 || result.Usage.CompletionTokens != 7 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

func TestCompleteStreamOpenAIResponsesPreservesReasoningItem(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if r.Path != "/v1/responses" {
			t.Errorf("path = %q", r.Path)
		}
		return providersim.Text("answer").WithReasoning("Checking.", "", "")
	})

	p := provider.FromPreset("openai", "key", server.BaseURL(providersim.Responses))
	p.Model = "gpt-test"
	agent := newTestAgent(t, p, p.Model)

	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "answer" || len(result.ProviderItems) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompleteStreamRetriesRetryableStatus(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterSeconds(0)), providersim.Text("ok"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	agent.MaxRetries = 2

	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "ok" || len(server.Requests()) != 2 {
		t.Fatalf("result=%+v calls=%d", result, len(server.Requests()))
	}
}

func TestCompleteStreamAccumulatesUsageWithinOneStream(t *testing.T) {
	var calls atomic.Int32
	server := providersim.New(t)
	server.Respond(func(r providersim.Request) providersim.Turn {
		if calls.Add(1) != 1 {
			t.Errorf("unexpected extra request: stream retries must not happen after output")
		}
		return providersim.Text("ok").WithUsage(providersim.Usage{Input: 3, Output: 1})
	})

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)

	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "ok" || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	if result.Usage.PromptTokens != 3 || result.Usage.CompletionTokens != 1 {
		t.Fatalf("aggregate usage = %+v", result.Usage)
	}
	if result.LastPromptTokens != 3 {
		t.Fatalf("last prompt tokens = %d", result.LastPromptTokens)
	}
}

func TestCompleteStreamSurfacesSSEError(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.StreamError(0, providersim.InvalidRequest).WithFaultMessage("bad input"))

	p := provider.FromPreset("anthropic", "key", server.BaseURL(providersim.Messages))
	p.Model = "claude-test"
	agent := newTestAgent(t, p, p.Model)

	_, err := completeForTest(t, agent)
	if err == nil || !strings.Contains(err.Error(), "bad input") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompleteStreamAcceptsNonStreamingJSONFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"choices":[{"message":{"role":"assistant","content":"fallback"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1}
		}`)
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "fallback" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompleteStreamSniffsJSONFallbackWithWrongContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, `{
			"choices":[{"message":{"role":"assistant","content":"fallback"},"finish_reason":"stop"}]
		}`)
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "fallback" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompleteStreamReportsAbruptEOFWithPartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	result, err := completeForTest(t, agent)
	if err == nil {
		t.Fatal("completeStream succeeded after an unterminated stream")
	}
	var apiErr *apiCallError
	if !errors.As(err, &apiErr) || !apiErr.Partial {
		t.Fatalf("error = %#v, want partial apiCallError", err)
	}
	if result.Content != "partial" {
		t.Fatalf("partial content = %q", result.Content)
	}
}

func TestCompleteStreamPreservesUsageOnPartialError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"+
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2,\"prompt_tokens_details\":{\"cached_tokens\":3,\"cache_write_tokens\":1}}}\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	agent := newTestAgent(t, p, p.Model)
	result, err := completeForTest(t, agent)
	if err == nil {
		t.Fatal("completeStream succeeded after an unterminated stream")
	}
	if result.Usage.PromptTokens != 7 || result.Usage.CompletionTokens != 2 ||
		result.Usage.PromptTokensCached != 3 || result.Usage.PromptTokensWritten != 1 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

func TestCompleteStreamWorksWithoutLogger(t *testing.T) {
	server := providersim.New(t)
	server.Script(providersim.Text("ok"))

	p := provider.FromPreset("openai-chat", "key", server.BaseURL(providersim.Chat))
	p.Model = "test-model"
	agent := NewAgent(p, p.Model, nil)
	agent.MaxRetries = 1
	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("content = %q", result.Content)
	}
}
