package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

const responsesWebSearchWire = `{"status":"completed","output":[
	{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[
		{"type":"url","url":"https://official.example/docs#step"},
		{"type":"url","url":"https://OFFICIAL.example/docs"},
		{"type":"url","url":"https://reference.example/topics"},
		{"type":"url","url":"https://more.example/extra"}
	]}},
	{"type":"message","role":"assistant","content":[{"type":"output_text",
		"text":"Generated prose or https://invented.example/",
		"annotations":[
			{"type":"url_citation","url":"https://official.example/docs#step","title":"Provider-backed title"},
			{"type":"url_citation","url":"https://invented.example/","title":"Unverified annotation"}
		]}]}
]}`

// Exercise the real Provider header source, connection, and protocol registry.
func newWebSearchFixture(t *testing.T, protocol string, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := &Provider{ID: "fixture-provider", AccountID: t.Name(), APIKey: "fixture-api-key", Model: "private-model"}
	if err := (Template{
		Name: "Fixture", ProviderURL: server.URL + "/proxy/custom/", APIStandard: protocol, AuthType: AuthProtocol,
		Headers:   map[string]string{"X-Fixture-Gateway": "gateway-value"},
		Dialer:    &DialSettings{MaxAttempts: 9},
		ExtraBody: map[string]any{"previous_response_id": "private-chat", "input": "hidden conversation", "store": true},
		Protocol:  &ProtocolSettings{ServiceTier: "priority"},
	}).Apply(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWebSearchProtocolRequests(t *testing.T) {
	const query = "literal: weather in Montréal\nNo chat context."
	const queryJSON = `"literal: weather in Montréal\nNo chat context."`
	const redirectURI = "https://vertexaisearch.cloud.google.com/grounding-api-redirect/example"
	for _, tt := range []struct {
		protocol, path, model, authHeader, authValue, body, reply string
		want                                                      []WebSearchSource
	}{
		{
			protocol: "ollama", path: "/proxy/custom/web_search", model: "\x00ignored", authHeader: "Authorization", authValue: "Bearer fixture-api-key",
			body:  `{"query":` + queryJSON + `,"max_results":2}`,
			reply: `{"results":[{"title":" A\n title ","url":"https://docs.example/","content":" An\t\t excerpt.\n "},{"url":"https://two.example/"},{"url":"https://more.example/"}]}`,
			want:  []WebSearchSource{{URL: "https://docs.example/", Title: "A title", Snippet: "An excerpt."}, {URL: "https://two.example/", Title: "two.example"}},
		},
		{
			protocol: "openai-responses", path: "/proxy/custom/responses", model: "selected-model", authHeader: "Authorization", authValue: "Bearer fixture-api-key",
			body:  `{"model":"selected-model","input":` + queryJSON + `,"store":false,"tools":[{"type":"web_search","search_context_size":"low"}],"tool_choice":{"type":"web_search"},"include":["web_search_call.action.sources"],"max_tool_calls":1,"max_output_tokens":1024}`,
			reply: responsesWebSearchWire,
			want:  []WebSearchSource{{URL: "https://official.example/docs#step", Title: "Provider-backed title"}, {URL: "https://reference.example/topics", Title: "reference.example"}},
		},
		{
			protocol: "anthropic", path: "/proxy/custom/messages", model: "selected-model", authHeader: "X-Api-Key", authValue: "fixture-api-key",
			body: `{"model":"selected-model","max_tokens":1024,"messages":[{"role":"user","content":` + queryJSON + `}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`,
			reply: `{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search"},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[
				{"type":"web_search_result","title":"First","url":"https://first.example/","encrypted_content":"not a snippet"},
				{"type":"web_search_result","title":"Second","url":"https://second.example/"},{"type":"web_search_result","title":"Third","url":"https://third.example/"}]},
				{"type":"text","text":"An answer","citations":[{"type":"web_search_result_location","url":"https://invented.example/"}]}],"stop_reason":"end_turn"}`,
			want: []WebSearchSource{{URL: "https://first.example/", Title: "First"}, {URL: "https://second.example/", Title: "Second"}},
		},
		{
			protocol: "gemini", path: "/proxy/custom/models/selected-model:generateContent", model: "selected-model", authHeader: "X-Goog-Api-Key", authValue: "fixture-api-key",
			body: `{"contents":[{"role":"user","parts":[{"text":` + queryJSON + `}]}],"tools":[{"google_search":{}}],"generationConfig":{"maxOutputTokens":1024}}`,
			reply: `{"candidates":[{"groundingMetadata":{"groundingChunks":[
				{"web":{"uri":"` + redirectURI + `","title":"Source with a redirect"}},
				{"image":{"uri":"https://ignore.example/"}},{"web":{"uri":"https://second.example/","title":"Second"}},
				{"web":{"uri":"https://third.example/","title":"Third"}}]}}]}`,
			want: []WebSearchSource{{URL: redirectURI, Title: "Source with a redirect"}, {URL: "https://second.example/", Title: "Second"}},
		},
	} {
		t.Run(tt.protocol, func(t *testing.T) {
			var requests atomic.Int32
			var wantBody any
			if err := json.Unmarshal([]byte(tt.body), &wantBody); err != nil {
				t.Fatal(err)
			}
			p := newWebSearchFixture(t, tt.protocol, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.RequestURI() != tt.path || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("wrong route or media type: %s %s %v", r.Method, r.URL, r.Header.Get("Content-Type"))
				}
				for _, key := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
					want := ""
					if key == tt.authHeader {
						want = tt.authValue
					}
					if got := r.Header.Get(key); got != want {
						t.Errorf("wrong auth placement: %s = %q, want %q", key, got, want)
					}
				}
				if got := r.Header.Get("Anthropic-Version"); (tt.protocol == "anthropic" && got != "2026-10-21") || (tt.protocol != "anthropic" && got != "") {
					t.Errorf("wrong version header: %q", got)
				}
				if got := r.Header.Get("X-Fixture-Gateway"); got != "gateway-value" {
					t.Errorf("missing account header: %q", got)
				}
				var body any
				decoder := json.NewDecoder(r.Body)
				if err := decoder.Decode(&body); err != nil || !reflect.DeepEqual(body, wantBody) {
					t.Errorf("wrong search request: %#v, err=%v", body, err)
				}
				if err := decoder.Decode(&body); err != io.EOF {
					t.Errorf("request carried trailing data: %v", err)
				}
				_, _ = io.WriteString(w, tt.reply)
			})
			sources, err := p.SearchWeb(t.Context(), tt.model, query, 2)
			if err != nil || requests.Load() != 1 || !reflect.DeepEqual(sources, tt.want) {
				t.Fatalf("sources=%#v, error=%v, requests=%d", sources, err, requests.Load())
			}
			observation, ok := p.WebSearchObservation(tt.model)
			if !ok || !observation.Working || observation.Detail != "" || observation.Results != 2 || observation.Latency <= 0 || observation.At.IsZero() {
				t.Fatalf("observation = %+v, present = %v", observation, ok)
			}
		})
	}
}

