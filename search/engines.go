package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"crude/logger"
	"crude/network"

	"github.com/PuerkitoBio/goquery"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	pkgLogger   *logger.Logger
	pkgLoggerMu sync.RWMutex
)

func SetLogger(lg *logger.Logger) {
	pkgLoggerMu.Lock()
	defer pkgLoggerMu.Unlock()
	pkgLogger = lg
}

func logIf(fn func(lg *logger.Logger)) {
	pkgLoggerMu.RLock()
	lg := pkgLogger
	pkgLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

// SearchResult represents a single web search result from any engine.
type SearchResult struct {
	Title       string
	URL         string
	Description string
}

// keylessSearch races independent providers. A stalled provider cannot hold
// usable results hostage until the enclosing MCP call times out.
func keylessSearch(ctx context.Context, query string, limit int) ([]SearchResult, []string, map[string]string) {
	engines := []keyedEngine{
		{"brave", searchBrave},
		{"duckduckgo", searchDuckDuckGo},
		{"startpage", searchStartpage},
		{"exa", func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
			return searchExaMCP(ctx, "https://mcp.exa.ai/mcp", query, limit)
		}},
	}
	return runKeylessSearch(ctx, engines, query, limit)
}

func runKeylessSearch(ctx context.Context, engines []keyedEngine, query string, limit int) ([]SearchResult, []string, map[string]string) {
	ctx, cancel := context.WithCancel(ctx)
	type engineResult struct {
		index   int
		results []SearchResult
		err     error
	}
	ch := make(chan engineResult, len(engines))
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	for index, engine := range engines {
		workers.Add(1)
		go func() {
			defer workers.Done()
			start := time.Now()
			results, err := engine.search(ctx, query, limit)
			logIf(func(lg *logger.Logger) {
				if err != nil {
					lg.Debug("SEARCH", "engine_failed", "engine", engine.name, "latency_ms", time.Since(start).Milliseconds(), "error", redactRequestURL(err).Error())
				} else {
					lg.Debug("SEARCH", "engine_ok", "engine", engine.name, "results", len(results), "latency_ms", time.Since(start).Milliseconds())
				}
			})
			ch <- engineResult{index: index, results: results, err: err}
		}()
	}
	answers := make([][]SearchResult, len(engines))
	var succeeded []string
	failed := make(map[string]string)
	var grace <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	collect := func() []SearchResult {
		var all []SearchResult
		for _, answer := range answers {
			all = append(all, answer...)
		}
		return deduplicateResults(all, limit)
	}
	for range engines {
		select {
		case <-ctx.Done():
			return collect(), succeeded, failed
		case <-grace:
			return collect(), succeeded, failed
		case r := <-ch:
			name := engines[r.index].name
			if r.err != nil {
				failed[name] = redactRequestURL(r.err).Error()
				continue
			}
			succeeded = append(succeeded, name)
			answers[r.index] = r.results
			if results := collect(); len(results) >= limit {
				return results, succeeded, failed
			} else if len(results) > 0 && timer == nil {
				// Briefly collect other ready results, never wait a full provider
				// deadline just to fill the requested maximum.
				timer = time.NewTimer(150 * time.Millisecond)
				grace = timer.C
			}
		}
	}
	return collect(), succeeded, failed
}

func deduplicateResults(results []SearchResult, limit int) []SearchResult {
	seen := make(map[string]bool)
	var deduped []SearchResult

	for _, r := range results {
		normalizedTitle := strings.ToLower(strings.TrimSpace(r.Title))
		parsed, err := url.Parse(strings.TrimSpace(r.URL))
		if normalizedTitle == "" || err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.User != nil {
			continue
		}
		parsed.Fragment, parsed.RawFragment = "", ""
		parsed.Host = strings.ToLower(parsed.Host)
		k := parsed.String()
		if !seen[k] {
			seen[k] = true
			deduped = append(deduped, r)
		}

		if len(deduped) >= limit {
			break
		}
	}

	return deduped
}

// Search Engine Implementations

func searchBrave(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	encodedQuery := url.QueryEscape(query)
	reqURL := fmt.Sprintf("https://search.brave.com/search?q=%s&source=web", encodedQuery)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/112.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := network.NewClient(15 * time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave returned status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	seen := make(map[string]bool)

	doc.Find(".result, .snippet").Each(func(i int, s *goquery.Selection) {
		if len(results) >= limit {
			return
		}

		titleEl := s.Find("a.title, .result-header a, a[href]").First()
		title := strings.TrimSpace(titleEl.Text())
		href, _ := titleEl.Attr("href")

		if title == "" || len(title) < 3 || href == "" || !strings.HasPrefix(href, "http") {
			return
		}

		if seen[href] {
			return
		}
		seen[href] = true

		description := strings.TrimSpace(s.Find(".snippet-description, .result-description, p.description, p.snippet").First().Text())

		results = append(results, SearchResult{
			Title:       title,
			URL:         href,
			Description: description,
		})
	})

	return results, nil
}

func searchDuckDuckGo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	encodedQuery := url.QueryEscape(query)
	urlStr := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", encodedQuery)

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/112.0.0.0 Safari/537.36")

	client := network.NewClient(15 * time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo returned status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}

	var results []SearchResult

	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		if len(results) >= limit {
			return
		}

		titleEl := s.Find(".result__a").First()
		title := strings.TrimSpace(titleEl.Text())
		href, _ := titleEl.Attr("href")
		description := strings.TrimSpace(s.Find(".result__snippet").First().Text())

		if title == "" {
			return
		}

		href = decodeDDGURL(href)

		results = append(results, SearchResult{
			Title:       title,
			URL:         href,
			Description: description,
		})
	})

	return results, nil
}

