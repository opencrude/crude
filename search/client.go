package search

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/logger"
	"crude/network"
)

// maxResponseBytes bounds every provider response read into memory.
const maxResponseBytes = 8 << 20

// pageCacheBytes bounds the fetched documents one client keeps for pagination.
const pageCacheBytes = 32 << 20

// Leave time for transport projection inside the ordinary 30-second MCP call.
const operationTimeout = 24 * time.Second

// Lookup resolves an optional provider setting such as an API key or instance
// URL. An empty result means the provider is not configured.
type Lookup func(name string) string

// Client runs web search and fetch through the providers its lookup configures.
// Keyed providers are tried in quality order and the keyless engines are the
// fallback, so search keeps working when a key is missing, revoked or rate
// limited.
type Client struct {
	// resultBudget bounds one returned result after JSON encoding; 0 is
	// unbounded.
	resultBudget int
	lookup       Lookup
	pages        *pageCache
	endpoints    endpoints
	keyless      func(context.Context, string, int) ([]SearchResult, []string, map[string]string)
	fetchMu      sync.Mutex
	fetching     map[string]*pageFlight
}

// endpoints are the keyed providers' API roots; tests point them at fakes.
type endpoints struct {
	exa, brave, tavily, jina string
}

var defaultEndpoints = endpoints{
	exa: "https://api.exa.ai", brave: "https://api.search.brave.com", tavily: "https://api.tavily.com", jina: "https://r.jina.ai",
}

// SetResultBudget bounds each result to fit a transport that carries at most
// this many JSON-encoded bytes.
func (client *Client) SetResultBudget(bytes int) { client.resultBudget = bytes }

func New(lookup Lookup) *Client {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	return &Client{lookup: lookup, pages: newPageCache(pageCacheBytes), endpoints: defaultEndpoints, keyless: keylessSearch}
}

type keyedEngine struct {
	name   string
	search func(context.Context, string, int) ([]SearchResult, error)
}

// keyedEngines returns the configured paid/self-hosted providers in quality
// order. Each costs money or quota per query, so they are tried one at a time.
func (client *Client) keyedEngines() []keyedEngine {
	var engines []keyedEngine
	if key := client.lookup("EXA_API_KEY"); key != "" {
		engines = append(engines, keyedEngine{"exa", func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
			return exaSearch(ctx, client.endpoints.exa, key, query, limit)
		}})
	}
	if key := client.lookup("BRAVE_SEARCH_API_KEY"); key != "" {
		engines = append(engines, keyedEngine{"brave-api", func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
			return braveAPISearch(ctx, client.endpoints.brave, key, query, limit)
		}})
	}
	if key := client.lookup("TAVILY_API_KEY"); key != "" {
		engines = append(engines, keyedEngine{"tavily", func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
			return tavilySearch(ctx, client.endpoints.tavily, key, query, limit)
		}})
	}
	if base := client.lookup("SEARXNG_URL"); base != "" {
		engines = append(engines, keyedEngine{"searxng", func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
			return searxngSearch(ctx, base, query, limit)
		}})
	}
	return engines
}

// Search returns formatted results for query.
func (client *Client) Search(ctx context.Context, query string, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("search: query is empty; pass the words to search for")
	}
	if limit <= 0 {
		limit = 6
	}
	start := time.Now()
	failed := make(map[string]string)
	engines := client.keyedEngines()
	deadline, _ := ctx.Deadline()
	// Preserve a keyless fallback even when every configured key is unusable.
	keyedDeadline := deadline.Add(-min(8*time.Second, time.Until(deadline)/2))
	for index, engine := range engines {
		budget := time.Until(keyedDeadline) / time.Duration(len(engines)-index)
		if budget <= 0 {
			break
		}
		attempt, stop := context.WithTimeout(ctx, min(8*time.Second, budget))
		results, err := engine.search(attempt, query, limit)
		stop()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			failed[engine.name] = err.Error()
			logIf(func(lg *logger.Logger) {
				lg.Warn("SEARCH", "engine_failed", "engine", engine.name, "error", err.Error())
			})
			continue
		}
		if results = deduplicateResults(results, limit); len(results) > 0 {
			logIf(func(lg *logger.Logger) {
				lg.Info("SEARCH", "search_ok", "engine", engine.name, "results", len(results), "latency_ms", time.Since(start).Milliseconds())
			})
			return formatResults(query, results, []string{engine.name}, failed), nil
		}
		failed[engine.name] = "no results"
	}
	fallback, stop := context.WithTimeout(ctx, 8*time.Second)
	results, succeeded, keylessFailed := client.keyless(fallback, query, limit)
	stop()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	for name, reason := range keylessFailed {
		failed[name] = reason
	}
	logIf(func(lg *logger.Logger) {
		lg.Info("SEARCH", "search_done", "results", len(results), "engines", len(succeeded), "latency_ms", time.Since(start).Milliseconds())
	})
	if len(results) == 0 {
		if len(succeeded) == 0 {
			if len(failed) == 0 {
				failed["search"] = "provider requests did not complete before the search deadline"
			}
			return "", errors.New(noResults(query, succeeded, failed))
		}
		return noResults(query, succeeded, failed), nil
	}
	return formatResults(query, results, succeeded, failed), nil
}

