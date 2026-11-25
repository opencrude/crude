package provider_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/internal/providersim"
)

func textStream(t *testing.T, w wireProfile) {
	const want = "ticket 17\n✓"
	f := fixture(t, w)
	gate := providersim.NewGate()
	f.sim.Script(providersim.Text(want).WithGate(gate))
	// The body cannot arrive until Crude observes the successful HTTP response.
	r, err := readStream(t.Context(), f.client(t, provider.OnRequestAccepted(gate.Release)), f.request())
	if err != nil || r.text != want || r.finish == "" {
		t.Fatalf("stream text=%q finish=%q err=%v", r.text, r.finish, err)
	}
	// Crude charges and sizes context from usage. A caller must not have to
	// ask for it; the request here sets no stream options.
	if r.usage == nil || r.usage.PromptTokens <= 0 {
		t.Errorf("stream reported no usage: %+v", r.usage)
	}
	if req := f.last(t, 1); !req.Stream || dialog(req.Effective) != "Find the ticket." {
		t.Fatalf("wrong stream request: %s", req.Raw)
	}
}

func textComplete(t *testing.T, w wireProfile) {
	const want = "ticket 17\n✓"
	f := fixture(t, w)
	f.sim.Script(providersim.Text(want))
	r, err := f.client(t).Complete(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Choices) != 1 || r.Choices[0].Message.ContentString() != want || r.Choices[0].FinishReason == "" {
		t.Fatalf("completion choices: %+v", r.Choices)
	}
	if req := f.last(t, 1); req.Stream || dialog(req.Effective) != "Find the ticket." {
		t.Fatalf("wrong completion request: %s", req.Raw)
	}
}

func toolRoundTrip(t *testing.T, w wireProfile, reasoning bool) {
	const trace, signature, ciphertext, answer = "Choose the index.", "c2lnbmVk", "c2VhbGVk", "ticket-17"
	call := providersim.ToolCall{ID: "call_lookup", Name: "lookup", Arguments: `{"key":"index"}`}
	turn := providersim.CallTools(call)
	var opts []providersim.Option
	if reasoning {
		turn = turn.WithReasoning(trace, signature, ciphertext)
		// Chat itself has no public reasoning item. DeepSeek documents
		// reasoning_content replay for Chat tool rounds.
		if w.wire == providersim.Chat {
			opts = append(opts, providersim.WithChatGateway(w.model, providersim.DeepSeek))
		}
	}
	f := fixture(t, w, opts...)
	if reasoning {
		f.p.SetThinkingEffort("high")
		if w.wire == providersim.Responses {
			// Ask for the encrypted item, just as a stateless account must.
			f.p.SetProtocolSettings(&provider.ProtocolSettings{ZeroDataRetention: true})
		}
	}
	f.sim.Script(turn, providersim.Text("found"))
	q := f.request()
	q.Tools = []provider.ToolDef{lookupTool()}
	first := f.stream(t, q)
	if reasoning && first.trace != trace {
		t.Errorf("streamed reasoning = %q, want %q", first.trace, trace)
	}
	if len(first.calls) != 1 || first.calls[0].ID != call.ID || first.calls[0].Function.Name != call.Name || first.calls[0].Function.Arguments != call.Arguments {
		t.Fatalf("decoded calls %+v, want %+v", first.calls, call)
	}
	result := provider.NewTextMessage("tool", answer)
	result.ToolCallID = first.calls[0].ID
	q.Messages = provider.FilterReplayMessages(append(q.Messages, first.assistant(f.p, q.Messages), result), f.p)
	if reply := f.stream(t, q); reply.text != "found" {
		t.Fatalf("tool continuation = %q", reply.text)
	}
	r := f.last(t, 2)
	calls, results := parts(r.Effective, providersim.PartToolCall), parts(r.Effective, providersim.PartToolResult)
	if len(r.Tools) != 1 || r.Tools[0].Name != call.Name || len(calls) != 1 || calls[0].Call != call || len(results) != 1 || results[0].CallID != call.ID {
		t.Fatalf("replayed tools/calls/results: %+v / %+v / %+v", r.Tools, calls, results)
	}
	// google.ai.generativelanguage.v1beta.FunctionResponse allows any result
	// key. What the model must see is the exact output, not a particular key.
	got := results[0].Text
	if w.wire == providersim.Gemini {
		var result map[string]string
		if err := json.Unmarshal([]byte(got), &result); err != nil || len(result) != 1 {
			t.Fatalf("expected a JSON object carrying the tool output, got %q", got)
		}
		for _, text := range result {
			got = text
		}
	}
	if got != answer {
		t.Errorf("replayed tool result = %q, want %q", got, answer)
	}
	if !reasoning {
		return
	}
	replay := parts(r.Effective, providersim.PartReasoning)
	if len(replay) == 0 || replay[0].Text != trace {
		t.Fatalf("reasoning replay: %+v, want %q", replay, trace)
	}
	switch w.wire {
	case providersim.Messages:
		if len(replay) != 2 || replay[0].Signature != signature || replay[1].Encrypted != ciphertext {
			t.Errorf("signed and redacted thinking changed: %+v", replay)
		}
	case providersim.Responses, providersim.Codex:
		if len(replay) != 1 || replay[0].Encrypted != ciphertext {
			t.Errorf("encrypted reasoning changed: %+v", replay)
		}
	case providersim.Gemini:
		if calls[0].Signature != signature {
			t.Errorf("first function call signature = %q, want %q", calls[0].Signature, signature)
		}
	case providersim.Interactions:
		if len(replay) != 1 || replay[0].Signature != signature {
			t.Errorf("thought signature changed: %+v", replay)
		}
	}
}

