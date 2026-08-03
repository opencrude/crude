package search

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"crude/docconv"
	"crude/logger"

	"github.com/PuerkitoBio/goquery"
)

// WebSearch queries multiple search engines concurrently, deduplicates results,
// and returns formatted output. Falls back through engines automatically on failure.
func WebSearch(query string, maxResults int) (string, error) {
	if query == "" {
		return "", fmt.Errorf("search: empty query")
	}
	if maxResults <= 0 {
		maxResults = 6
	}

	logIf(func(lg *logger.Logger) { lg.Info("SEARCH", "web_search", "query", query, "max_results", maxResults) })
	start := time.Now()
	results, succeeded, failed := SuperSearch(query, maxResults)
	dur := time.Since(start).Milliseconds()

	if len(results) == 0 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "No results found for: %s\n\n", query)

		if len(succeeded) == 0 && len(failed) > 0 {
			sb.WriteString("All search engines failed:\n")
			logIf(func(lg *logger.Logger) { lg.Warn("SEARCH", "all_engines_failed", "query", query, "latency_ms", dur, "failures", fmt.Sprintf("%v", failed)) })
			for eng, errMsg := range failed {
				fmt.Fprintf(&sb, "  - %s: %s\n", eng, errMsg)
			}
			sb.WriteString("\nThis may be due to network issues or rate limiting.\n")
			sb.WriteString("Suggestions:\n")
			sb.WriteString("  - Retry the search in a moment\n")
			sb.WriteString("  - Try a simpler or different query\n")
			sb.WriteString("  - Use web_fetch to access a specific URL directly\n")
		} else {
			sb.WriteString("Suggestions:\n")
			sb.WriteString("  - Try different or more specific keywords\n")
			sb.WriteString("  - Remove quotes or special characters\n")
			sb.WriteString("  - Try a related topic\n")
		}

		return sb.String(), nil
	}

	logIf(func(lg *logger.Logger) { lg.Info("SEARCH", "web_search_ok", "query", query, "results", len(results), "engines", len(succeeded), "latency_ms", dur) })

	var sb strings.Builder
	fmt.Fprintf(&sb, "Web search results for: %s\n", query)

	if len(succeeded) > 0 {
		fmt.Fprintf(&sb, "(Sources: %s)\n", strings.Join(succeeded, ", "))
	}
	if len(failed) > 0 {
		var failedEngines []string
		for eng := range failed {
			failedEngines = append(failedEngines, eng)
		}
		fmt.Fprintf(&sb, "(Unavailable: %s)\n", strings.Join(failedEngines, ", "))
	}
	sb.WriteString("\n")

	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, r.Title)
		fmt.Fprintf(&sb, "   %s\n", r.URL)
		if r.Description != "" {
			desc := r.Description
			if len([]rune(desc)) > 200 {
				desc = string([]rune(desc)[:200]) + "..."
			}
			fmt.Fprintf(&sb, "   %s\n", desc)
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// WebPage is extracted page content.
type WebPage struct {
	URL        string
	Title      string
	Content    string
	TotalChars int
}

// WebFetch fetches a URL and extracts its text content with smart extraction
// and robust error handling. offset is 1-based character position (0 = start).
// limit is max chars to return (0 = all).
func WebFetch(rawURL string, offset, limit int) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("fetch: empty URL")
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	logIf(func(lg *logger.Logger) { lg.Info("SEARCH", "fetch", "url", rawURL, "offset", offset, "limit", limit) })
	start := time.Now()

	result, err := fetchWebContent(rawURL, offset, limit)
	if err != nil {
		logIf(func(lg *logger.Logger) { lg.Warn("SEARCH", "fetch_error", "url", rawURL, "latency_ms", time.Since(start).Milliseconds(), "error", err.Error()) })
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
			return "", fmt.Errorf("fetch: request timed out for %s — the site may be slow or blocking automated requests. Try again or try a different URL", rawURL)
		}
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
			return "", fmt.Errorf("fetch: could not connect to %s — the site may be down or the URL may be incorrect. Verify the URL and try again", rawURL)
		}
		if strings.Contains(err.Error(), "TLS") || strings.Contains(err.Error(), "certificate") {
			return "", fmt.Errorf("fetch: TLS/certificate error for %s — the site may have security issues. Try http:// instead of https:// if appropriate", rawURL)
		}
		return "", fmt.Errorf("fetch: %w", err)
	}

	dur := time.Since(start).Milliseconds()
	logIf(func(lg *logger.Logger) { lg.Info("SEARCH", "fetch_ok", "url", rawURL, "total_chars", result.TotalChars, "latency_ms", dur) })

	if result.Content == "" {
		return fmt.Sprintf("No content could be extracted from %s\n\nThe page may be:\n- A JavaScript-rendered single-page app (try a different URL)\n- Empty or requiring authentication\n- A binary file (PDF, image, etc.)", rawURL), nil
	}

	totalChars := result.TotalChars

	var sb strings.Builder
	if result.Title != "" {
		fmt.Fprintf(&sb, "Title: %s\n", result.Title)
	}
	fmt.Fprintf(&sb, "URL: %s\n", result.URL)

	if (offset > 1 || limit > 0) && totalChars > 0 {
		endPos := offset + len([]rune(result.Content)) - 1
		if limit > 0 && offset+limit-1 < endPos {
			endPos = offset + limit - 1
		}
		fmt.Fprintf(&sb, "Characters %d–%d of %s (%d total):\n\n", offset, endPos, result.URL, totalChars)
	} else {
		fmt.Fprintf(&sb, "Total characters: %d\n\n", totalChars)
	}
	sb.WriteString(result.Content)
	return sb.String(), nil
}

