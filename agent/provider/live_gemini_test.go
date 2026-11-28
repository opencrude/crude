package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// Live Gemini API tests. They need CRUDE_LIVE_GEMINI_KEY (an AI Studio key)
// and are skipped otherwise. CRUDE_LIVE_GEMINI_MODEL selects the chat model.

func liveGeminiProvider(t *testing.T, protocol string) (*Provider, string) {
	t.Helper()
	key := os.Getenv("CRUDE_LIVE_GEMINI_KEY")
	if key == "" {
		t.Skip("CRUDE_LIVE_GEMINI_KEY not set")
	}
	model := os.Getenv("CRUDE_LIVE_GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.5-flash"
	}
	p := FromPreset("google", key, "")
	if err := p.SetProtocolID(protocol); err != nil {
		t.Fatal(err)
	}
	p.Model = model
	p.SetDialSettings(DialSettings{MaxAttempts: 4})
	return p, model
}

var liveGeminiTools = []ToolDef{
	{Type: "function", Function: FunctionDef{Name: "read_file", Description: "Read a file from disk.",
		Parameters: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"path":{"type":"string"},"mode":{"const":"text"},"limit":{"type":["integer","null"],"minimum":1}},"required":["path","missing"],"additionalProperties":false}`)}},
	{Type: "function", Function: FunctionDef{Name: "get_weather", Description: "Get the weather for one city.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"},"units":{"anyOf":[{"type":"string","enum":["c","f"]},{"type":"null"}]},"tags":{"type":"array","items":{"type":"string"},"uniqueItems":true}},"required":["city"]}`)}},
}