type siblingWebSearchProtocol struct{ OllamaStandard }

func (p *siblingWebSearchProtocol) Info() ProtocolInfo {
	info := p.OllamaStandard.Info()
	info.ID = "fixture-sibling-web-search"
	info.WebSearchPath = "../v1/search"
	return info
}

func TestWebSearchEndpointPathUsesProtocolDeclaration(t *testing.T) {
	proto := &siblingWebSearchProtocol{}
	RegisterProtocol(proto)
	t.Cleanup(func() {
		registryMu.Lock()
		defer registryMu.Unlock()
		delete(registry, proto.Info().ID)
	})
	p := newWebSearchFixture(t, proto.Info().ID, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/proxy/v1/search" {
			t.Errorf("search path = %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	if sources, err := p.SearchWeb(t.Context(), "", "query", 1); err != nil || len(sources) != 0 {
		t.Fatalf("empty endpoint results = %v, err = %v", sources, err)
	}
	observation, ok := p.WebSearchObservation("different-model")
	if !ok || !observation.Working || observation.Results != 0 || observation.Detail != "" {
		t.Fatalf("empty search was not observed as working: %+v, present = %v", observation, ok)
	}
}

func TestWebSearchSupportByProtocol(t *testing.T) {
	want := map[string]bool{"ollama": false, "openai-responses": true, "anthropic": true, "gemini": true}
	for _, protocol := range ProtocolList() {
		id := protocol.Info().ID
		wantModel, wantSupported := want[id]
		// Neither the host nor the auth type is evidence about the protocol.
		gotSupported, gotModel := WebSearchSupport(Template{APIStandard: id, ProviderURL: "https://gateway.example/custom", AuthType: AuthChatGPT})
		if gotSupported != wantSupported || gotModel != wantModel {
			t.Errorf("protocol %s: support=%t, model=%t; want %t, %t", id, gotSupported, gotModel, wantSupported, wantModel)
		}
		if wantSupported {
			if _, ok := protocol.(WebSearchCodec); !ok {
				t.Errorf("protocol %s declares search but has no codec", id)
			}
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("missing supported protocols: %v", want)
	}
}

func TestWebSearchResponsesRequiresCompletedTypedSearch(t *testing.T) {
	for _, tt := range []struct {
		name, reply string
		wantCount   int // -1 means the account did not produce valid search evidence.
	}{
		{"gateway ignores tool and returns prose", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"https://invented.example","annotations":[{"type":"url_citation","url":"https://invented.example","title":"invented"}]}]}]}`, -1},
		{"failed call", strings.Replace(responsesWebSearchWire, `"web_search_call","status":"completed"`, `"web_search_call","status":"failed"`, 1), -1},
		{"unfinished call", strings.Replace(responsesWebSearchWire, `"web_search_call","status":"completed"`, `"web_search_call","status":"in_progress"`, 1), -1},
		{"output capped after search", strings.Replace(responsesWebSearchWire, `"status":"completed","output"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output"`, 1), 3},
		{"content filter", strings.Replace(responsesWebSearchWire, `"status":"completed","output"`, `"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output"`, 1), -1},
		{"empty real search", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[]}}]}`, 0},
		{"missing sources", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search"}}]}`, -1},
		{"untyped sources", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[{"url":"https://example.com"}]}}]}`, -1},
		{"all sources unsafe", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[{"type":"url","url":"file:///secret"}]}}]}`, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newWebSearchFixture(t, "openai-responses", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tt.reply) })
			sources, err := p.SearchWeb(t.Context(), "fixture", "query", 10)
			if tt.wantCount < 0 {
				if err == nil || !strings.Contains(err.Error(), "did not return search sources") || len(sources) != 0 {
					t.Fatalf("unverified search succeeded: sources=%v, err=%v", sources, err)
				}
			} else if err != nil || len(sources) != tt.wantCount {
				t.Fatalf("search failed: sources=%v, err=%v", sources, err)
			}
			got, ok := p.WebSearchObservation("fixture")
			if !ok || got.Working != (err == nil) || got.Results != len(sources) || (err != nil && got.Detail != err.Error()) {
				t.Fatalf("wrong observation: %+v, present = %v", got, ok)
			}
		})
	}
}

