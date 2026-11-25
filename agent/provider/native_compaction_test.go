package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/internal/providersim"
)

func compactFixture(p *Provider) NativeCheckpointRequest {
	return NativeCheckpointRequest{Model: p.Model, Messages: []ChatMessage{NewTextMessage("system", "current instructions"), NewTextMessage("user", "real-history-secret")}, ProbeAnswer: "blue-91", ProbeSource: "preserve blue-91", ProbeQuestion: "what was the code?", Directive: "summarize", Verify: "export", Limit: 900, ValidatePortable: func(string) error { return nil }}
}
func responsesText(w http.ResponseWriter, text string) {
	fmt.Fprintf(w, `{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":16,"output_tokens":3}}`, text)
}

func TestNativeCheckpointDiscoveryAndReplay(t *testing.T) {
	for _, mode := range []string{"standalone", "trigger", "codex-trigger", "messages", "advertised"} {
		t.Run(mode, func(t *testing.T) {
			wire, standard, model := providersim.Responses, "openai-responses", "m"
			anth := mode == "messages" || mode == "advertised"
			if mode == "codex-trigger" {
				wire, standard = providersim.Codex, "codex"
			}
			if anth {
				wire, standard, model = providersim.Messages, "anthropic", "claude-sonnet-4-6"
			}
			var opts []providersim.Option
			if mode == "messages" {
				opts = append(opts, providersim.WithModels(wire))
			}
			sim := providersim.New(t, opts...)
			base := sim.BaseURL(wire)
			sim.Respond(func(r providersim.Request) providersim.Turn {
				body := string(r.Raw)
				if r.Wire != wire || r.Model != model || (!anth && r.Header.Get("Authorization") != "Bearer k") || (anth && (r.Header.Get("x-api-key") != "k" || !strings.Contains(r.Header.Get("anthropic-beta"), compactBeta))) {
					t.Error("wrong model, wire or headers")
				}
				if wire == providersim.Codex && (!r.Stream || r.Store == nil || *r.Store || !strings.Contains(body, `"instructions":`) || strings.Contains(body, `"role":"system"`)) {
					t.Error("wrong Codex instructions, storage or stream")
				}
				if mode == "trigger" && strings.HasSuffix(r.Path, "/compact") {
					return providersim.HTTPError(providersim.ModelNotFound)
				}
				if strings.HasSuffix(r.Path, "/compact") || strings.Contains(body, `"type":"compaction_trigger"`) || (anth && strings.Contains(body, `"compaction":`)) {
					answer := "portable"
					if !strings.Contains(body, "real-history-secret") {
						if !strings.Contains(body, "blue-91") {
							t.Error("bad synthetic request")
						}
						answer = "probe summary"
					}
					return providersim.Text(answer).WithUsage(providersim.Usage{Input: 17, Output: 4})
				}
				effective, err := json.Marshal(r.Effective.Messages)
				if err != nil {
					t.Error(err)
				}
				if strings.Contains(body, "real-history-secret") || strings.Contains(body, "blue-91") {
					t.Error("source leaked into checkpoint continuation")
				}
				if strings.Contains(body, `"next"`) {
					if !anth && !strings.Contains(string(effective), "new input 1") || anth && !strings.Contains(body, "portable") {
						t.Error("resumed the wrong checkpoint")
					}
					return providersim.Text("continued")
				}
				if !anth && (r.Store == nil || *r.Store) {
					t.Error("read-back requested storage")
				}
				answer := "portable"
				if strings.Contains(body, "what was the code?") {
					if !strings.Contains(string(effective), "blue-91") && (!anth || !strings.Contains(body, "probe summary")) {
						t.Error("probe read-back did not recover its window")
					}
					answer = "blue-91"
				} else if !strings.Contains(string(effective), "real-history-secret") && (!anth || !strings.Contains(body, "portable")) {
					t.Error("real read-back did not recover its window")
				}
				return providersim.Text(answer).WithUsage(providersim.Usage{Input: 16, Output: 3})
			})
			p := &Provider{ID: "custom", AccountID: mode, Model: model, APIKey: "k", BaseURL: base, StandardName: standard}
			req := compactFixture(p)
			var labels []string
			req.Usage = func(label string, u TokenUsage, valid bool) {
				labels = append(labels, label)
				if !valid {
					t.Errorf("unsuccessful exchange: %s", label)
				}
				wantIn, wantOut := 17, 4
				if strings.Contains(label, "replay") {
					wantIn, wantOut = 16, 3
				}
				if u.PromptTokens != wantIn || u.CompletionTokens != wantOut {
					t.Errorf("%s usage: %+v", label, u)
				}
			}
			var msg ChatMessage
			for index := range 2 {
				req.Messages = append(req.Messages, NewTextMessage("user", fmt.Sprintf("new input %d", index)))
				got, err := p.NativeCheckpoint(t.Context(), req)
				if err != nil || got.Fallback != "" || got.Message.ContentString() != "portable" {
					t.Fatalf("native result %v fallback=%q", err, got.Fallback)
				}
				msg = got.Message
			}
			want := "compaction_probe compaction_probe_replay native_compaction native_checkpoint_replay native_compaction native_checkpoint_replay"
			if anth {
				want = "compaction_probe compaction_probe_replay native_compaction native_compaction"
				if mode == "advertised" {
					want = "native_compaction native_checkpoint_replay native_compaction"
				}
			}
			journal := sim.Requests()
			expectedCalls := len(labels)
			if anth {
				expectedCalls++
				if len(journal) == 0 || journal[0].Method != "GET" || journal[0].Path != "/v1/models/"+model || journal[0].Header.Get("x-api-key") != "k" || !strings.Contains(journal[0].Header.Get("anthropic-beta"), compactBeta) {
					t.Error("wrong discovery route or credential")
				}
			}
			if mode == "trigger" {
				expectedCalls++
			}
			if strings.Join(labels, " ") != want || len(journal) != expectedCalls {
				t.Fatalf("paid operations: %v, want %s; HTTP requests=%d want %d", labels, want, len(journal), expectedCalls)
			}
			data, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			var resumed ChatMessage
			if err := json.Unmarshal(data, &resumed); err != nil {
				t.Fatal(err)
			}
			if len(resumed.ProviderItems) != 1 {
				t.Fatalf("checkpoint artifacts %d", len(resumed.ProviderItems))
			}
			var seal string
			if anth {
				var block struct{ Signature string }
				if err := json.Unmarshal(resumed.ProviderItems[0].Data, &block); err != nil {
					t.Fatal(err)
				}
				seal = block.Signature
			} else {
				var items []struct {
					Type      string
					Encrypted string `json:"encrypted_content"`
				}
				if err := json.Unmarshal(resumed.ProviderItems[0].Data, &items); err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					if item.Type == "compaction" {
						seal = item.Encrypted
					}
				}
			}
			if seal == "" {
				t.Fatal("missing sealed checkpoint")
			}
			history := []ChatMessage{req.Messages[0], resumed, NewTextMessage("user", "next")}
			encode := func(p *Provider) string {
				q := ChatRequest{Model: p.Model, Messages: FilterReplayMessages(history, p)}
				b, e := p.Protocol().EncodeRequest(&q)
				if e != nil {
					t.Fatal(e)
				}
				return string(b)
			}
			if s := encode(p); !strings.Contains(s, seal) || (!anth && strings.Contains(s, "portable")) {
				t.Fatal("native replay lost after cold resume")
			}
			other := &Provider{ID: "custom", AccountID: "other", Model: model, APIKey: "k", BaseURL: base, StandardName: standard}
			if s := encode(other); strings.Contains(s, seal) || !strings.Contains(s, "portable") {
				t.Fatal("account switch leaked artifact or lost portable context")
			}
			if err := ForgetCompactionSupport(mode); err != nil {
				t.Fatal(err)
			}
			if s := encode(p); !strings.Contains(s, seal) {
				t.Fatal("forgetting a capability rewrote a working conversation prefix")
			}
			// A cold adapter can still emit a syntactically valid but unusable item.
			// Ask the issuing server to verify the original seal after the forget.
			client, err := p.Client()
			if err != nil {
				t.Fatal(err)
			}
			q := ChatRequest{Model: p.Model, Messages: FilterReplayMessages(history, p)}
			stream, err := client.Stream(t.Context(), &q)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var text strings.Builder
			for {
				u, err := stream.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				text.WriteString(u.ContentDelta)
			}
			if text.String() != "continued" {
				t.Fatalf("resumed reply %q", text.String())
			}

		})
	}
}

func TestNativeEvidenceSurvivesReopenAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	p := &Provider{ID: "custom", AccountID: "acct", Model: "m", BaseURL: "https://gateway.invalid/v1", StandardName: "openai-responses"}
	key := p.compactionScope(p.Model)
	c, err := OpenModelCatalog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	flight, _ := c.compaction.begin(t.Context(), key, p.AccountID)
	for _, state := range []string{supportVerified, supportCooling} {
		if err := c.compaction.record(key, flight, p, p.Model, state, "responses"); err != nil {
			t.Fatal(err)
		}
	}
	f := c.compaction.fact(key)
	if f.state != supportVerified || f.usable() || f.retryAt <= time.Now().Unix() {
		t.Fatal("one transient erased the verdict or allowed an immediate retry")
	}
	c.compaction.end(key, flight)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = OpenModelCatalog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if next := c.compaction.fact(key); next != f {
		t.Fatalf("restored %+v want %+v", next, f)
	}
	if err = c.compaction.forget(p.AccountID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = c.db.QueryRow(`SELECT count(*) FROM compaction_support`).Scan(&n); err != nil || n != 0 {
		t.Fatal("forget did not delete evidence")
	}
}
func TestForgetCancelsDiscoveryBeforeRealRequest(t *testing.T) {
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Script(providersim.Turn{}.WithGate(gate))
	p := &Provider{ID: "custom", AccountID: "forget-flight", Model: "m", BaseURL: server.BaseURL(providersim.Responses), StandardName: "openai-responses"}
	done := make(chan error, 1)
	go func() { _, e := p.NativeCheckpoint(t.Context(), compactFixture(p)); done <- e }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := gate.Wait(ctx); err != nil {
		t.Fatal("no check:", err)
	}
	if err := ForgetCompactionSupport(p.AccountID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("forgotten result accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forget did not cancel request")
	}
	if err := gate.WaitCanceled(ctx); err != nil {
		t.Fatal("forgotten request still open:", err)
	}
	if len(server.Requests()) != 1 || currentSupport().fact(p.compactionScope("m")).fresh() {
		t.Fatal("stale discovery sent another request or published evidence")
	}
}
func TestNativePolicyAndUncertainSpend(t *testing.T) {
	server := providersim.New(t)
	var real, probe atomic.Int32
	server.Respond(func(r providersim.Request) providersim.Turn {
		if strings.Contains(string(r.Raw), "real-history-secret") {
			real.Add(1)
			return providersim.HTTPError(providersim.Overload)
		}
		if strings.HasSuffix(r.Path, "/compact") {
			probe.Add(1)
			return providersim.Turn{}
		}
		return providersim.Text("blue-91")
	})
	p := &Provider{ID: "custom", Model: "m", BaseURL: server.BaseURL(providersim.Responses), StandardName: "openai-responses"}
	p.SetProtocolSettings(&ProtocolSettings{Compaction: CompactionErrandOnly})
	if r, e := p.NativeCheckpoint(context.Background(), compactFixture(p)); e != nil || r.Fallback == "" || len(server.Requests()) != 0 {
		t.Fatal("Errand only probed")
	}
	p.SetProtocolSettings(nil)
	// Auto keeps the run moving with Errand and tells the user once.
	if r, e := p.NativeCheckpoint(t.Context(), compactFixture(p)); e != nil || !r.Changed || r.Fallback != "The provider did not confirm native compaction (HTTP 503)." || real.Load() != 1 {
		t.Fatalf("uncertain real call: %+v %v", r, e)
	}
	if r, e := p.NativeCheckpoint(t.Context(), compactFixture(p)); e != nil || r.Changed || r.Fallback == "" || real.Load() != 1 {
		t.Fatal("auto resent a possibly paid request or repeated the notice")
	}
	// Provider only never charges Errand: while the marker holds, the serving
	// model writes a portable checkpoint instead of resending the native call.
	p.SetProtocolSettings(&ProtocolSettings{Compaction: CompactionProviderOnly})
	r, e := p.NativeCheckpoint(t.Context(), compactFixture(p))
	if e != nil || r.Fallback == "" || r.Transient || real.Load() != 1 || probe.Load() != 1 {
		t.Fatalf("provider-only resent or stopped: %+v %v", r, e)
	}
	var status *StatusError
	// An explicit /compact may resend once the route cooldown has passed.
	c := currentSupport()
	k := p.compactionScope(p.Model)
	c.mu.Lock()
	f := c.facts[k]
	f.retryAt = 0
	c.facts[k] = f
	c.mu.Unlock()
	explicit := compactFixture(p)
	explicit.Explicit = true
	if _, e = p.NativeCheckpoint(t.Context(), explicit); !errors.As(e, &status) || status.StatusCode != http.StatusServiceUnavailable || real.Load() != 2 || probe.Load() != 2 {
		t.Fatalf("explicit retry: real=%d probe=%d %v", real.Load(), probe.Load(), e)
	}
}

