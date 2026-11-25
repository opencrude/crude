package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/internal/providersim"
)

func TestThresholdResponsesOrderAndBinding(t *testing.T) {
	p := &Provider{ID: "custom", StandardName: "openai-responses", Model: "model", AccountID: "a", BaseURL: "https://example.test"}
	d := p.Protocol().NewStreamDecoder()
	objs := []json.RawMessage{json.RawMessage(`{"type":"message","id":"earlier","role":"assistant","content":[{"type":"output_text","text":"answered"}]}`), json.RawMessage(`{"type":"compaction","id":"seal","encrypted_content":"exact"}`), json.RawMessage(`{"type":"configuration_update","id":"cfg","updates":[{"opaque":987654321123456789}]}`), json.RawMessage(`{"type":"reasoning","id":"r","encrypted_content":"opaque"}`), json.RawMessage(`{"type":"function_call","call_id":"function","id":"fc","name":"test","arguments":"{}"}`)}
	for _, index := range []int{2, 1, 4, 0, 3} {
		raw, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": index, "item": objs[index]})
		if _, e := d.Decode(StreamEvent{Data: raw}); e != nil {
			t.Fatal(e)
		}
	}
	raw, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "response", "status": "completed", "output": objs}})
	u, e := d.Decode(StreamEvent{Data: raw})
	if e != nil || !u.ReplaceProviderItems || len(u.ProviderItems) != len(objs) {
		t.Fatalf("terminal output=%v %v", u, e)
	}
	for i, it := range u.ProviderItems {
		if !bytes.Equal(it.Data, objs[i]) {
			t.Fatalf("out-of-order terminal item %d", i)
		}
	}
	msgs := []ChatMessage{NewTextMessage("system", "system"), NewTextMessage("user", "original history")}
	prefix := MessagePrefixHash(msgs)
	msg := NewTextMessage("assistant", "answered")
	msg.ProviderItems = StampProviderItems(u.ProviderItems, p, p.Model, prefix)
	msg.ReplayPrefixHash, msg.ReplayScope, msg.ReplaySourceModel = prefix, p.ReplayScope(), p.Model
	msg.ToolCalls = []ToolCall{{ID: "function", Type: "function", Function: FunctionCall{Name: "test", Arguments: "{}"}}}
	msgs = append(msgs, msg, ChatMessage{Role: "tool", ToolCallID: "function", Content: TextContent("actual result")})
	receipt := &ThresholdReceipt{index: 2, scope: p.compactionScope(p.Model), prefix: prefix, seal: objs[1]}
	persisted, _ := json.Marshal(msgs)
	var hydrated []ChatMessage
	if e := json.Unmarshal(persisted, &hydrated); e != nil {
		t.Fatal(e)
	}
	window, e := receipt.window(p, hydrated)
	if e != nil || bytes.Contains(window.raw, []byte("answered")) || !bytes.Contains(window.raw, []byte("987654321123456789")) || !bytes.Contains(window.raw, []byte("actual result")) {
		t.Fatalf("wrong suffix: %s; %v", window.raw, e)
	}
	compact, _ := encodeResponsesInput(FilterReplayMessages(hydrated, p), 0)
	sent, _ := json.Marshal(compact)
	if bytes.Contains(sent, []byte("original history")) || bytes.Count(sent, []byte(`"type":"compaction"`)) != 1 {
		t.Fatalf("stateless resume: %s", sent)
	}
	p.AccountID = "b"
	projected := FilterReplayMessages(hydrated, p)
	if len(projected[2].ProviderItems) != 0 || projected[2].ContentString() != "answered" || !reflect.DeepEqual(hydrated[2].ToolCalls, projected[2].ToolCalls) {
		t.Fatal("foreign route retained ciphertext or lost ordinary output")
	}
	if _, e := receipt.window(p, hydrated); e == nil {
		t.Fatal("foreign route exported the window")
	}
	p.AccountID = "a"
	hydrated[0] = NewTextMessage("system", "changed")
	if _, e := receipt.window(p, hydrated); e == nil {
		t.Fatal("changed system prefix exported the window")
	}
	hydrated[0] = msgs[0]
	hydrated[3].Content = TextContent(strings.Repeat("x", maxNativeWindowBytes))
	if _, e := receipt.window(p, hydrated); e == nil {
		t.Fatal("oversized suffix accepted")
	}
}

