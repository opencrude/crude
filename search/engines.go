package search

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"crude/logger"
	"crude/network"

	"github.com/PuerkitoBio/goquery"
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
	Engine      string
}

// engineTracker tracks engine reliability and reorders preference based on success/failure.
type engineTracker struct {
	mu     sync.RWMutex
	scores map[string]int
}

var tracker = &engineTracker{
	scores: map[string]int{
		"brave":      0,
		"duckduckgo": 0,
		"startpage":  0,
	},
}

func (t *engineTracker) success(engine string) {
	t.mu.Lock()
	t.scores[engine]++
	scores := t.scores[engine]
	t.mu.Unlock()
	logIf(func(lg *logger.Logger) { lg.Debug("SEARCH", "engine_success", "engine", engine, "score", scores) })
}

func (t *engineTracker) failure(engine string) {
	t.mu.Lock()
	t.scores[engine]--
	scores := t.scores[engine]
	t.mu.Unlock()
	logIf(func(lg *logger.Logger) { lg.Debug("SEARCH", "engine_failure", "engine", engine, "score", scores) })
}

func (t *engineTracker) engines() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	all := []string{"brave", "duckduckgo", "startpage", "exa"}

	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if t.scores[all[j]] > t.scores[all[i]] {
				all[i], all[j] = all[j], all[i]
			}
		}
	}

	return all
}

func searchWithEngine(engine, query string, limit int) ([]SearchResult, error) {
	logIf(func(lg *logger.Logger) {
		lg.Debug("SEARCH", "engine_start", "engine", engine, "query", query, "limit", limit)
	})
	start := time.Now()
	results, err := func() ([]SearchResult, error) {
		switch strings.ToLower(engine) {
		case "brave":
			return searchBrave(query, limit)
		case "duckduckgo":
			return searchDuckDuckGo(query, limit)
		case "startpage":
			return searchStartpage(query, limit)
		case "exa":
			return searchExa(query, limit)
		default:
			return searchBrave(query, limit)
		}
	}()
	dur := time.Since(start).Milliseconds()
	if err != nil {
		logIf(func(lg *logger.Logger) {
			lg.Warn("SEARCH", "engine_failed", "engine", engine, "latency_ms", dur, "error", err.Error())
		})
	} else {
		logIf(func(lg *logger.Logger) {
			lg.Debug("SEARCH", "engine_ok", "engine", engine, "results", len(results), "latency_ms", dur)
		})
	}
	return results, err
}

// SuperSearch queries multiple engines concurrently, deduplicates results,
// and tracks engine health. Returns deduplicated results, succeeded engines,
// and failed engines with their error messages.
func SuperSearch(query string, limit int) ([]SearchResult, []string, map[string]string) {
	engines := tracker.engines()
	logIf(func(lg *logger.Logger) {
		lg.Info("SEARCH", "super_start", "query", query, "limit", limit, "engines", engines)
	})

	type engineResult struct {
		engine  string
		results []SearchResult
		err     error
	}

	ch := make(chan engineResult, len(engines))

	for _, eng := range engines {
		go func(e string) {
			results, err := searchWithEngine(e, query, limit)
			ch <- engineResult{engine: e, results: results, err: err}
		}(eng)
	}

	var allResults []SearchResult
	var succeeded []string
	failed := make(map[string]string)

	for i := 0; i < len(engines); i++ {
		r := <-ch
		if r.err != nil {
			tracker.failure(r.engine)
			failed[r.engine] = r.err.Error()
			continue
		}
		tracker.success(r.engine)
		succeeded = append(succeeded, r.engine)
		allResults = append(allResults, r.results...)
	}

	deduped := deduplicateResults(allResults, limit)
	logIf(func(lg *logger.Logger) {
		lg.Info("SEARCH", "super_done", "query", query, "raw", len(allResults), "deduped", len(deduped), "succeeded", len(succeeded), "failed_engines", len(failed))
	})
	return deduped, succeeded, failed
}

func deduplicateResults(results []SearchResult, limit int) []SearchResult {
	type key struct {
		title string
		url   string
	}

	seen := make(map[key]bool)
	var deduped []SearchResult

	for _, r := range results {
		normalizedTitle := strings.ToLower(strings.TrimSpace(r.Title))
		normalizedURL := strings.ToLower(strings.TrimSpace(r.URL))

		if normalizedTitle == "" || normalizedURL == "" {
			continue
		}

		k := key{title: normalizedTitle, url: normalizedURL}
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

func searchBrave(query string, limit int) ([]SearchResult, error) {
	encodedQuery := url.QueryEscape(query)
	reqURL := fmt.Sprintf("https://search.brave.com/search?q=%s&source=web", encodedQuery)

	req, err := http.NewRequest("GET", reqURL, nil)
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

	doc, err := goquery.NewDocumentFromReader(resp.Body)
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
			Engine:      "brave",
		})
	})

	return results, nil
}

func searchDuckDuckGo(query string, limit int) ([]SearchResult, error) {
	encodedQuery := url.QueryEscape(query)
	urlStr := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", encodedQuery)

	req, err := http.NewRequest("GET", urlStr, nil)
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

	doc, err := goquery.NewDocumentFromReader(resp.Body)
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
			Engine:      "duckduckgo",
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

func searchStartpage(query string, limit int) ([]SearchResult, error) {
	encodedQuery := url.QueryEscape(query)
	reqURL := fmt.Sprintf("https://www.startpage.com/do/search?query=%s", encodedQuery)

	req, err := http.NewRequest("GET", reqURL, nil)
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

	doc, err := goquery.NewDocumentFromReader(resp.Body)
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
			Engine:      "startpage",
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
				Engine:      "startpage",
			})
		})
	}

	return results, nil
}

func searchExa(query string, limit int) ([]SearchResult, error) {
	type ExaSearchRequest struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Name      string `json:"name"`
			Arguments struct {
				Query      string `json:"query"`
				Type       string `json:"type"`
				NumResults int    `json:"numResults"`
				LiveCrawl  string `json:"livecrawl"`
			} `json:"arguments"`
		} `json:"params"`
	}

	type ExaSearchResponse struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}

	apiKey := os.Getenv("EXA_API_KEY")
	baseURL := "https://mcp.exa.ai/mcp"
	if apiKey != "" {
		baseURL = fmt.Sprintf("https://mcp.exa.ai/mcp?exaApiKey=%s", apiKey)
	}

	reqBody := ExaSearchRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
	}
	reqBody.Params.Name = "web_search_exa"
	reqBody.Params.Arguments.Query = query
	reqBody.Params.Arguments.Type = "auto"
	reqBody.Params.Arguments.NumResults = limit
	reqBody.Params.Arguments.LiveCrawl = "fallback"

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("content-type", "application/json")

	client := network.NewClient(30 * time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(respBody), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			var data ExaSearchResponse
			if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
				continue
			}
			if len(data.Result.Content) > 0 {
				text := data.Result.Content[0].Text
				return parseExaResults(text, limit)
			}
		}
	}

	return []SearchResult{}, nil
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
			currentResult = &SearchResult{Engine: "exa"}
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