// A definite refusal in Provider only is the route's answer, not an outage:
// the serving model writes the portable checkpoint and the run continues.
func TestProviderOnlyRefusalLetsTheServingModelWrite(t *testing.T) {
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("compaction is not supported for this model")
	})
	p := &Provider{ID: "custom", AccountID: "refusal", Model: "m", BaseURL: server.BaseURL(providersim.Responses), StandardName: "openai-responses"}
	p.SetProtocolSettings(&ProtocolSettings{Compaction: CompactionProviderOnly})
	r, err := p.NativeCheckpoint(t.Context(), compactFixture(p))
	if err != nil || r.Transient || r.Fallback != "The provider rejected native compaction (HTTP 400)." {
		t.Fatalf("refusal: %+v %v", r, err)
	}
	if c := currentSupport().fact(p.compactionScope("m")); c.state != supportUnavailable {
		t.Fatalf("refused probe was not negative evidence: %+v", c)
	}
}

// An unverified anchored delta that reaches a missing standalone operation
// is resent to the create operation, never as a full-history upload.
func TestNativeAnchoredAttemptFindsTriggerOperation(t *testing.T) {
	sim := providersim.New(t)
	sim.Script(providersim.Text("done"), providersim.HTTPError(providersim.ModelNotFound), providersim.Turn{}, providersim.Text("portable summary"))
	p := &Provider{ID: "custom", AccountID: "anchor-trigger", Model: "m", BaseURL: sim.BaseURL(providersim.Responses), StandardName: "openai-responses"}
	r, anchor := anchoredCheckpoint(t, p)
	got, err := p.NativeCheckpoint(t.Context(), r)
	calls := sim.Requests()
	if err != nil || got.Fallback != "" || len(calls) != 4 {
		t.Fatalf("anchored operations=%d err=%v fallback=%q", len(calls), err, got.Fallback)
	}
	if calls[1].Path != "/v1/responses/compact" || calls[2].Path != "/v1/responses" || !strings.Contains(string(calls[2].Raw), `"type":"compaction_trigger"`) {
		t.Error("wrong alternate operation")
	}
	for _, call := range calls[1:3] {
		if call.PreviousResponseID != anchor || !strings.Contains(string(call.Raw), "next step") {
			t.Error("anchor or suffix lost on alternate operation")
		}
	}
	for _, call := range calls[1:] {
		if strings.Contains(string(call.Raw), "real-history-secret") || strings.Contains(string(call.Raw), "blue-91") {
			t.Error("sent full history or an unnecessary probe")
		}
	}
	if f := currentSupport().fact(p.compactionScope("m")); f.state != supportVerified || f.method != p.Protocol().Info().ChatPath {
		t.Fatalf("working operation not learned: %+v", f)
	}
}