func TestWebSearchAnthropicToolError(t *testing.T) {
	for _, tt := range []struct{ code, message string }{
		{"max_uses_exceeded", "The account reached its search limit. Try a simpler query."},
		{"secret echoed as an unknown error code", "Web search is not available. Try again later."},
	} {
		t.Run(tt.code, func(t *testing.T) {
			p := newWebSearchFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":{"type":"web_search_tool_result_error","error_code":%q}}]}`, tt.code)
			})
			if sources, err := p.SearchWeb(t.Context(), "fixture", "query", 4); err == nil || err.Error() != tt.message || len(sources) != 0 {
				t.Fatalf("sources = %v, err = %v", sources, err)
			}
			if observation, ok := p.WebSearchObservation("fixture"); !ok || observation.Working || observation.Detail != tt.message {
				t.Fatalf("wrong observation: %+v, present = %v", observation, ok)
			}
		})
	}
}

func TestWebSearchRejectsHostedAnswersWithoutSearchEvidence(t *testing.T) {
	for _, tt := range []struct{ protocol, body string }{
		{"anthropic", `{"content":[{"type":"text","text":"An answer without sources"}],"stop_reason":"end_turn"}`},
		{"gemini", `{"candidates":[{"content":{"role":"model","parts":[{"text":"An answer without sources"}]},"finishReason":"STOP"}]}`},
	} {
		t.Run(tt.protocol, func(t *testing.T) {
			p := newWebSearchFixture(t, tt.protocol, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			})
			sources, err := p.SearchWeb(t.Context(), "fixture", "query", 1)
			if len(sources) != 0 || err == nil || !strings.Contains(err.Error(), "did not return search sources") {
				t.Fatalf("ordinary answer counted as search evidence: sources=%v, err=%v", sources, err)
			}
		})
	}
}

