package search

import (
	"fmt"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

// WebResult is a single web search result.
type WebResult struct {
	Title   string
	URL     string
	Snippet string
}

// WebSearch queries DuckDuckGo using colly and returns formatted results.
func WebSearch(query string, maxResults int) (string, error) {
	if query == "" {
		return "", fmt.Errorf("search: empty query")
	}
	if maxResults <= 0 {
		maxResults = 6
	}

	var results []WebResult

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		colly.MaxDepth(1),
		colly.Async(false),
	)
	c.SetRequestTimeout(15 * time.Second)

	c.OnHTML(".result.results_links", func(e *colly.HTMLElement) {
		if len(results) >= maxResults {
			return
		}

		titleEl := e.DOM.Find("a.result__a")
		title := strings.TrimSpace(titleEl.Text())
		if title == "" {
			return
		}

		rawURL, _ := titleEl.Attr("href")
		actualURL := decodeDDGURL(rawURL)

		snippet := strings.TrimSpace(e.DOM.Find("a.result__snippet").Text())

		results = append(results, WebResult{
			Title:   title,
			URL:     actualURL,
			Snippet: snippet,
		})
	})

	url := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", queryEscape(query))
	if err := c.Visit(url); err != nil {
		return "", fmt.Errorf("search: request failed: %w", err)
	}
	c.Wait()

	if len(results) == 0 {
		return "No results found for: " + query, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Web search results for: %s\n\n", query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}
	return sb.String(), nil
}

// WebPage is extracted page content.
type WebPage struct {
	URL     string
	Title   string
	Content string
}

// WebFetch fetches a URL and extracts the main text content using colly.
func WebFetch(url string, maxChars int) (string, error) {
	if url == "" {
		return "", fmt.Errorf("fetch: empty URL")
	}
	// No default limit — sysadmin tools need full content

	var page WebPage
	page.URL = url

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		colly.MaxDepth(1),
	)
	c.SetRequestTimeout(20 * time.Second)

	c.OnHTML("title", func(e *colly.HTMLElement) {
		page.Title = strings.TrimSpace(e.Text)
	})

	c.OnHTML("body", func(e *colly.HTMLElement) {
		var textParts []string
		e.ForEach("p, pre, code, h1, h2, h3, h4, h5, h6, li, td, th, blockquote", func(_ int, el *colly.HTMLElement) {
			text := strings.TrimSpace(el.Text)
			if text != "" {
				textParts = append(textParts, text)
			}
		})
		page.Content = strings.Join(textParts, "\n\n")
	})

	if err := c.Visit(url); err != nil {
		return "", fmt.Errorf("fetch: request failed: %w", err)
	}
	c.Wait()

	if page.Content == "" {
		return fmt.Sprintf("No content extracted from %s", url), nil
	}

	// Only truncate if explicitly requested (maxChars > 0)
	if maxChars > 0 && len(page.Content) > maxChars {
		page.Content = page.Content[:maxChars] + "\n\n[content truncated — specify higher max_chars or read in sections]"
	}

	var sb strings.Builder
	if page.Title != "" {
		sb.WriteString(fmt.Sprintf("Title: %s\n", page.Title))
	}
	sb.WriteString(fmt.Sprintf("URL: %s\n\n", url))
	sb.WriteString(page.Content)
	return sb.String(), nil
}

// decodeDDGURL extracts the actual URL from a DuckDuckGo redirect URL.
func decodeDDGURL(rawURL string) string {
	if idx := strings.Index(rawURL, "uddg="); idx >= 0 {
		rawURL = rawURL[idx+5:]
		if end := strings.Index(rawURL, "&"); end >= 0 {
			rawURL = rawURL[:end]
		}
	}
	return rawURL
}

// queryEscape is a simple URL query escaper.
func queryEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == ' ':
			b.WriteByte('+')
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteRune(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