func TestThresholdPaidGuardAfterStreamFault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  providersim.Turn
		rejected bool
	}{{"http", providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterSeconds(120)), true}, {"first_event", providersim.StreamError(0, providersim.RateLimit), true}, {"later_event", providersim.StreamError(2, providersim.RateLimit), false}} {
		t.Run(tc.name, func(t *testing.T) {
			sim, p, r := nativeRateFixture(t, providersim.Messages, CompactionProviderOnly)
			key := p.compactionScope(p.Model)
			cache := currentSupport()
			f, _ := cache.begin(t.Context(), key, p.AccountID)
			if e := cache.record(key, f, p, p.Model, supportVerified, thresholdMethod); e != nil {
				t.Fatal(e)
			}
			cache.end(key, f)
			sim.Script(tc.failure)
			sends := 0
			r.Boundary = func(ctx context.Context, option ClientOption) (BoundaryReply, error) {
				sends++
				c, e := p.Client(option)
				if e != nil {
					return BoundaryReply{}, e
				}
				stream, e := c.Stream(ctx, &ChatRequest{Model: r.Model, Messages: r.Messages, MaxOutputTokens: r.Limit})
				if e != nil {
					return BoundaryReply{}, e
				}
				defer stream.Close()
				for {
					_, e := stream.Next()
					if e != nil {
						return BoundaryReply{}, e
					}
				}
			}
			r.InputTokens = 60000
			_, e := p.NativeCheckpoint(t.Context(), r)
			if e == nil || Classify(e) != FailureTransient || sends != 1 {
				t.Fatalf("wrong recovery: %v, sends=%d", e, sends)
			}
			k := attemptKey{key, checkpointSource(r.Owner, r.Messages), p.AccountID}
			cache.mu.Lock()
			held := cache.attempts[k] > time.Now().Unix()
			fact := cache.facts[key]
			cache.mu.Unlock()
			if held == tc.rejected || fact.state == supportUnavailable || fact.retryAt <= time.Now().Unix() {
				t.Fatalf("guard=%t state=%+v", held, fact)
			}
			if tc.name == "http" && fact.retryAt < time.Now().Unix()+115 {
				t.Fatal("ignored server cooldown")
			}
			if !tc.rejected {
				expireNativeCooldown(p)
				got, e := p.NativeCheckpoint(t.Context(), r)
				if e != nil || got.Fallback == "" || sends != 1 {
					t.Fatal("resent a possibly paid ordinary request")
				}
			}
		})
	}
}

func TestThresholdAccountEditsKeepCallerOptions(t *testing.T) {
	for _, tc := range []struct{ name, wire, config, ordinary string }{
		{"omitted", "openai-responses", `[{"type":"compaction"}]`, ``},
		{"null_and_siblings", "openai-responses", `[{"type":"first","x":true},{"type":"compaction","compact_threshold":null,"route_field":3},{"type":"last"}]`, `[{"type":"first","x":true},{"type":"last"}]`},
		{"messages_and_siblings", "anthropic", `{"caller_option":1,"edits":[{"type":"clear_tool_uses_20250919"},{"type":"compact_20260112","trigger":{"type":"input_tokens","value":75000},"instructions":"Keep blue.","pause_after_compaction":false},{"type":"clear_thinking_20251015"}]}`, `{"caller_option":1,"edits":[{"type":"clear_tool_uses_20250919"},{"type":"clear_thinking_20251015"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{ID: "custom", StandardName: tc.wire, Model: "model", BaseURL: "https://example.test"}
			var value any
			if e := json.Unmarshal([]byte(tc.config), &value); e != nil {
				t.Fatal(e)
			}
			p.SetExtraBody(map[string]any{"context_management": value})
			x := compactExchange{provider: p, request: NativeCheckpointRequest{Directive: "Write sections."}}
			encode := func(opts ...ClientOption) json.RawMessage {
				c, e := p.Client(opts...)
				if e != nil {
					t.Fatal(e)
				}
				raw, e := c.encodeChatRequest(&ChatRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("user", "test")}})
				if e != nil {
					t.Fatal(e)
				}
				var fields map[string]json.RawMessage
				if e := json.Unmarshal(raw, &fields); e != nil {
					t.Fatal(e)
				}
				return fields["context_management"]
			}
			equivalent := func(got json.RawMessage, want string) {
				t.Helper()
				if want == "" {
					if len(got) > 0 {
						t.Fatalf("unexpected edit: %s", got)
					}
					return
				}
				var a, b any
				if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil || !reflect.DeepEqual(a, b) {
					t.Fatalf("got %s; want %s", got, want)
				}
			}
			equivalent(encode(), tc.ordinary)
			want := strings.ReplaceAll(tc.config, `"pause_after_compaction":false`, `"pause_after_compaction":true`)
			want = strings.ReplaceAll(want, `"Keep blue."`, `"Keep blue.\n\nWrite sections."`)
			equivalent(encode(x.boundaryOption()), want)
			equivalent(encode(), tc.ordinary)
			if edits := p.configuredThreshold(""); edits == nil || !edits.declared {
				t.Fatal("valid configured route lost its evidence")
			}
		})
	}
}

func TestThresholdPrefersVerifiedOnDemand(t *testing.T) {
	sim, p, r := nativeRateFixture(t, providersim.Responses, CompactionProviderOnly)
	p.SetExtraBody(map[string]any{"context_management": []any{map[string]any{"type": "compaction", "compact_threshold": nil}}})
	r.Boundary = func(context.Context, ClientOption) (BoundaryReply, error) {
		t.Fatal("on-demand route was available")
		return BoundaryReply{}, nil
	}
	// Discover on the first boundary, then reuse that operation on a new prefix.
	sim.Script(providersim.Turn{}, providersim.Text(r.ProbeAnswer), providersim.Turn{}, providersim.Text("verified summary"), providersim.Turn{}, providersim.Text("verified summary"))
	for range 2 {
		got, e := p.NativeCheckpoint(t.Context(), r)
		if e != nil || got.Fallback != "" || len(got.Message.ProviderItems) != 1 {
			t.Fatalf("on-demand checkpoint: %s, %v", got.Fallback, e)
		}
		r.Messages = append(r.Messages, NewTextMessage("user", "new input"))
	}
	reqs := sim.Requests()
	if len(reqs) != 6 || !strings.HasSuffix(reqs[4].Path, "/compact") {
		t.Fatal("verified route was not reused")
	}
	for _, req := range reqs {
		if bytes.Contains(req.Raw, []byte("context_management")) {
			t.Fatal("on-demand or read-back request enabled threshold scheduling")
		}
	}
}