func TestWebSearchRejectsUnsafeSourcesAndCapsText(t *testing.T) {
	p := newWebSearchFixture(t, "ollama", func(w http.ResponseWriter, r *http.Request) {
		rows := []map[string]string{
			{"url": "file:///etc/passwd"},
			{"url": "https://user:password@example.com/"},
			{"url": "https://example.com/path\ncontrol"},
			{"url": "https:///missing-host"},
			{"url": "https://example.com/" + strings.Repeat("x", 8<<10)},
			{"url": "https://SITE.example/docs#one", "title": strings.Repeat("x", 513), "content": "a\t" + strings.Repeat("€", 299)},
			{"url": "https://site.example/docs#two", "title": "Duplicate"},
			{"url": "http://next.example/", "title": "   "},
			{"url": "https://not-returned.example/"},
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"results": rows}); err != nil {
			t.Error(err)
		}
	})
	sources, err := p.SearchWeb(t.Context(), "", "query", 2)
	if err != nil || len(sources) != 2 || sources[0].URL != "https://SITE.example/docs#one" || sources[0].Title != "site.example" ||
		sources[1] != (WebSearchSource{URL: "http://next.example/", Title: "next.example"}) {
		t.Fatalf("unexpected search results: %+v, err=%v", sources, err)
	}
	if snippet := sources[0].Snippet; !utf8.ValidString(snippet) || snippet != "a "+strings.Repeat("€", 199) {
		t.Fatalf("snippet was not collapsed and capped at a rune boundary: length=%d", len(snippet))
	}
}

func TestWebSearchDoesNotRetryOrRedirect(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			p := newWebSearchFixture(t, "ollama", func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/next" {
					_, _ = io.WriteString(w, `{"results":[]}`)
					return
				}
				w.Header().Set("Location", "/next")
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "secret echo in response body")
			})
			_, err := p.SearchWeb(t.Context(), "", "query", 6)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) || strings.Contains(err.Error(), "secret echo") || requests.Load() != 1 {
				t.Fatalf("status=%d, requests=%d, error=%v", status, requests.Load(), err)
			}
		})
	}
}

