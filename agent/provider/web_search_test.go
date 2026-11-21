package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const webSearchWire = `{"status":"completed","output":[
	{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[
		{"type":"url","url":"https://official.example/docs#step"},
		{"type":"url","url":"https://OFFICIAL.example/docs"},
		{"type":"url","url":"https://reference.example/topics"},
		{"type":"url","url":"https://more.example/extra"}
	]}},
	{"type":"message","role":"assistant","content":[{"type":"output_text",
		"text":"Never publish generated prose or https://invented.example/",
		"annotations":[
			{"type":"url_citation","url":"https://official.example/docs#step","title":"Provider-backed title"},
			{"type":"url_citation","url":"https://invented.example/","title":"Unverified annotation"}
		]}]}
]}`

func newSearchWebFixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	client, err := NewClient(&OpenAIResponsesStandard{}, endpoint.URL+"/v1", "fixture-api-key", DialConfig{MaxAttempts: 9, MaxResponseBytes: 1 << 20},
		WithHTTPClient(endpoint.Client()),
		WithRequestExtras(map[string]any{"previous_response_id": "private-chat", "input": "hidden conversation", "store": true}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestWebSearchFallbackWireHasOnlyQueryAndTypedSearch(t *testing.T) {
	var requests atomic.Int32
	query := "literal: weather in Montréal\nNo chat context."
	client := newSearchWebFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-api-key" {
			t.Errorf("wrong route or auth: %s %s", r.Method, r.URL)
		}
		var received map[string]any
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		want := map[string]any{
			"model": "exact-selected-id", "input": query, "store": false,
			"tools":             []any{map[string]any{"type": "web_search", "search_context_size": "low"}},
			"tool_choice":       map[string]any{"type": "web_search"},
			"include":           []any{"web_search_call.action.sources"},
			"max_tool_calls":    float64(1),
			"max_output_tokens": float64(1024),
		}
		if !reflect.DeepEqual(received, want) {
			t.Errorf("unexpected model request: %+v", received)
		}
		_, _ = fmt.Fprint(w, webSearchWire)
	})
	sources, err := client.searchWebOnce(t.Context(), "exact-selected-id", query, 2)
	want := []WebSearchSource{
		{URL: "https://official.example/docs#step", Title: "Provider-backed title"},
		{URL: "https://reference.example/topics", Title: "reference.example"},
	}
	if err != nil || requests.Load() != 1 || !reflect.DeepEqual(sources, want) {
		t.Fatalf("wrong source evidence: %v, err=%v requests=%d", sources, err, requests.Load())
	}
}

func TestWebSearchFallbackRejectsUnverifiedSources(t *testing.T) {
	for _, test := range []struct {
		name, reply string
		fail        bool
	}{
		{"generated prose plus annotation", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"https://invented.example","annotations":[{"type":"url_citation","url":"https://invented.example","title":"invented"}]}]}]}`, true},
		{"failed search", strings.Replace(webSearchWire, `"web_search_call","status":"completed"`, `"web_search_call","status":"failed"`, 1), true},
		{"incomplete search", strings.Replace(webSearchWire, `"web_search_call","status":"completed"`, `"web_search_call","status":"in_progress"`, 1), true},
		{"incomplete output capped", strings.Replace(webSearchWire, `"status":"completed","output"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output"`, 1), false},
		{"content filter", strings.Replace(webSearchWire, `"status":"completed","output"`, `"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output"`, 1), true},
		{"empty real search", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[]}}]}`, false},
		{"missing sources", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search"}}]}`, true},
		{"invalid destinations", `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[{"type":"url","url":"file:///secret"},{"type":"url","url":"https://user:password@example.com"},{"type":"url","url":"https://example.com\nnot-a-link"}]}}]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newSearchWebFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, test.reply) })
			results, err := client.searchWebOnce(t.Context(), "fixture", "query", 10)
			if (err != nil) != test.fail || (test.fail || test.name == "empty real search") && len(results) != 0 {
				t.Fatalf("unexpected results=%v, error=%v", results, err)
			}
		})
	}
}

func TestWebSearchFallbackDoesNotRetryOrRedirect(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			client := newSearchWebFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/next" {
					t.Error("redirect replayed the model request")
				}
				w.Header().Set("Location", "/next")
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, "secret-echo-in-provider-body")
			})
			_, err := client.searchWebOnce(t.Context(), "fixture", "query", 6)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) ||
				strings.Contains(err.Error(), "secret-echo") || requests.Load() != 1 {
				t.Fatalf("status=%d requests=%d error=%v", status, requests.Load(), err)
			}
		})
	}
}

func TestWebSearchFallbackUsesCallerCancellation(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	client := newSearchWebFixture(t, func(_ http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		close(entered)
		<-r.Context().Done()
		close(exited)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.searchWebOnce(ctx, "fixture", "query", 3)
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("HTTP attempt did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled operation did not return the caller's cause: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler was not cancelled")
	}
}

func TestWebSearchFallbackDispatchRejectsGatewayAndNonResponses(t *testing.T) {
	for _, test := range []struct {
		base, protocol, auth string
		valid                bool
	}{
		{"https://api.openai.com/v1", "openai-responses", AuthProtocol, true},
		{"https://api.openai.com/v1/", "responses", AuthBearer, true},
		{"http://api.openai.com/v1", "openai-responses", AuthProtocol, false},
		{"https://api.openai.com/v1/?key=fixture", "openai-responses", AuthProtocol, false},
		{"https://someone@api.openai.com/v1", "openai-responses", AuthProtocol, false},
		{"https://api.openai.com.example/v1", "openai-responses", AuthProtocol, false},
		{"https://api.openai.com/v1", "openai", AuthProtocol, false},
		{"https://api.openai.com/v1", "openai-responses", AuthChatGPT, false},
	} {
		if got := SupportsWebSearchFallback(Template{ProviderURL: test.base, APIStandard: test.protocol, AuthType: test.auth}); got != test.valid {
			t.Errorf("connection %+v support=%t", test, got)
		}
		if !test.valid {
			p := &Provider{BaseURL: test.base, StandardName: test.protocol, authType: test.auth}
			if _, err := p.SearchWebOnce(t.Context(), "fixture", "query", 3); err == nil || !strings.Contains(err.Error(), "OpenAI Responses account") {
				t.Errorf("unsupported route dispatched: %+v, err=%v", test, err)
			}
		}
	}
}