func cacheUsage(t *testing.T, w wireProfile) {
	f := fixture(t, w, providersim.WithCacheProfile(w.wire, w.model, providersim.CacheProfile{Minimum: 1, Granularity: 1}))
	f.sim.Respond(func(providersim.Request) providersim.Turn { return providersim.Text("done") })
	q := f.request()
	mark := &provider.CacheControl{Type: "ephemeral"}
	if w.wire == providersim.Messages {
		q.Messages[len(q.Messages)-1].CacheControl = mark
	}
	writeKnown := w.wire == providersim.Messages || w.wire == providersim.Responses || w.wire == providersim.Codex
	for i, label := range []string{"first", "identical", "appended"} {
		r := f.stream(t, q)
		prefix := f.last(t, i+1).Prefix
		written := 0
		if writeKnown {
			written = prefix.CacheWriteTokens
		}
		if prefix.InputTokens <= 0 || (i == 0 && prefix.CachedTokens != 0) ||
			(i == 1 && prefix.CachedTokens != prefix.InputTokens) ||
			(i == 2 && (prefix.CachedTokens == 0 || prefix.CachedTokens >= prefix.InputTokens)) ||
			(writeKnown && (i == 1) != (written == 0)) {
			t.Fatalf("%s: cache fixture did not exercise a cold input, full hit and new tail: %+v", label, prefix)
		}
		if u := r.usage; u == nil || u.PromptTokens != prefix.InputTokens || u.CompletionTokens != 1 ||
			u.PromptTokensCached != prefix.CachedTokens || !u.CacheReadKnown ||
			u.PromptTokensWritten != written || u.CacheWriteKnown != writeKnown {
			t.Errorf("%s: normalized usage %+v; prefix %+v, writes reported=%t, one output token", label, u, prefix, writeKnown)
		}
		if i == 1 {
			next := provider.NewTextMessage("user", "Look again.")
			if w.wire == providersim.Messages {
				next.CacheControl = mark
			}
			q.Messages = append(q.Messages, r.assistant(f.p, q.Messages), next)
		}
	}
}

