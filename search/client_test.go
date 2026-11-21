package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func lookupOf(values map[string]string) Lookup {
	return func(name string) string { return values[name] }
}

func TestSearchRacesKeyedAndFreeProviders(t *testing.T) {
	var exaCalls, braveCalls atomic.Int32
	exaStarted, braveStarted, freeStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/exa/search"):
			exaCalls.Add(1)
			close(exaStarted)
			if r.Header.Get("x-api-key") != "exa-key" {
				t.Errorf("exa key header=%q", r.Header.Get("x-api-key"))
			}
			for _, started := range []chan struct{}{braveStarted, freeStarted} {
				select {
				case <-started:
				case <-r.Context().Done():
					t.Error("another provider did not start while Exa was running")
					return
				}
			}
			w.WriteHeader(http.StatusTooManyRequests)
		case strings.HasPrefix(r.URL.Path, "/brave/res/v1/web/search"):
			braveCalls.Add(1)
			close(braveStarted)
			select {
			case <-exaStarted:
			case <-r.Context().Done():
				t.Error("Exa did not start while Brave was running")
				return
			}
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
	client.keyless = []Method{{Name: "free", Search: func(context.Context, string, int) ([]SearchResult, error) {
		close(freeStarted)
		return []SearchResult{{Title: "Language specification", URL: "https://go.dev/ref/spec"}}, nil
	}}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	text, err := client.Search(ctx, "go generics", 5)
	if err != nil {
		t.Fatal(err)
	}
	if exaCalls.Load() != 1 || braveCalls.Load() != 1 {
		t.Fatalf("calls exa=%d brave=%d", exaCalls.Load(), braveCalls.Load())
	}
	for _, want := range []string{"(Sources: ", "free", "brave-api", "(Unavailable: exa)", "https://go.dev/doc/tutorial/generics", "Tutorial", "https://go.dev/ref/spec"} {
		if !strings.Contains(text, want) {
			t.Fatalf("result lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<strong>") || strings.Contains(text, "exa-key") {
		t.Fatalf("result leaked markup or key:\n%s", text)
	}
}

func TestSearchExplainsEachFailure(t *testing.T) {
	client := New(nil)
	client.keyless = []Method{searchReply("duckduckgo", false, 0, nil, errors.New("HTTP 503"))}
	extra := []Method{searchReply("Account Web", true, 0, nil, errors.New("quota reached"))}
	text, err := client.SearchWith(t.Context(), "anything", 3, extra)
	outage, ok := errors.AsType[*ProvidersFailed](err)
	if text != "" || !ok {
		t.Fatalf("provider outage must be a typed failure, got %q, %v", text, err)
	}
	for _, want := range []string{"Every search provider failed", "Account Web: quota reached", "duckduckgo: HTTP 503"} {
		if !strings.Contains(outage.Error(), want) {
			t.Fatalf("failure lacks %q: %v", want, outage)
		}
	}
	if _, err := client.Search(t.Context(), "  ", 3); err == nil || !strings.Contains(err.Error(), "query is empty") {
		t.Fatalf("empty query err=%v", err)
	}
}

func TestSearchCompletedEmptyIsNotAnOutage(t *testing.T) {
	client := New(nil)
	client.keyless = []Method{searchReply("Free Engine", false, 0, nil, errors.New("HTTP 503"))}
	extra := []Method{searchReply("Account Web", true, 0, []SearchResult{
		{Title: "Invalid scheme", URL: "ftp://example.test/bad"},
		{Title: "   ", URL: "https://example.test/no-title"},
		{Title: "Credentials", URL: "https://user:password@example.test/bad"},
	}, nil)}
	text, err := client.SearchWith(t.Context(), "go", 5, extra)
	if err != nil || !strings.Contains(text, "No results found for: go") ||
		!strings.Contains(text, "(Sources: Account Web)") || !strings.Contains(text, "(Unavailable: Free Engine)") ||
		strings.Contains(text, "Every search provider failed") || strings.Contains(text, "example.test") {
		t.Fatalf("completed empty search = %q, %v", text, err)
	}
}

func TestSearchAwaitsConfiguredMethodsAndFusesAgreement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := New(nil)
		client.keyless = []Method{searchReply("Free Engine", false, 0, []SearchResult{
			{Title: "Free rank one", URL: "https://example.test/solo"},
			{Title: "Free name", URL: "https://EXAMPLE.test/shared#free", Description: "Useful description from the free engine."},
		}, nil)}
		extra := []Method{
			searchReply("Account Two", true, 325*time.Millisecond, []SearchResult{
				{Title: "Best shared title", URL: "https://example.test/shared#account", Description: " "},
			}, nil),
			searchReply("Account One", true, 300*time.Millisecond, []SearchResult{
				{Title: "Account rank one", URL: "https://example.test/account"},
			}, nil),
		}
		text, err := client.SearchWith(t.Context(), "go", 2, extra)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"(Sources: Free Engine, Account One, Account Two)",
			"1. Best shared title\n   https://example.test/shared#account\n   Useful description from the free engine.",
			"2. Free rank one",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("result lacks %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "Account rank one") || strings.Contains(text, "Free name") {
			t.Fatalf("limit or deduplication failed:\n%s", text)
		}
	})
}

func TestSearchFusionUsesWholeDeduplicatedList(t *testing.T) {
	client := New(nil)
	client.keyless = nil
	text, err := client.SearchWith(t.Context(), "go", 2, []Method{
		searchReply("Page List A", true, 0, []SearchResult{
			{Title: "A", URL: "https://example.test/A"},
			{Title: "Duplicate A", URL: "https://EXAMPLE.test/A#duplicate"},
			{Title: "Different case", URL: "https://example.test/a"},
			{Title: "Shared", URL: "https://example.test/z"},
		}, nil),
		searchReply("Page List B", true, 0, []SearchResult{
			{Title: "Case-sensitive resource", URL: "https://EXAMPLE.test/a#b"},
			{Title: "B", URL: "https://example.test/B"},
			{Title: "Shared", URL: "https://EXAMPLE.test/z#b"},
		}, nil),
	})
	if err != nil || !strings.Contains(text, "1. Case-sensitive resource") || !strings.Contains(text, "2. Shared") ||
		strings.Contains(text, "\n3.") {
		t.Fatalf("fusion lost agreement past the per-method limit, counted a duplicate vote, or folded path case: %q, %v", text, err)
	}
}

func searchReply(name string, configured bool, after time.Duration, results []SearchResult, err error) Method {
	return Method{Name: name, Configured: configured, Search: func(ctx context.Context, _ string, _ int) ([]SearchResult, error) {
		if after > 0 {
			timer := time.NewTimer(after)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return results, err
	}}
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