func decodeDDGURL(rawURL string) string {
	if idx := strings.Index(rawURL, "uddg="); idx >= 0 {
		rawURL = rawURL[idx+5:]
		if end := strings.Index(rawURL, "&"); end >= 0 {
			rawURL = rawURL[:end]
		}
		decoded, err := url.QueryUnescape(rawURL)
		if err == nil {
			return decoded
		}
	}
	return rawURL
}

func searchStartpage(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	encodedQuery := url.QueryEscape(query)
	reqURL := fmt.Sprintf("https://www.startpage.com/do/search?query=%s", encodedQuery)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := network.NewClient(15 * time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("startpage returned status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	seen := make(map[string]bool)

	doc.Find("a.result-title.result-link[href]").Each(func(i int, s *goquery.Selection) {
		if len(results) >= limit {
			return
		}

		href, _ := s.Attr("href")
		if href == "" || seen[href] {
			return
		}
		seen[href] = true

		title := strings.TrimSpace(s.Find("h2").First().Text())
		if title == "" {
			title = strings.TrimSpace(s.Text())
		}
		description := strings.TrimSpace(s.SiblingsFiltered("p.description, p.desc").First().Text())

		if title == "" {
			return
		}

		results = append(results, SearchResult{
			Title:       title,
			URL:         href,
			Description: description,
		})
	})

	if len(results) == 0 {
		doc.Find(".result, .search-result").Each(func(i int, s *goquery.Selection) {
			if len(results) >= limit {
				return
			}

			link := s.Find("a[href]").First()
			href, _ := link.Attr("href")
			title := strings.TrimSpace(link.Text())

			if title == "" || href == "" || seen[href] {
				return
			}
			seen[href] = true

			description := strings.TrimSpace(s.Find("p.description, p.desc, .desc").First().Text())

			results = append(results, SearchResult{
				Title:       title,
				URL:         href,
				Description: description,
			})
		})
	}

	return results, nil
}

// Exa's hosted keyless service uses the SDK; configured keys use exaSearch.
func searchExaMCP(ctx context.Context, endpoint, query string, limit int) ([]SearchResult, error) {
	httpClient := network.NewClient(15 * time.Second)
	defer httpClient.CloseIdleConnections()
	client := mcp.NewClient(&mcp.Implementation{Name: "crude-web", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1, MaxEventSize: maxResponseBytes}, nil)
	if err != nil {
		return nil, redactRequestURL(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search_exa", Arguments: map[string]any{"query": query, "type": "auto", "numResults": limit, "livecrawl": "fallback"}})
	if err != nil {
		return nil, redactRequestURL(err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if block, ok := content.(*mcp.TextContent); ok {
			text.WriteString(block.Text)
			text.WriteByte('\n')
		}
	}
	if result.IsError {
		return nil, fmt.Errorf("exa: %s", strings.TrimSpace(text.String()))
	}
	return parseExaResults(text.String(), limit)
}

func parseExaResults(text string, limit int) ([]SearchResult, error) {
	var results []SearchResult
	lines := strings.Split(text, "\n")
	var currentResult *SearchResult

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Title:") {
			if currentResult != nil && currentResult.Title != "" {
				results = append(results, *currentResult)
			}
			currentResult = &SearchResult{}
			currentResult.Title = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
		} else if strings.HasPrefix(line, "URL:") && currentResult != nil {
			currentResult.URL = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		} else if strings.HasPrefix(line, "Highlights:") && currentResult != nil {
			currentResult.Description = strings.TrimSpace(strings.TrimPrefix(line, "Highlights:"))
		}
	}

	if currentResult != nil && currentResult.Title != "" {
		results = append(results, *currentResult)
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// redactRequestURL drops the query string from a transport error's URL. Go's
// *url.Error embeds the full request URL, which for Exa carries the API key;
// engine errors reach logs and the model's tool result.
func redactRequestURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil && parsed.RawQuery != "" {
			parsed.RawQuery = "redacted"
			redacted := *urlErr
			redacted.URL = parsed.String()
			return &redacted
		}
	}
	return err
}