func anchoredCheckpoint(t *testing.T, p *Provider) (NativeCheckpointRequest, string) {
	t.Helper()
	r := compactFixture(p)
	client, err := p.Client()
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Complete(t.Context(), &ChatRequest{Model: p.Model, Messages: r.Messages, ResponseContinuationScope: p.ResponseContinuationScope(p.Model)})
	if err != nil || len(first.Choices) != 1 || first.Choices[0].Message.ResponseReference == nil {
		t.Fatalf("no stored anchor: %+v %v", first, err)
	}
	anchor := first.Choices[0].Message.ResponseReference.ID
	assistant := first.Choices[0].Message
	// Stamp the accepted provider items, as the turn loop does before persistence.
	assistant.ReplayScope = p.ReplayScope()
	assistant.ReplayPrefixHash = MessagePrefixHash(r.Messages)
	assistant.ReplaySourceModel = p.Model
	assistant.ProviderItems = StampProviderItems(CanonicalizeProviderItems(assistant.ProviderItems), p, p.Model, assistant.ReplayPrefixHash)
	BindResponseReference(&assistant, assistant.ResponseReference)
	r.Messages = append(r.Messages, assistant, NewTextMessage("user", "next step"))
	return r, anchor
}

func TestNativeStoredReferenceSkipsProbeAndHistory(t *testing.T) {
	endpoint := providersim.New(t)
	endpoint.Script(providersim.Text("done"), providersim.Turn{}, providersim.Text("portable summary"))
	p := &Provider{ID: "custom", AccountID: "anchor", Model: "m", BaseURL: endpoint.BaseURL(providersim.Responses), StandardName: "openai-responses"}
	r, anchor := anchoredCheckpoint(t, p)
	got, e := p.NativeCheckpoint(t.Context(), r)
	calls := endpoint.Requests()
	if e != nil || got.Fallback != "" || len(calls) != 3 {
		t.Fatalf("anchored calls=%d err=%v fallback=%s", len(calls), e, got.Fallback)
	}
	for _, call := range calls[1:] {
		if strings.Contains(string(call.Raw), "real-history-secret") || strings.Contains(string(call.Raw), "blue-91") {
			t.Error("sent full history or paid for an unnecessary probe")
		}
	}
	if calls[1].PreviousResponseID != anchor || calls[1].Path != "/v1/responses/compact" || !strings.Contains(string(calls[1].Raw), "next step") {
		t.Error("wrong anchor or suffix")
	}
	history := []ChatMessage{r.Messages[0], got.Message}
	projected := FilterReplayMessages(history, p)
	if _, ok := p.RecoverNativeReplay(projected, &StatusError{StatusCode: 503, Message: "invalid compaction"}); ok {
		t.Fatal("retried uncertain rejection")
	}
	portable, ok := p.RecoverNativeReplay(projected, &StatusError{StatusCode: 400, Message: "invalid compaction item"})
	if !ok || len(portable[1].ProviderItems) != 0 || len(history[1].ProviderItems) != 1 || len(FilterReplayMessages(history, p)[1].ProviderItems) != 0 {
		t.Fatal("could not recover portably without mutating durable history")
	}
	history = append(history, NewTextMessage("user", strings.Repeat("later tool rounds ", 4_000)))
	if !p.PortableReplayTooLarge(history, FilterReplayMessages(history, p)) {
		t.Fatal("would replay a large suffix cold")
	}
	reply := NewTextMessage("assistant", "accepted on portable prefix")
	reply.ReplayScope = p.ReplayScope()
	reply.ReplayPrefixHash = MessagePrefixHash(FilterReplayMessages(history, p))
	history = append(history, reply)
	if p.PortableReplayTooLarge(history, FilterReplayMessages(history, p)) {
		t.Fatal("failed to recognize the already warm portable prefix")
	}

}