// mimeToExt maps document MIME types to file extensions for docconv.
var mimeToExt = map[string]string{
	"application/pdf":                                                  ".pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": ".xlsx",
	"application/vnd.ms-excel":         ".xls",
	"application/epub+zip":             ".epub",
	"application/x-ipynb+json":         ".ipynb",
}

// docExtFromURL checks if a URL ends with a supported document extension.
func docExtFromURL(rawURL string) string {
	// Strip query string and fragment
	path := rawURL
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = path[:idx]
	}
	ext := strings.ToLower(filepath.Ext(path))
	if docconv.SupportedExt(ext) {
		return ext
	}
	return ""
}

func fetchWebContent(fetchURL string, offset, limit int) (*WebPage, error) {
	req, err := http.NewRequest("GET", fetchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/112.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, fetchURL)
	}

	finalURL := resp.Request.URL.String()

	contentType := resp.Header.Get("Content-Type")

	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		if strings.Contains(contentType, "text/plain") || strings.Contains(contentType, "application/json") || strings.Contains(contentType, "application/xml") || strings.Contains(contentType, "text/xml") {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
			if err != nil {
				return nil, err
			}
			content := string(body)
			content = strings.ReplaceAll(content, "\r\n", "\n")
			content = strings.ReplaceAll(content, "\r", "")
			totalChars := len([]rune(content))
			content = sliceContent(content, offset, limit)
			return &WebPage{
				URL:        finalURL,
				Title:      "",
				Content:    content,
				TotalChars: totalChars,
			}, nil
		}

		// Try document conversion (PDF, DOCX, PPTX, XLSX, etc.)
		// Parse the media type from Content-Type (strip parameters like charset)
		mediaType := strings.TrimSpace(contentType)
		if idx := strings.IndexAny(mediaType, ";"); idx >= 0 {
			mediaType = strings.TrimSpace(mediaType[:idx])
		}
		ext := mimeToExt[mediaType]
		if ext == "" {
			ext = docExtFromURL(finalURL)
		}
		if ext != "" {
			logIf(func(lg *logger.Logger) {
				lg.Info("SEARCH", "fetch_doc", "url", finalURL, "mime", contentType, "ext", ext)
			})
			if resp.ContentLength > 25*1024*1024 {
				return nil, fmt.Errorf("fetch: document too large (%d MB, limit 25 MB) — use execute_command to download and convert locally", resp.ContentLength/(1024*1024))
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, 25*1024*1024))
			if err != nil {
				return nil, fmt.Errorf("fetch: reading document: %w", err)
			}
			md, meta, err := docconv.ConvertBytes(body, ext)
			if err != nil {
				return nil, fmt.Errorf("fetch: could not convert %s document: %w — try downloading it directly", docconv.FormatName(ext), err)
			}
			content := ""
			if meta != nil {
				content = docconv.Header(finalURL, meta)
			}
			content += md
			totalChars := len([]rune(content))
			content = sliceContent(content, offset, limit)
			return &WebPage{
				URL:        finalURL,
				Title:      "Document: " + docconv.FormatName(ext),
				Content:    content,
				TotalChars: totalChars,
			}, nil
		}

		return nil, fmt.Errorf("unsupported content type %q — this appears to be a binary file (PDF, image, etc.). Try accessing the URL directly in a browser", contentType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	title := strings.TrimSpace(doc.Find("title").First().Text())
	if title == "" {
		title = strings.TrimSpace(doc.Find("h1").First().Text())
	}

	doc.Find("script, style, noscript, iframe, nav, header, footer, aside, .ad, .ads, .sidebar, .nav, .menu, .comment, .comments").Each(func(i int, s *goquery.Selection) {
		s.Remove()
	})

	var content strings.Builder

	selections := doc.Find("article, main, .content, .main-content, #content, .post-content, .article-content, .entry-content, .post-body")
	if selections.Length() > 0 {
		selections.Each(func(i int, s *goquery.Selection) {
			content.WriteString(s.Text())
		})
	} else {
		doc.Find("body").Each(func(i int, s *goquery.Selection) {
			content.WriteString(s.Text())
		})
	}

	rawContent := content.String()

	rawContent = strings.Join(strings.Fields(rawContent), " ")

	totalChars := len([]rune(rawContent))
	rawContent = sliceContent(rawContent, offset, limit)

	return &WebPage{
		URL:        finalURL,
		Title:      title,
		Content:    rawContent,
		TotalChars: totalChars,
	}, nil
}

// sliceContent extracts a character range from content.
// offset is 1-based (0 or 1 = start). limit is max chars, 0 = all.
func sliceContent(content string, offset, limit int) string {
	runes := []rune(content)
	total := len(runes)

	start := 0
	if offset > 1 {
		start = offset - 1
	}
	if start >= total {
		return ""
	}

	if limit <= 0 {
		return string(runes[start:])
	}

	end := start + limit
	if end > total {
		end = total
	}
	return string(runes[start:end])
}