func TestWebSearchRefusesReplayAndOAuth(t *testing.T) {
	for _, name := range []string{"static idempotency header", "replay auth header", "OAuth credentials", "unsupported protocol"} {
		t.Run(name, func(t *testing.T) {
			p := newWebSearchFixture(t, "openai-responses", func(w http.ResponseWriter, r *http.Request) {
				t.Error("sent an unsupported search request")
			})
			var reason string
			switch name {
			case "static idempotency header":
				p.headers["x-Idempotency-KEY"] = "avoid-replay"
				reason = "idempotency header"
			case "replay auth header":
				p.authType, p.authHeader = AuthCustomHeader, "Idempotency-Key"
				reason = "idempotency header"
			case "OAuth credentials":
				p.SetOAuth(&OAuthCredentials{AccessToken: "never-send"})
				reason = "Could not use the account key"
			case "unsupported protocol":
				p.StandardName = "codex"
				reason = "cannot search the web"
			}
			if _, err := p.SearchWeb(t.Context(), "fixture", "query", 3); err == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("search was not refused for %s: %v", name, err)
			}
		})
	}
}

func TestWebSearchRespectsAccountRevocation(t *testing.T) {
	var requests atomic.Int32
	p := newWebSearchFixture(t, "ollama", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer bound-key" {
			t.Errorf("used the wrong account key: %q", got)
		}
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	account := Account{ID: p.AccountID, ProviderID: p.ID, APIKey: "bound-key",
		Connection: Template{APIStandard: p.StandardName, ProviderURL: p.BaseURL, AuthType: AuthProtocol}}
	manager := NewAccountManager(map[string]Account{account.ID: account}, nil)
	if err := manager.Bind(p, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SearchWeb(t.Context(), "", "query", 1); err != nil {
		t.Fatal(err)
	}
	manager.Replace(nil)
	if _, err := p.SearchWeb(t.Context(), "", "query", 1); err == nil || requests.Load() != 1 {
		t.Fatalf("search used credentials after revocation: err=%v, requests=%d", err, requests.Load())
	}
	if got, ok := p.WebSearchObservation(""); !ok || got.Working || got.Detail == "" {
		t.Fatalf("revocation did not replace the prior success: observation=%+v, present=%v", got, ok)
	}
}

func TestWebSearchValidatesInputAndBoundsResponse(t *testing.T) {
	var requests atomic.Int32
	p := newWebSearchFixture(t, "openai-responses", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// A syntactically valid response, padded over the 1 MiB read ceiling.
		_, _ = io.WriteString(w, responsesWebSearchWire+strings.Repeat(" ", 1<<20))
	})
	for _, tt := range []struct {
		model, query string
		limit        int
	}{
		{"", "query", 1}, {" leading", "query", 1}, {"broken\tmodel", "query", 1}, {"\xff", "query", 1}, {strings.Repeat("x", 1025), "query", 1},
		{"valid", " \n ", 1}, {"valid", "\x00", 1}, {"valid", "\xff", 1}, {"valid", strings.Repeat("q", 4097), 1},
		{"valid", "query", 0}, {"valid", "query", 11},
	} {
		if _, err := p.SearchWeb(t.Context(), tt.model, tt.query, tt.limit); err == nil {
			t.Errorf("invalid request accepted (model bytes=%d, query bytes=%d, limit=%d)", len(tt.model), len(tt.query), tt.limit)
		}
	}
	if attempts := requests.Load(); attempts != 0 {
		t.Fatalf("made %d requests with invalid arguments", attempts)
	}
	if sources, err := p.SearchWeb(t.Context(), "valid", strings.Repeat("q", 4096), 10); err == nil || !strings.Contains(err.Error(), "Could not read") || requests.Load() != 1 || len(sources) != 0 {
		t.Fatalf("response limit not enforced: sources=%v, err=%v, requests=%d", sources, err, requests.Load())
	}
}