// liveTurn drains one stream into an assistant message the way the agent
// accumulates it.
func liveTurn(t *testing.T, p *Provider, model string, messages []ChatMessage, tools []ToolDef) (ChatMessage, string, *TokenUsage) {
	t.Helper()
	client, err := p.Client(OnRequestEncoded(func(body []byte) {
		t.Logf("request %d bytes: %s", len(body), truncate(string(body), 300))
	}), OnRetry(func(attempt int, delay time.Duration, err error) {
		t.Logf("retry %d after %v: %v", attempt, delay, err)
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	stream, err := client.Stream(ctx, &ChatRequest{Model: model, Messages: messages, Tools: tools, MaxOutputTokens: 4096})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	var content, thoughts strings.Builder
	calls := map[int]*ToolCall{}
	message := ChatMessage{Role: "assistant"}
	var finish string
	var usage *TokenUsage
	for {
		update, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		content.WriteString(update.ContentDelta)
		thoughts.WriteString(update.ReasoningText())
		for _, delta := range update.ToolCalls {
			call := calls[delta.Index]
			if call == nil {
				call = &ToolCall{Index: delta.Index, Type: "function"}
				calls[delta.Index] = call
			}
			if delta.ID != "" {
				call.ID = delta.ID
			}
			if delta.Name != "" {
				call.Function.Name = delta.Name
			}
			if delta.ReplaceArguments {
				call.Function.Arguments = delta.Arguments
			} else {
				call.Function.Arguments += delta.Arguments
			}
		}
		message.ProviderItems = append(message.ProviderItems, update.ProviderItems...)
		if update.FinishReason != "" {
			finish = update.FinishReason
		}
		if update.Usage != nil {
			usage = update.Usage
		}
	}
	for _, index := range slices.Sorted(func(yield func(int) bool) {
		for index := range calls {
			if !yield(index) {
				return
			}
		}
	}) {
		call := *calls[index]
		if call.ID == "" {
			call.ID = "call-local-" + call.Function.Name
		}
		message.ToolCalls = append(message.ToolCalls, call)
	}
	message.Content = TextContent(content.String())
	t.Logf("finish=%s thoughts=%d chars content=%q calls=%d items=%d usage=%+v", finish, thoughts.Len(), truncate(content.String(), 120), len(message.ToolCalls), len(message.ProviderItems), usage)
	return message, finish, usage
}

func liveToolResults(calls []ToolCall) []ChatMessage {
	var results []ChatMessage
	for _, call := range calls {
		output := "Sunny, 21 C"
		if call.Function.Name == "read_file" {
			output = "test-host\n"
		}
		results = append(results, ChatMessage{Role: "tool", ToolCallID: call.ID, Content: TextContent(output)})
	}
	return results
}

func testLiveGeminiToolRoundTrip(t *testing.T, protocol string) {
	p, model := liveGeminiProvider(t, protocol)
	messages := []ChatMessage{
		NewTextMessage("system", "You are a coding agent. Call independent tools in parallel."),
		NewTextMessage("user", "Read /etc/hostname and get the weather in Paris. Call both tools now, in one turn."),
	}
	reply, finish, usage := liveTurn(t, p, model, messages, liveGeminiTools)
	if finish != "tool_calls" || len(reply.ToolCalls) < 2 {
		t.Fatalf("finish=%q calls=%+v, want two parallel tool calls", finish, reply.ToolCalls)
	}
	if len(reply.ProviderItems) == 0 {
		t.Fatal("model turn has no provider items to replay")
	}
	if usage == nil || usage.PromptTokens == 0 || !usage.CacheReadKnown {
		t.Fatalf("usage = %+v", usage)
	}
	history := append(append(slices.Clone(messages), reply), liveToolResults(reply.ToolCalls)...)

	// Exact replay keeps Gemini's own signatures.
	answer, finish, _ := liveTurn(t, p, model, history, liveGeminiTools)
	if finish != "stop" || !strings.Contains(strings.ToLower(answer.ContentString()), "test-host") {
		t.Fatalf("exact replay: finish=%q answer=%q", finish, answer.ContentString())
	}

	// A turn Crude cannot replay (another provider, compaction) is rebuilt
	// with the documented skip signature.
	rebuilt := slices.Clone(history)
	rebuilt[2].ProviderItems = nil
	answer, finish, _ = liveTurn(t, p, model, rebuilt, liveGeminiTools)
	if finish != "stop" || answer.ContentString() == "" {
		t.Fatalf("rebuilt replay: finish=%q answer=%q", finish, answer.ContentString())
	}
}

func TestLiveGeminiToolRoundTrip(t *testing.T) { testLiveGeminiToolRoundTrip(t, "gemini") }

func TestLiveGeminiInteractionsToolRoundTrip(t *testing.T) {
	testLiveGeminiToolRoundTrip(t, "gemini-interactions")
}

// TestLiveGeminiThinkingLevels asks for levels some models reject; Crude
// learns the rejection and sends the nearest level the model accepts.
func TestLiveGeminiThinkingLevels(t *testing.T) {
	for _, protocol := range []string{"gemini", "gemini-interactions"} {
		for _, effort := range []string{"max", "none", "low"} {
			t.Run(protocol+"/"+effort, func(t *testing.T) {
				p, model := liveGeminiProvider(t, protocol)
				p.SetThinkingEffort(effort)
				reply, finish, _ := liveTurn(t, p, model, []ChatMessage{NewTextMessage("user", "Reply with the word ok.")}, nil)
				if finish != "stop" || reply.ContentString() == "" {
					t.Fatalf("finish=%q reply=%q", finish, reply.ContentString())
				}
			})
		}
	}
}

func TestLiveGeminiListAndComplete(t *testing.T) {
	p, model := liveGeminiProvider(t, "gemini")
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	var chat, decision bool
	for _, info := range models {
		if info.ID == model {
			chat = info.ContextWindow > 0 && info.MaxOutput > 0
		}
		decision = decision || info.Decision
	}
	if !chat || !decision {
		t.Fatalf("model list lacks %s limits or a decision model (%d models)", model, len(models))
	}
	response, err := client.Complete(t.Context(), &ChatRequest{Model: model, Messages: []ChatMessage{NewTextMessage("user", "Reply with the word ok.")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := response.Choices[0]; got.FinishReason != "stop" || got.Message.ContentString() == "" {
		t.Fatalf("Complete = %+v", got)
	}
}

func TestLiveGeminiDecision(t *testing.T) {
	p, _ := liveGeminiProvider(t, "gemini")
	p.Model = "gemini-decision-1"
	answers, err := p.Decide(t.Context(), "The user asked to fix a failing Go test in the provider package.", map[string]DecisionQuestion{
		"code": {Type: "noul", Instructions: "Is this request about software development?"},
		"area": {Type: "choice", Instructions: "Which area is it about?", Criteria: map[string]string{"backend": "Server code", "ui": "User interface"}},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers["code"].Noul < 0.5 || answers["area"].Choice != "backend" {
		t.Fatalf("answers = %+v", answers)
	}
}