func growingPrefix(t *testing.T, w wireProfile) {
	f := fixture(t, w)
	f.sim.Script(providersim.Text("ticket 17"), providersim.Text("verified"))
	q := f.request()
	q.Tools = []provider.ToolDef{lookupTool()}
	first := f.stream(t, q)
	old := f.last(t, 1)
	q.Messages = provider.FilterReplayMessages(append(q.Messages, first.assistant(f.p, q.Messages), provider.NewTextMessage("user", "Check it.")), f.p)
	f.stream(t, q)
	r := f.last(t, 2)
	p := r.Prefix
	if p.PreviousIndex != old.Index || p.SharedTokens != old.Prefix.InputTokens || p.InputTokens <= p.SharedTokens || p.FirstDifference == "" {
		t.Fatalf("old prefix %+v; growing prefix %+v", old.Prefix, p)
	}
	// The first changed rendered block must be the appended assistant text
	// or its enclosing item, not one of the earlier messages or tool definitions.
	for _, part := range parts(r.Conversation, providersim.PartText) {
		if part.Text == "ticket 17" && (part.Path == p.FirstDifference || strings.HasPrefix(part.Path, p.FirstDifference+"/")) {
			return
		}
	}
	t.Fatalf("first difference %q is not the appended answer: %+v", p.FirstDifference, r.Messages)
}

func contextOverflow(t *testing.T, w wireProfile) {
	f := fixture(t, w)
	f.sim.Script(providersim.HTTPError(providersim.ContextOverflow))
	_, err := readStream(t.Context(), f.client(t), f.request())
	f.last(t, 1)
	if !provider.IsContextWindowExceeded(err) {
		t.Fatalf("expected context-window exceeded, got %v", err)
	}
}

// requestTimeout checks that HTTP 408 is temporary on every wire. RFC 9110
// section 15.5.9 defines it, and the OpenAI and Anthropic SDKs retry it.
func requestTimeout(t *testing.T, w wireProfile) {
	f := fixture(t, w)
	f.sim.Script(providersim.HTTPError(providersim.RequestTimeout), providersim.Text("recovered"))
	var callbacks int
	c := f.client(t, provider.WithMaxAttempts(2), provider.OnRetry(func(_ int, _ time.Duration, err error) {
		callbacks++
		var status *provider.StatusError
		if !errors.As(err, &status) || status.StatusCode != http.StatusRequestTimeout || provider.Classify(err) != provider.FailureTransient || !provider.Retryable(err) {
			t.Errorf("expected transient HTTP 408, got %v", err)
		}
	}))
	r, err := readStream(t.Context(), c, f.request())
	if err != nil || r.text != "recovered" || callbacks != 1 {
		t.Fatalf("expected one retry and success: callbacks=%d text=%q err=%v", callbacks, r.text, err)
	}
	f.last(t, 2)
}

func retryCase(format string) func(*testing.T, wireProfile) {
	return func(t *testing.T, w wireProfile) {
		f := fixture(t, w)
		var want time.Duration
		var hint providersim.RetryHint
		dates := make(chan time.Time, 1)
		switch format {
		case "date":
			// Start the HTTP-date clock at dispatch, after schema validation.
			f.sim.Respond(func(r providersim.Request) providersim.Turn {
				if r.Index != 1 {
					return providersim.Text("recovered")
				}
				date := time.Now().Truncate(time.Second).Add(2 * time.Second)
				dates <- date
				return providersim.HTTPError(providersim.RateLimit).WithRetryHint(providersim.RetryAfterDate(date))
			})
		case "seconds":
			want = time.Second
			hint = providersim.RetryAfterSeconds(1)
		case "ms", "info":
			want = 37 * time.Millisecond
			hint = providersim.RetryAfterMilliseconds(37)
		}
		if format != "date" {
			f.sim.Script(providersim.HTTPError(providersim.RateLimit).WithRetryHint(hint), providersim.Text("recovered"))
		}
		var callbacks int
		start := time.Now()
		c := f.client(t, provider.WithMaxAttempts(2), provider.OnRetry(func(attempt int, delay time.Duration, err error) {
			callbacks++
			var status *provider.StatusError
			if !errors.As(err, &status) || status.StatusCode != http.StatusTooManyRequests || provider.Classify(err) != provider.FailureTransient || !provider.Retryable(err) {
				t.Errorf("expected transient HTTP 429, got %v", err)
			}
			advice := provider.AdvisedRetryDelay(err)
			low, high := want, want
			if format == "date" {
				select {
				case date := <-dates:
					low, high = time.Until(date), date.Sub(start)
				default:
					t.Error("retried before the server sent an HTTP date")
					return
				}
			}
			if attempt != 1 || advice <= 0 || advice < low || advice > high || delay < advice {
				t.Errorf("retry %d: advised %s (want %s..%s), scheduled %s", attempt, advice, low, high, delay)
			}
		}))
		r, err := readStream(t.Context(), c, f.request())
		if err != nil || r.text != "recovered" || callbacks != 1 {
			t.Fatalf("expected one retry and success: callbacks=%d text=%q err=%v", callbacks, r.text, err)
		}
		f.last(t, 2)
	}
}

