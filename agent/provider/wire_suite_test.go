package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

type wireProfile struct {
	wire            providersim.Wire
	standard, model string
	// Number of simulator frames through the first text delta, before the
	// terminal event. Error and cut tests must keep this accepted output.
	textFrame int
}

var wireProfiles = []wireProfile{
	{providersim.Chat, "openai", "chat-model", 2},
	{providersim.Responses, "openai-responses", "responses-model", 5},
	{providersim.Codex, "codex", "codex-model", 5},
	{providersim.Messages, "anthropic", "claude-sonnet-4-6", 4},
	{providersim.Gemini, "gemini", "gemini-3.8-flash", 1},
	{providersim.Interactions, "gemini-interactions", "gemini-3.8-flash", 3},
	{providersim.Ollama, "ollama", "ollama-model", 1},
}

// Each exception is a wire capability, not a list of currently passing clients.
// Keep these visible in `go test -v`, including the exact retry-hint envelope.
func TestWireSuite(t *testing.T) {
	type behavior struct {
		name string
		run  func(*testing.T, wireProfile)
		na   map[providersim.Wire]string
	}
	const noRPC = "The wire has no google.rpc.RetryInfo error detail."
	const noID = "The wire has no previous_response_id continuation."
	const noCompact = "The wire has no native conversation compaction operation."
	const relativeRPC = "The Gemini simulator emits relative 429 hints in RetryInfo, not a header."
	cases := []behavior{
		{"text_stream", textStream, nil},
		{"text_complete", textComplete, map[providersim.Wire]string{providersim.Codex: "Codex serves only streams over HTTP."}},
		{"tool_result", func(t *testing.T, w wireProfile) { toolRoundTrip(t, w, false) }, nil},
		{"reasoning_replay", func(t *testing.T, w wireProfile) { toolRoundTrip(t, w, true) }, nil},
		{"usage", cacheUsage, nil},
		{"prefix", growingPrefix, nil},
		{"context_overflow", contextOverflow, nil},
		{"retry_seconds", retryCase("seconds"), map[providersim.Wire]string{providersim.Gemini: relativeRPC}},
		{"retry_date", retryCase("date"), nil},
		{"retry_ms", retryCase("ms"), map[providersim.Wire]string{providersim.Gemini: relativeRPC}},
		{"retry_info", retryCase("info"), map[providersim.Wire]string{
			providersim.Chat: noRPC, providersim.Responses: noRPC, providersim.Codex: noRPC,
			providersim.Messages: noRPC, providersim.Ollama: noRPC,
			providersim.Interactions: "Interactions uses a string error code, not google.rpc.Status.",
		}},
		{"stream_error", func(t *testing.T, w wireProfile) { partialStream(t, w, false) }, nil},
		{"stream_cut", func(t *testing.T, w wireProfile) { partialStream(t, w, true) }, nil},
		{"previous_response_id", storedContinuation, map[providersim.Wire]string{
			providersim.Chat: noID, providersim.Messages: noID, providersim.Gemini: noID, providersim.Ollama: noID,
			providersim.Codex:        "Codex HTTP requires store:false.",
			providersim.Interactions: "Interactions uses previous_interaction_id, not previous_response_id.",
		}},
		{"native_checkpoint", nativeCheckpoint, map[providersim.Wire]string{
			providersim.Chat: noCompact, providersim.Gemini: noCompact,
			providersim.Interactions: noCompact, providersim.Ollama: noCompact,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range wireProfiles {
				t.Run(string(w.wire), func(t *testing.T) {
					if why := tc.na[w.wire]; why != "" {
						t.Skip(why)
					}
					t.Parallel() // Independent servers; actual HTTP retry waits overlap.
					tc.run(t, w)
				})
			}
		})
	}
}

type wireFixture struct {
	wireProfile
	sim *providersim.Server
	p   *provider.Provider
}