func TestWebSearchCallerCancellationLeavesNoObservation(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	p := newWebSearchFixture(t, "ollama", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"results":`)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
		close(exited)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.SearchWeb(ctx, "", "query", 1)
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("search request did not reach the server")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error did not preserve caller cancellation: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler was not canceled")
	}
	if observation, ok := p.WebSearchObservation(""); ok {
		t.Fatalf("canceled search left an observation: %+v", observation)
	}
}

func TestWebSearchRecordsDeadline(t *testing.T) {
	p := newWebSearchFixture(t, "ollama", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	p.dial.RequestTimeoutSeconds = 1
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := p.SearchWeb(ctx, "", "query", 1)
	const detail = "The search did not finish in time."
	if !errors.Is(err, context.DeadlineExceeded) || err.Error() != detail || ctx.Err() != nil {
		t.Fatalf("error did not reflect account deadline: search=%v, caller=%v", err, ctx.Err())
	}
	first, ok := p.WebSearchObservation("")
	if !ok || first.Working || first.Detail != detail || first.Results != 0 || first.At.IsZero() || first.Latency <= 0 {
		t.Fatalf("wrong deadline observation: %+v, present = %v", first, ok)
	}
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, err := p.SearchWeb(expired, "", "query", 1); !errors.Is(err, context.DeadlineExceeded) || err.Error() != detail {
		t.Fatalf("error did not reflect caller deadline: %v", err)
	}
	if got, ok := p.WebSearchObservation(""); !ok || got.Working || got.Detail != detail || got.At.Before(first.At) || got.Latency >= first.Latency {
		t.Fatalf("caller deadline did not record an observation: %+v, present = %v", got, ok)
	}
}

func TestWebSearchObservationScopeAndEviction(t *testing.T) {
	p := newWebSearchFixture(t, "openai-responses", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, responsesWebSearchWire)
	})
	if _, err := p.SearchWeb(t.Context(), "searched", "query", 1); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, scopeURL, account, model string
		want                           bool
	}{
		{"new provider with the same route", p.BaseURL, p.AccountID, "searched", true},
		{"different account", p.BaseURL, "other-account", "searched", false},
		{"edited API base", p.BaseURL + "changed", p.AccountID, "searched", false},
		{"different model", p.BaseURL, p.AccountID, "not-searched", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			observing := &Provider{ID: p.ID, BaseURL: tt.scopeURL, StandardName: p.StandardName, AccountID: tt.account}
			if got, ok := observing.WebSearchObservation(tt.model); ok != tt.want || (ok && !got.Working) {
				t.Fatalf("wrong scoped observation: %+v, present = %v", got, ok)
			}
		})
	}
	for i := range webSearchObservationLimit {
		// Use a validation failure to fill the process cache without HTTP I/O.
		if _, err := p.SearchWeb(t.Context(), fmt.Sprintf("model-%d", i), "", 1); err == nil {
			t.Fatal("blank query was not refused")
		}
	}
	if _, ok := p.WebSearchObservation("searched"); ok {
		t.Fatal("oldest observation was not evicted")
	}
	for _, model := range []string{"model-0", "model-511"} {
		if got, ok := p.WebSearchObservation(model); !ok || got.Working || !strings.Contains(got.Detail, "non-empty search query") {
			t.Fatalf("current failure observation = %+v, present = %v", got, ok)
		}
	}
}

func TestWebSearchEvictsOldestOutcomeWhenTimesMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var store webSearchObservationStore
		started := time.Now()
		key := func(i int) webSearchObservationKey {
			return webSearchObservationKey{model: fmt.Sprint(i)}
		}
		for i := range webSearchObservationLimit {
			store.record(key(i), started, i, nil)
		}
		store.record(key(0), started, 42, nil)
		store.record(key(webSearchObservationLimit), started, 1, nil)
		if _, ok := store.get(key(1)); ok {
			t.Fatal("did not evict the least recently recorded outcome")
		}
		if got, ok := store.get(key(0)); !ok || got.Results != 42 || !got.At.Equal(started) {
			t.Fatalf("refreshed outcome was lost: %+v, present = %v", got, ok)
		}
	})
}