func partialStream(t *testing.T, w wireProfile, cut bool) {
	f := fixture(t, w)
	turn := providersim.Text("partial").WithStreamError(w.textFrame, providersim.RateLimit)
	if cut {
		turn = providersim.Text("partial").WithCut(w.textFrame)
	}
	f.sim.Script(turn)
	r, err := readStream(t.Context(), f.client(t, provider.WithMaxAttempts(2)), f.request())
	if err == nil || r.text != "partial" || r.finish != "" {
		t.Fatalf("partial text=%q finish=%q err=%v; want accepted text and an error, no finish", r.text, r.finish, err)
	}
	f.last(t, 1) // Never replay accepted output.
	if cut {
		var interruption *provider.StreamInterruptionError
		if !errors.As(err, &interruption) || !provider.Retryable(err) || provider.Classify(err) != provider.FailureTransient {
			t.Errorf("expected retryable stream interruption: %T %v", err, err)
		}
		return
	}
	var api *provider.APIError
	if !errors.As(err, &api) {
		t.Fatalf("expected an in-stream provider error, got %T: %v", err, err)
	}
	want := provider.FailureTransient
	if w.wire == providersim.Ollama {
		// Ollama NDJSON has only a string, no status or rate-limit code.
		want = provider.FailureUnrecognizedVerdict
	}
	if class := provider.Classify(err); class != want {
		t.Errorf("stream error class %v, want %v: %v", class, want, err)
	}
}

func storedContinuation(t *testing.T, w wireProfile) {
	f := fixture(t, w)
	f.sim.Script(providersim.Text("ticket 17"), providersim.Text("verified"))
	q := f.request()
	q.ResponseContinuationScope = f.p.ResponseContinuationScope(w.model)
	c := f.client(t)
	first, err := c.Complete(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Choices) != 1 || first.ContinuationID == "" {
		t.Fatalf("missing stored reply: %+v", first)
	}
	q.Messages = append(q.Messages, first.Choices[0].Message, provider.NewTextMessage("user", "Check it."))
	if r := f.stream(t, q); r.text != "verified" {
		t.Fatalf("continuation reply = %q", r.text)
	}
	r := f.last(t, 2)
	if r.PreviousResponseID != first.ContinuationID || len(r.System) != 0 || len(r.Messages) != 1 || r.Messages[0].Role != "user" || dialog(r.Conversation) != "Check it." || dialog(r.Effective) != "Find the ticket.\nticket 17\nCheck it." {
		t.Fatalf("previous_response_id=%q (want %q), delta=%s effective=%+v", r.PreviousResponseID, first.ContinuationID, r.Raw, r.Effective)
	}
}