func TestCompactionAttemptGuardSurvivesRestartAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := OpenModelCatalog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := attemptKey{"route", "source", "account"}
	f, _ := c.compaction.begin(t.Context(), key.scope, key.account)
	if err = c.compaction.guardAttempt(t.Context(), key, f, false, true); err != nil {
		t.Fatal(err)
	}
	c.compaction.end(key.scope, f)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = OpenModelCatalog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.compaction.forget(key.account); err != nil {
		t.Fatal(err)
	}
	f, _ = c.compaction.begin(t.Context(), key.scope, key.account)
	defer c.compaction.end(key.scope, f)
	var earlier *earlierAttemptError
	if err = c.compaction.guardAttempt(t.Context(), key, f, false, false); !errors.As(err, &earlier) || time.Until(earlier.until) > attemptHold || time.Until(earlier.until) <= 0 {
		t.Fatalf("auto repeated an uncertain post: %v", err)
	}
	if err = c.compaction.guardAttempt(t.Context(), key, f, true, true); err != nil {
		t.Fatalf("explicit retry refused: %v", err)
	}
	if err = c.compaction.releaseRejected(key, f); err != nil {
		t.Fatal(err)
	}
	if err = c.compaction.guardAttempt(t.Context(), key, f, false, false); err != nil {
		t.Fatal("a confirmed rejection was treated as billable uncertainty")
	}
}
func TestChangingCompactionPolicyDoesNotRevokeChatCredentials(t *testing.T) {
	account := Account{ID: "change", ProviderID: "custom", APIKey: "key", Connection: Template{Name: "example", APIStandard: "openai-responses", ProviderURL: "https://example.invalid/v1", AuthType: AuthBearer}}
	m := NewAccountManager(map[string]Account{account.ID: account}, nil)
	p := &Provider{ID: account.ProviderID, Model: "m"}
	if err := account.Connection.Apply(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Bind(p, account.ID); err != nil {
		t.Fatal(err)
	}
	f, _ := currentSupport().begin(t.Context(), p.compactionScope(p.Model), p.AccountID)
	defer currentSupport().end(p.compactionScope(p.Model), f)
	account.Connection.Protocol = &ProtocolSettings{Compaction: CompactionErrandOnly}
	m.Replace(map[string]Account{account.ID: account})
	if context.Cause(f.ctx) != errAccountChanged || p.UsesNativeCompaction() {
		t.Fatal("policy change left an old check or native operation enabled")
	}
	if _, err := p.RequestHeaders(t.Context()); err != nil {
		t.Fatal("compaction preference broke an ordinary chat binding")
	}
	m.Replace(nil)
	if _, err := p.RequestHeaders(t.Context()); err == nil {
		t.Fatal("removed credentials still work")
	}
}
func TestIgnoredNativeOptionNeverSendsUserHistory(t *testing.T) {
	// This gateway ignores both operations, including /compact, returning an
	// ordinary message where CompactResource requires a compaction window.
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "real-history-secret") {
			t.Error("private history used for discovery")
		}
		responsesText(w, "I made a summary.")
	}))
	defer server.Close()
	p := &Provider{ID: "custom", AccountID: "ignore", Model: "m", BaseURL: server.URL, StandardName: "openai-responses"}
	if !p.NativeCompactionPlanned("m") {
		t.Fatal("an unchecked native wire must be tried at the boundary")
	}
	for range 2 {
		r, err := p.NativeCheckpoint(t.Context(), compactFixture(p))
		if err != nil || r.Fallback == "" || len(r.Message.ProviderItems) != 0 {
			t.Fatal("ignored option treated as success")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("should test the two wire operations once, not each turn: %d", calls.Load())
	}
	if p.NativeCompactionPlanned("m") {
		t.Fatal("a cooling route disabled Errand's background preparation")
	}
	p.SetProtocolSettings(&ProtocolSettings{Compaction: CompactionProviderOnly})
	if !p.NativeCompactionPlanned("m") {
		t.Fatal("provider only must wait for the native route")
	}
	p.SetProtocolSettings(nil)
	c := currentSupport()
	k := p.compactionScope(p.Model)
	c.mu.Lock()
	f := c.facts[k]
	f.retryAt = 0
	c.facts[k] = f
	c.mu.Unlock()
	if _, err := p.NativeCheckpoint(t.Context(), compactFixture(p)); err != nil || c.fact(k).state != supportUnavailable || calls.Load() != 4 {
		t.Fatal("repeated ignored options did not earn a longer negative verdict")
	}
	if p.NativeCompactionPlanned("m") {
		t.Fatal("a refused route disabled Errand's background preparation")
	}
	c.mu.Lock()
	f = c.facts[k]
	f.expires = 0
	c.facts[k] = f
	c.mu.Unlock()
	if _, err := p.NativeCheckpoint(t.Context(), compactFixture(p)); err != nil || calls.Load() != 6 {
		t.Fatal("expired negative was never rechecked")
	}

}

