package search

import (
	"context"
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

func keylessMethods() []Method {
	return []Method{
		{Name: "brave", Search: searchBrave},
		{Name: "duckduckgo", Search: searchDuckDuckGo},
		{Name: "startpage", Search: searchStartpage},
		{Name: "exa-mcp", Search: func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
			return searchExaMCP(ctx, "https://mcp.exa.ai/mcp", query, limit)
		}},
	}
}

const searchGrace = 150 * time.Millisecond

type methodAnswer struct {
	index    int
	finished time.Time
	results  []SearchResult
	err      error
}

type methodFailure struct {
	name, reason string
}

func runSearch(parent context.Context, methods []Method, query string, limit int) ([]SearchResult, []string, []methodFailure) {
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	deadline, _ := ctx.Deadline()
	cutoff := deadline
	ch := make(chan methodAnswer, len(methods))
	var workers sync.WaitGroup
	configured := 0
	for index, method := range methods {
		if method.Configured {
			configured++
		}
		workers.Go(func() {
			results, err := method.Search(ctx, query, limit)
			ch <- methodAnswer{index: index, finished: time.Now(), results: results, err: err}
		})
	}

	type completion struct {
		methodAnswer
		hits []searchHit
	}
	var answers []completion
	answered := make([]bool, len(methods))
	unique := make(map[string]bool)
	record := func(answer methodAnswer) {
		answered[answer.index] = true
		if methods[answer.index].Configured {
			configured--
		}
		done := completion{methodAnswer: answer}
		if answer.err == nil {
			done.hits = uniqueSearchHits(answer.results)
			for _, hit := range done.hits {
				unique[hit.key] = true
			}
		}
		answers = append(answers, done)
	}

	var grace <-chan time.Time
	var timer *time.Timer
	timeCutoff := false
	fillLimit := true
race:
	for range methods {
		if ctx.Err() != nil {
			timeCutoff = true
			break
		}
		select {
		case <-ctx.Done():
			timeCutoff = true
			break race
		case <-grace:
			timeCutoff = true
			break race
		case answer := <-ch:
			if !answer.finished.Before(cutoff) {
				timeCutoff = true
				break race
			}
			wasWaiting := configured > 0
			record(answer)
			if configured > 0 || len(unique) == 0 {
				continue
			}
			if wasWaiting {
				// At least one usable result existed when the last configured
				// method finished. Give free engines grace even at the limit.
				fillLimit = false
			}
			if fillLimit && len(unique) >= limit {
				break race
			}
			if timer == nil {
				if end := time.Now().Add(searchGrace); end.Before(deadline) {
					cutoff = end
					timer = time.NewTimer(time.Until(end))
					grace = timer.C
				}
			}
		}
	}
	if timer != nil {
		timer.Stop()
	}
	cancel()
	workers.Wait()
	if parent.Err() != nil {
		return nil, nil, nil
	}
	if timeCutoff {
		// A timer and an answer can be ready at the same time. Keep replies that
		// finished before the cutoff, not replies produced by cancellation.
		close(ch)
		for answer := range ch {
			if answer.finished.Before(cutoff) {
				record(answer)
			}
		}
	}

	sort.SliceStable(answers, func(i, j int) bool { return answers[i].finished.Before(answers[j].finished) })
	var succeeded []string
	var failed []methodFailure
	var rankedLists [][]searchHit
	for _, answer := range answers {
		name := methods[answer.index].Name
		if answer.err != nil {
			reason := redactRequestURL(answer.err).Error()
			failed = append(failed, methodFailure{name, reason})
			logIf(func(lg *logger.Logger) {
				lg.Debug("SEARCH", "engine_failed", "engine", name, "error", reason)
			})
			continue
		}
		succeeded = append(succeeded, name)
		rankedLists = append(rankedLists, answer.hits)
	}
	if timeCutoff && cutoff.Equal(deadline) {
		for index, method := range methods {
			if !answered[index] {
				failed = append(failed, methodFailure{method.Name, "did not answer before the search deadline"})
			}
		}
	}
	return fuseSearchHits(rankedLists, limit), succeeded, failed
}

type searchHit struct {
	SearchResult
	key string
}

func uniqueSearchHits(results []SearchResult) []searchHit {
	seen := make(map[string]bool)
	var hits []searchHit
	for _, r := range results {
		parsed, err := url.Parse(strings.TrimSpace(r.URL))
		if strings.TrimSpace(r.Title) == "" || err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.User != nil {
			continue
		}
		parsed.Fragment, parsed.RawFragment = "", ""
		parsed.Host = strings.ToLower(parsed.Host)
		key := parsed.String()
		if !seen[key] {
			seen[key] = true
			hits = append(hits, searchHit{SearchResult: r, key: key})
		}
	}
	return hits
}

// fuseSearchHits applies reciprocal rank fusion with k=60. Visit methods in
// completion order so equal-ranked occurrences keep the earlier metadata.
func fuseSearchHits(lists [][]searchHit, limit int) []SearchResult {
	type fused struct {
		SearchResult
		ranks           []int
		score           float64
		bestRank        int
		bestMethod      int
		descriptionRank int
	}
	byURL := make(map[string]*fused)
	var hits []*fused
	for method, list := range lists {
		for index, hit := range list {
			rank := index + 1
			entry := byURL[hit.key]
			if entry == nil {
				entry = &fused{}
				byURL[hit.key] = entry
				hits = append(hits, entry)
			}
			entry.ranks = append(entry.ranks, rank)
			if entry.bestRank == 0 || rank < entry.bestRank {
				entry.bestRank, entry.bestMethod = rank, method
				entry.Title, entry.URL = hit.Title, hit.URL
			}
			if strings.TrimSpace(hit.Description) != "" && (entry.descriptionRank == 0 || rank < entry.descriptionRank) {
				entry.descriptionRank, entry.Description = rank, hit.Description
			}
		}
	}
	for _, hit := range hits {
		// Sum in rank order so the same ranks tie regardless of which method
		// contributed each rank.
		sort.Ints(hit.ranks)
		for _, rank := range hit.ranks {
			hit.score += 1.0 / float64(60+rank)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.bestRank != b.bestRank {
			return a.bestRank < b.bestRank
		}
		return a.bestMethod < b.bestMethod
	})
	results := make([]SearchResult, 0, min(limit, len(hits)))
	for _, hit := range hits[:min(limit, len(hits))] {
		results = append(results, hit.SearchResult)
	}
	return results
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
	// The SDK detaches its session from the operation. Bind its session DELETE
	// to the search too, so closing the session cannot delay cancellation.
	sessionClient := *httpClient
	sessionClient.Transport = exaSessionTransport{httpClient.Transport, ctx}
	client := mcp.NewClient(&mcp.Implementation{Name: "crude-web", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &sessionClient, DisableStandaloneSSE: true, MaxRetries: -1, MaxEventSize: maxResponseBytes}, nil)
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

type exaSessionTransport struct {
	http.RoundTripper
	search context.Context
}

func (transport exaSessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodDelete {
		return transport.RoundTripper.RoundTrip(req)
	}
	ctx := transport.search
	if deadline, ok := req.Context().Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	return transport.RoundTripper.RoundTrip(req.WithContext(ctx))
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