func nativeCheckpoint(t *testing.T, w wireProfile) {
	const probe, source, summary, next = "blue-91", "The private ticket is green-48.", "Keep green-48.", "What is the ticket?"
	f := fixture(t, w, providersim.WithModels(w.wire, w.model))
	q := f.request()
	q.Messages = append(q.Messages, provider.NewTextMessage("assistant", source), provider.NewTextMessage("user", "Keep the ticket."))
	// Read the model's effective context, not a Crude-specific field. This
	// also scripts Messages' on-demand reply (the summary itself is signed).
	f.sim.Respond(func(r providersim.Request) providersim.Turn {
		text := dialog(r.Effective)
		switch {
		case strings.Contains(text, probe):
			return providersim.Text(probe)
		case strings.Contains(text, "green-48"):
			return providersim.Text(summary)
		default:
			return providersim.Text("no ticket here")
		}
	})
	checkpoint, err := f.p.NativeCheckpoint(t.Context(), provider.NativeCheckpointRequest{
		Model: w.model, Messages: q.Messages, Limit: 512,
		ProbeSource: "Remember " + probe, ProbeAnswer: probe, ProbeQuestion: "What was the code?",
		Directive: "Save the ticket.", Verify: "Read back the ticket.",
		ValidatePortable: func(s string) error {
			if s != summary {
				return fmt.Errorf("missing ticket: %q", s)
			}
			return nil
		},
	})
	if err != nil || checkpoint.Fallback != "" || checkpoint.Message.ContentString() != summary {
		t.Fatalf("native checkpoint text=%q fallback=%q err=%v", checkpoint.Message.ContentString(), checkpoint.Fallback, err)
	}
	log := f.sim.Requests()
	// Discovery may try more than one operation with a synthetic challenge,
	// but the one real compaction must use the wire's documented operation.
	realCompactions := 0
	for _, r := range log {
		if !strings.Contains(dialog(r.Conversation), source) {
			continue
		}
		ok := false
		switch w.wire {
		case providersim.Responses:
			ok = r.Path == "/v1/responses/compact"
		case providersim.Messages:
			var body struct{ Compaction struct{ Type string } }
			ok = r.Path == "/v1/messages" && json.Unmarshal(r.Raw, &body) == nil && body.Compaction.Type == "summarize"
		case providersim.Codex:
			for _, p := range parts(r.Conversation, providersim.PartOpaque) {
				var item struct{ Type string }
				if json.Unmarshal(p.Raw, &item) == nil && item.Type == "compaction_trigger" {
					ok = r.Path == "/backend-api/codex/responses"
				}
			}
		}
		if !ok {
			t.Fatalf("unexpected request with the source: %s %s", r.Path, r.Raw)
		}
		realCompactions++
	}
	if realCompactions != 1 {
		t.Fatalf("real compaction submissions = %d, want one", realCompactions)
	}
	q.Messages = provider.FilterReplayMessages([]provider.ChatMessage{q.Messages[0], checkpoint.Message, provider.NewTextMessage("user", next)}, f.p)
	reply := f.stream(t, q)
	r := f.last(t, len(log)+1)
	if reply.text != summary || !strings.Contains(dialog(r.Effective), "green-48") || !strings.Contains(dialog(r.Conversation), next) || strings.Contains(dialog(r.Conversation), source) {
		t.Fatalf("post-compaction reply=%q raw=%s effective=%+v", reply.text, r.Raw, r.Effective)
	}
	// A portable text-only fallback must not satisfy this check. The server
	// validates the exact issued (sealed or signed) item before journaling.
	for _, p := range parts(r.Conversation, providersim.PartOpaque) {
		var item struct {
			Type, Signature string
			Encrypted       string `json:"encrypted_content"`
		}
		if json.Unmarshal(p.Raw, &item) == nil && item.Type == "compaction" && (w.wire == providersim.Messages && item.Signature != "" || w.wire != providersim.Messages && item.Encrypted != "") {
			return
		}
	}
	t.Fatalf("missing authenticated compaction item in %s", r.Raw)
}