// Deleting an account removes what Crude learned about it and its attempt
// markers; a restart also drops evidence for accounts changed while offline.
func TestAccountRemovalClearsCompactionEvidence(t *testing.T) {
	c, err := OpenModelCatalog(filepath.Join(t.TempDir(), "catalog.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	prior, unavailable := activeModelCatalog.Load(), catalogUnavailable.Load()
	UseModelCatalog(c)
	t.Cleanup(func() {
		activeModelCatalog.Store(prior)
		catalogUnavailable.Store(unavailable)
		_ = c.Close()
	})
	account := Account{ID: "gone", ProviderID: "custom", APIKey: "key", Connection: Template{Name: "gateway", APIStandard: "openai-responses", ProviderURL: "https://gateway.invalid/v1", AuthType: AuthBearer}}
	m := NewAccountManager(map[string]Account{account.ID: account}, nil)
	p := &Provider{ID: account.ProviderID, Model: "m"}
	if err := account.Connection.Apply(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Bind(p, account.ID); err != nil {
		t.Fatal(err)
	}
	learn := func() {
		t.Helper()
		key := p.compactionScope("m")
		f, _ := c.compaction.begin(t.Context(), key, account.ID)
		defer c.compaction.end(key, f)
		if err := c.compaction.record(key, f, p, "m", supportVerified, "responses/compact"); err != nil {
			t.Fatal(err)
		}
		if err := c.compaction.guardAttempt(t.Context(), attemptKey{key, "source", account.ID}, f, false, true); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(table string) (n int) {
		t.Helper()
		if err := c.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	learn()
	if got := CompactionObservations(map[string]Account{account.ID: account}); len(got) != 1 || got[0].Status != supportVerified {
		t.Fatalf("observations %+v", got)
	}
	m.Replace(map[string]Account{})
	if rows("compaction_support") != 0 || rows("compaction_attempts") != 0 || len(CompactionObservations(map[string]Account{account.ID: account})) != 0 || c.compaction.fact(p.compactionScope("m")).state != "" {
		t.Fatal("deleted account kept its compaction evidence")
	}
	// Offline edits: a kept account keeps evidence, a changed key loses it.
	learn()
	if err := c.PruneCompactionSupport(map[string]Account{account.ID: account}); err != nil || rows("compaction_support") != 1 {
		t.Fatalf("pruned a current route: %v", err)
	}
	rotated := account
	rotated.APIKey = "rotated"
	if err := c.PruneCompactionSupport(map[string]Account{account.ID: rotated}); err != nil || rows("compaction_support") != 0 {
		t.Fatalf("kept evidence for a changed credential: %v", err)
	}
	if err := c.PruneCompactionSupport(map[string]Account{}); err != nil || rows("compaction_attempts") != 0 {
		t.Fatalf("kept attempt markers for a deleted account: %v", err)
	}
}

// Keep the gateway variants separate from the pinned Codex trigger above.
// openai/codex@ebd0765 uses streamed create, not standalone /compact. This
// gateway can also mount /compact, append a stray answer to the trigger, and
// omit the items from the final envelope after sending output_item.done.
func TestCodexNativeCompactionStreams(t *testing.T) {
	for _, mode := range []string{"standalone", "trigger"} {
		t.Run(mode, func(t *testing.T) {
			var standalone, triggers, follows atomic.Int32
			sse := func(w http.ResponseWriter, items ...string) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, item := range items {
					fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":%s}\n\n", item)
				}
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n")
			}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				s := string(raw)
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				if instructions, _ := body["instructions"].(string); instructions == "" || strings.Contains(s, `"role":"system"`) {
					t.Errorf("codex request without top-level instructions: %s", s)
				}
				token := "codex-probe"
				if strings.Contains(s, "real-history-secret") {
					token = "codex-real"
				}
				if strings.HasSuffix(r.URL.Path, "/compact") {
					standalone.Add(1)
					if mode == "trigger" {
						http.NotFound(w, r)
						return
					}
					fmt.Fprintf(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":%q}],"usage":{"input_tokens":9,"output_tokens":2}}`, token)
					return
				}
				if body["stream"] != true || body["store"] != false {
					t.Errorf("codex create request must stream without storage: %s", s)
				}
				if strings.Contains(s, "compaction_trigger") {
					triggers.Add(1)
					sse(w, fmt.Sprintf(`{"type":"compaction","encrypted_content":%q}`, token), `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ignored reply"}]}`)
					return
				}
				follows.Add(1)
				answer := "portable"
				if strings.Contains(s, "codex-probe") {
					answer = "blue-91"
				}
				sse(w, fmt.Sprintf(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}`, answer))
			}))
			defer endpoint.Close()
			p := &Provider{ID: "custom", AccountID: "codex-" + mode, Model: "m", APIKey: "k", BaseURL: endpoint.URL, StandardName: "codex"}
			got, err := p.NativeCheckpoint(t.Context(), compactFixture(p))
			if err != nil || got.Fallback != "" || got.Message.ContentString() != "portable" {
				t.Fatalf("codex native result %+v %v", got, err)
			}
			wantTriggers := int32(0)
			if mode == "trigger" {
				wantTriggers = 2
			}
			if triggers.Load() != wantTriggers || follows.Load() != 2 || standalone.Load() == 0 {
				t.Fatalf("standalone=%d triggers=%d follows=%d", standalone.Load(), triggers.Load(), follows.Load())
			}
			item := got.Message.ProviderItems[0]
			if item.Standard != "openai-responses" || strings.Contains(string(item.Data), "ignored reply") || !strings.Contains(string(item.Data), "codex-real") {
				t.Fatalf("window = %s (%s)", item.Data, item.Standard)
			}
			history, _ := ProjectReplayMessages([]ChatMessage{compactFixture(p).Messages[0], got.Message, NewTextMessage("user", "next")}, p)
			b, err := p.Protocol().EncodeRequest(&ChatRequest{Model: p.Model, Messages: history})
			if err != nil || !strings.Contains(string(b), "codex-real") {
				t.Fatalf("next codex request lost the window: %s %v", b, err)
			}
		})
	}
}

// OpenAI documents that /responses/compact takes the window as the
// conversation renders it, without instructions. The system message stays the
// first input item, so the call shares the conversation's cached prefix, and
// a retained copy of it in the returned window is not replayed twice.
func TestResponsesCompactKeepsTheConversationPrefix(t *testing.T) {
	s := &OpenAIResponsesStandard{}
	req := &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("system", "conversation rules"), NewTextMessage("user", "history")}}
	body, err := s.compactBody(req, "four sections", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(body)
	normal, _ := s.EncodeRequest(req)
	var compact, create struct{ Input []json.RawMessage }
	_ = json.Unmarshal(encoded, &compact)
	_ = json.Unmarshal(normal, &create)
	if _, ok := body["instructions"]; ok || len(compact.Input) != 2 || string(compact.Input[0]) != string(create.Input[0]) || strings.Contains(string(encoded), "four sections") {
		t.Fatalf("compact body diverges from the conversation prefix: %s", encoded)
	}
	w, _, err := s.compactResult([]byte(`{"object":"response.compaction","output":[{"type":"message","role":"system","content":[{"type":"input_text","text":"conversation rules"}]},{"type":"compaction","encrypted_content":"x"},{"type":"message","role":"user","content":[{"type":"input_text","text":"kept"}]}]}`))
	if err != nil || strings.Contains(string(w.raw), "conversation rules") || !strings.Contains(string(w.raw), "kept") {
		t.Fatalf("window = %s %v", w.raw, err)
	}
	if _, _, err := s.compactResult([]byte(`{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"x"},{"type":"message","role":"developer","content":[]}]}`)); !errors.Is(err, errNoWindow) {
		t.Fatalf("a developer item after the summary was accepted: %v", err)
	}
}