func formatResults(query string, results []SearchResult, succeeded []string, failed map[string]string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Web search results for: %s\n", query)
	if len(succeeded) > 0 {
		fmt.Fprintf(&sb, "(Sources: %s)\n", strings.Join(succeeded, ", "))
	}
	if len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for name := range failed {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(&sb, "(Unavailable: %s)\n", strings.Join(names, ", "))
	}
	sb.WriteString("\n")
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Description != "" {
			desc := strings.Join(strings.Fields(r.Description), " ")
			if runes := []rune(desc); len(runes) > 300 {
				desc = string(runes[:300]) + "…"
			}
			fmt.Fprintf(&sb, "   %s\n", desc)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func noResults(query string, succeeded []string, failed map[string]string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "No results found for: %s\n\n", query)
	if len(succeeded) == 0 && len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for name := range failed {
			names = append(names, name)
		}
		sort.Strings(names)
		sb.WriteString("Every search provider failed:\n")
		for _, name := range names {
			fmt.Fprintf(&sb, "  - %s: %s\n", name, failed[name])
		}
		sb.WriteString("\nRetry in a moment, try a simpler query, or use web_fetch on a URL you already know.")
		return sb.String()
	}
	sb.WriteString("Try different or more specific keywords, remove quotes or operators, or search a related topic.")
	return sb.String()
}

// postJSON sends one provider request and decodes a bounded JSON response.
func postJSON(ctx context.Context, provider, method, endpoint string, headers map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	client := network.NewClient(30 * time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return redactRequestURL(err)
	}
	defer resp.Body.Close()
	data, err := readResponse(resp.Body, maxResponseBytes)
	if err != nil {
		return redactRequestURL(err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s rejected the configured key (HTTP %d); update it in Settings → MCP → Web", provider, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%s rate limit or quota reached (HTTP 429)", provider)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("%s returned HTTP %d", provider, resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s returned an unreadable response: %w", provider, err)
	}
	return nil
}

func exaSearch(ctx context.Context, base, key, query string, limit int) ([]SearchResult, error) {
	var response struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Highlights []string `json:"highlights"`
			Summary    string   `json:"summary"`
			Text       string   `json:"text"`
		} `json:"results"`
	}
	request := map[string]any{"query": query, "type": "auto", "numResults": limit, "contents": map[string]any{"highlights": true}}
	if err := postJSON(ctx, "Exa", "POST", base+"/search", map[string]string{"x-api-key": key}, request, &response); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(response.Results))
	for _, item := range response.Results {
		description := strings.Join(item.Highlights, " … ")
		if description == "" {
			description = item.Summary
		}
		if description == "" {
			description = item.Text
		}
		results = append(results, SearchResult{Title: firstNonEmpty(item.Title, item.URL), URL: item.URL, Description: description})
	}
	return results, nil
}

func braveAPISearch(ctx context.Context, base, key, query string, limit int) ([]SearchResult, error) {
	var response struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	endpoint := base + "/res/v1/web/search?" + url.Values{"q": {query}, "count": {fmt.Sprint(min(limit, 20))}}.Encode()
	if err := postJSON(ctx, "Brave Search API", "GET", endpoint, map[string]string{"X-Subscription-Token": key}, nil, &response); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(response.Web.Results))
	for _, item := range response.Web.Results {
		results = append(results, SearchResult{Title: item.Title, URL: item.URL, Description: stripTags(item.Description)})
	}
	return results, nil
}

func tavilySearch(ctx context.Context, base, key, query string, limit int) ([]SearchResult, error) {
	var response struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	request := map[string]any{"query": query, "max_results": limit}
	if err := postJSON(ctx, "Tavily", "POST", base+"/search", map[string]string{"Authorization": "Bearer " + key}, request, &response); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(response.Results))
	for _, item := range response.Results {
		results = append(results, SearchResult{Title: item.Title, URL: item.URL, Description: item.Content})
	}
	return results, nil
}

func searxngSearch(ctx context.Context, base, query string, limit int) ([]SearchResult, error) {
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/search")
	if err != nil || endpoint.Host == "" || endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return nil, errors.New("SearXNG URL must be an http(s) base URL")
	}
	endpoint.RawQuery = url.Values{"q": {query}, "format": {"json"}}.Encode()
	var response struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := postJSON(ctx, "SearXNG", "GET", endpoint.String(), nil, nil, &response); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, min(limit, len(response.Results)))
	for _, item := range response.Results {
		if len(results) == limit {
			break
		}
		results = append(results, SearchResult{Title: item.Title, URL: item.URL, Description: item.Content})
	}
	return results, nil
}

func stripTags(text string) string {
	var sb strings.Builder
	inside := false
	for _, r := range text {
		switch {
		case r == '<':
			inside = true
		case r == '>':
			inside = false
		case !inside:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// pageCache keeps whole extracted documents so paginated reads of one URL do
// not refetch it. It is bounded by content bytes and evicts least recently used.
type pageCache struct {
	mu    sync.Mutex
	limit int
	used  int
	order *list.List
	items map[string]*list.Element
}

type cachedPage struct {
	key  string
	page WebPage
}

func newPageCache(limit int) *pageCache {
	return &pageCache{limit: limit, order: list.New(), items: make(map[string]*list.Element)}
}

func (cache *pageCache) get(key string) (WebPage, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, exists := cache.items[key]
	if !exists {
		return WebPage{}, false
	}
	cache.order.MoveToFront(element)
	return element.Value.(*cachedPage).page, true
}

func (cache *pageCache) put(key string, page WebPage) {
	size := len(page.Content)
	if size > cache.limit {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, exists := cache.items[key]; exists {
		cache.used -= len(element.Value.(*cachedPage).page.Content)
		cache.order.Remove(element)
		delete(cache.items, key)
	}
	for cache.used+size > cache.limit && cache.order.Len() > 0 {
		oldest := cache.order.Back()
		entry := oldest.Value.(*cachedPage)
		cache.used -= len(entry.page.Content)
		cache.order.Remove(oldest)
		delete(cache.items, entry.key)
	}
	cache.items[key] = cache.order.PushFront(&cachedPage{key: key, page: page})
	cache.used += size
}
