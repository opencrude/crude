package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func lookupOf(values map[string]string) Lookup {
	return func(name string) string { return values[name] }
}

func TestSearchPrefersKeyedProviderAndFallsBackInOrder(t *testing.T) {
	var exaCalls, braveCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/exa/search"):
			exaCalls.Add(1)
			if r.Header.Get("x-api-key") != "exa-key" {
				t.Errorf("exa key header=%q", r.Header.Get("x-api-key"))
			}
			w.WriteHeader(http.StatusTooManyRequests)
		case strings.HasPrefix(r.URL.Path, "/brave/res/v1/web/search"):
			braveCalls.Add(1)
			if r.Header.Get("X-Subscription-Token") != "brave-key" || r.URL.Query().Get("q") != "go generics" {
				t.Errorf("brave request=%v %v", r.Header, r.URL)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"web": map[string]any{"results": []any{
				map[string]any{"title": "Generics", "url": "https://go.dev/doc/tutorial/generics", "description": "<strong>Tutorial</strong>"},
			}}})
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()
	client := New(lookupOf(map[string]string{"EXA_API_KEY": "exa-key", "BRAVE_SEARCH_API_KEY": "brave-key"}))
	client.endpoints = endpoints{exa: server.URL + "/exa", brave: server.URL + "/brave"}
	client.keyless = func(context.Context, string, int) ([]SearchResult, []string, map[string]string) {
		t.Error("keyless engines must not run when a keyed provider answered")
		return nil, nil, nil
	}
	text, err := client.Search(t.Context(), "go generics", 5)
	if err != nil {
		t.Fatal(err)
	}
	if exaCalls.Load() != 1 || braveCalls.Load() != 1 {
		t.Fatalf("calls exa=%d brave=%d", exaCalls.Load(), braveCalls.Load())
	}
	for _, want := range []string{"Sources: brave-api", "Unavailable: exa", "https://go.dev/doc/tutorial/generics", "Tutorial"} {
		if !strings.Contains(text, want) {
			t.Fatalf("result lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<strong>") || strings.Contains(text, "exa-key") {
		t.Fatalf("result leaked markup or key:\n%s", text)
	}
}

func TestSearchWithoutKeysUsesKeylessEnginesAndExplainsTotalFailure(t *testing.T) {
	client := New(nil)
	client.keyless = func(context.Context, string, int) ([]SearchResult, []string, map[string]string) {
		return nil, nil, map[string]string{"duckduckgo": "HTTP 503"}
	}
	_, err := client.Search(t.Context(), "anything", 3)
	if err == nil {
		t.Fatal("provider outage must be a tool failure, not a successful empty search")
	}
	text := err.Error()
	if !strings.Contains(text, "Every search provider failed") || !strings.Contains(text, "duckduckgo: HTTP 503") {
		t.Fatalf("failure text=%s", text)
	}
	if _, err := client.Search(t.Context(), "  ", 3); err == nil || !strings.Contains(err.Error(), "query is empty") {
		t.Fatalf("empty query err=%v", err)
	}
}

func TestFetchPaginatesFromCacheAndFallsBackToReader(t *testing.T) {
	var direct, reader atomic.Int32
	body := strings.Repeat("abcdefghij", 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/page":
			direct.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/blocked":
			direct.Add(1)
			w.WriteHeader(http.StatusForbidden)
		case strings.HasPrefix(r.URL.Path, "/exa/contents"):
			reader.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"url": "https://x.test/blocked", "title": "Blocked", "text": "reader text"}}})
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()
	client := New(lookupOf(map[string]string{"EXA_API_KEY": "k"}))
	client.endpoints.exa = server.URL + "/exa"
	first, err := client.Fetch(t.Context(), server.URL+"/page", 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Fetch(t.Context(), server.URL+"/page", 31, 30)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Load() != 1 {
		t.Fatalf("paginated reads refetched the page %d times", direct.Load())
	}
	if !strings.Contains(first, "Characters 1–30 of 100; pass offset=31") || !strings.Contains(second, "Characters 31–60 of 100") {
		t.Fatalf("pagination:\n%s\n---\n%s", first, second)
	}
	blocked, err := client.Fetch(t.Context(), server.URL+"/blocked", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reader.Load() != 1 || !strings.Contains(blocked, "Read via: Exa") || !strings.Contains(blocked, "reader text") {
		t.Fatalf("reader fallback:\n%s", blocked)
	}
}

func TestPageCacheEvictsLeastRecentlyUsedWithinBytes(t *testing.T) {
	cache := newPageCache(10)
	cache.put("a", WebPage{Content: "aaaa"})
	cache.put("b", WebPage{Content: "bbbb"})
	cache.get("a")
	cache.put("c", WebPage{Content: "cccc"})
	if _, ok := cache.get("b"); ok {
		t.Fatal("least recently used page survived")
	}
	if _, ok := cache.get("a"); !ok {
		t.Fatal("recently used page was evicted")
	}
	cache.put("huge", WebPage{Content: strings.Repeat("x", 11)})
	if _, ok := cache.get("huge"); ok || cache.used > cache.limit {
		t.Fatalf("oversized page cached; used=%d", cache.used)
	}
}

func TestFetchFitsTheTransportBudgetAndOffersContinuation(t *testing.T) {
	body := strings.Repeat("ü\"line\n", 50_000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := New(nil)
	client.SetResultBudget(64 << 10)
	text, err := client.Fetch(t.Context(), server.URL, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if encodedLen(text) > 64<<10 {
		t.Fatalf("result encodes to %d bytes, over the budget", encodedLen(text))
	}
	if !strings.Contains(text, "pass offset=") || !strings.Contains(text, "of 350000") {
		t.Fatalf("truncated page lacks a continuation: %.200s", text)
	}
}