func fixture(t *testing.T, w wireProfile, opts ...providersim.Option) *wireFixture {
	t.Helper()
	s := providersim.New(t, opts...)
	p := &provider.Provider{ID: "custom", AccountID: t.Name(), StandardName: w.standard, Model: w.model, APIKey: "fixture", BaseURL: s.BaseURL(w.wire)}
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1, RequestTimeoutSeconds: 10})
	return &wireFixture{w, s, p}
}

func (f *wireFixture) client(t *testing.T, opts ...provider.ClientOption) *provider.Client {
	t.Helper()
	c, err := f.p.Client(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *wireFixture) request() *provider.ChatRequest {
	return &provider.ChatRequest{
		Model: f.model, MaxOutputTokens: 2048,
		Messages: []provider.ChatMessage{
			provider.NewTextMessage("system", "Keep the answer short."),
			provider.NewTextMessage("user", "Find the ticket."),
		},
	}
}

func (f *wireFixture) last(t *testing.T, count int) providersim.Request {
	t.Helper()
	r, ok := f.sim.Last()
	if !ok || r.Index != count || r.Wire != f.wire || r.Violation != "" {
		t.Fatalf("want %d accepted %s requests, last #%d %s: %s", count, f.wire, r.Index, r.Wire, r.Violation)
	}
	return r
}

type wireReply struct {
	text, trace, finish string
	usage               *provider.TokenUsage
	items               []provider.Item
	calls               []provider.ToolCall
}

// Consume only the public Client stream. Build the next request from actual
// output; never synthesize a signed item, generated call, or completion flag.
func readStream(ctx context.Context, c *provider.Client, q *provider.ChatRequest) (r wireReply, err error) {
	s, err := c.Stream(ctx, q)
	if err != nil {
		return r, err
	}
	defer s.Close()
	for {
		u, err := s.Next()
		if errors.Is(err, io.EOF) {
			return r, nil
		}
		if err != nil {
			return r, err
		}
		r.text += u.ContentDelta
		r.trace += u.ReasoningText()
		r.items = append(r.items, u.ProviderItems...)
		if u.FinishReason != "" {
			r.finish = u.FinishReason
		}
		if u.Usage != nil {
			r.usage = u.Usage
		}
		for _, d := range u.ToolCalls {
			i := slices.IndexFunc(r.calls, func(c provider.ToolCall) bool { return c.Index == d.Index })
			if i < 0 {
				i = len(r.calls)
				r.calls = append(r.calls, provider.ToolCall{Index: d.Index})
			}
			call := &r.calls[i]
			if d.ID != "" {
				call.ID = d.ID
			}
			if d.Type != "" {
				call.Type = d.Type
			}
			if d.Name != "" {
				call.Function.Name = d.Name
			}
			if d.ReplaceArguments {
				call.Function.Arguments = d.Arguments
			} else {
				call.Function.Arguments += d.Arguments
			}
		}
	}
}

func (f *wireFixture) stream(t *testing.T, q *provider.ChatRequest) wireReply {
	t.Helper()
	r, err := readStream(t.Context(), f.client(t), q)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r wireReply) assistant(p *provider.Provider, history []provider.ChatMessage) provider.ChatMessage {
	m := provider.NewTextMessage("assistant", r.text)
	m.ReasoningContent = r.trace
	m.ToolCalls = r.calls
	m.ReplayScope = p.ReplayScope()
	m.ReplaySourceModel = p.Model
	m.ReplayPrefixHash = provider.MessagePrefixHash(history)
	m.ProviderItems = provider.StampProviderItems(provider.CanonicalizeProviderItems(r.items), p, p.Model, m.ReplayPrefixHash)
	return m
}

func parts(c providersim.Conversation, kind providersim.PartKind) []providersim.Part {
	var out []providersim.Part
	for _, m := range c.Messages {
		for _, p := range m.Parts {
			if p.Kind == kind {
				out = append(out, p)
			}
		}
	}
	return out
}

func dialog(c providersim.Conversation) string {
	var texts []string
	for _, p := range parts(c, providersim.PartText) {
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, "\n")
}

func lookupTool() provider.ToolDef {
	return provider.ToolDef{Type: "function", Function: provider.FunctionDef{Name: "lookup", Description: "Read a ticket.", Parameters: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`)}}
}
