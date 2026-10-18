package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"crude/docconv"
	"crude/logger"
	"crude/network"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/PuerkitoBio/goquery"
)

// WebPage is extracted page content.
type WebPage struct {
	URL        string
	Title      string
	Content    string
	TotalChars int
	// Reader names the fallback service that produced the content, if any.
	Reader string
}

// Fetch returns the readable content of rawURL. offset is a 1-based character
// position (0 = start) and limit the maximum characters (0 = all). A document
// is fetched once per client and paginated from memory afterwards. When a site
// refuses direct requests, a configured reader (Exa contents or Jina Reader)
// fetches it instead.
func (client *Client) Fetch(ctx context.Context, rawURL string, offset, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	var err error
	rawURL, err = normalizeFetchURL(rawURL)
	if err != nil {
		return "", err
	}
	start := time.Now()
	page, cached, err := client.loadPage(ctx, rawURL)
	if err != nil {
		logIf(func(lg *logger.Logger) {
			lg.Warn("SEARCH", "fetch_error", "url", rawURL, "latency_ms", time.Since(start).Milliseconds(), "error", err.Error())
		})
		return "", err
	}
	logIf(func(lg *logger.Logger) {
		lg.Info("SEARCH", "fetch_ok", "url", rawURL, "total_chars", page.TotalChars, "cached", cached, "latency_ms", time.Since(start).Milliseconds())
	})
	if strings.TrimSpace(page.Content) == "" {
		return fmt.Sprintf("No content could be extracted from %s. The page may render with JavaScript, require sign-in, or be empty. Configure an Exa or Jina Reader key in Settings → MCP → Web to read such pages.", page.URL), nil
	}
	content := sliceContent(page.Content, offset, limit)
	result := formatPage(page, offset, content)
	// A page that would not fit the transport is cut to the largest prefix
	// that does, with the offset to continue from.
	for client.resultBudget > 0 && encodedLen(result) > client.resultBudget && content != "" {
		runes := []rune(content)
		keep := int(float64(len(runes)) * float64(client.resultBudget) / float64(encodedLen(result)) * 0.98)
		content = string(runes[:max(0, min(keep, len(runes)-1))])
		result = formatPage(page, offset, content)
	}
	if client.resultBudget > 0 && encodedLen(result) > client.resultBudget {
		return "", errors.New("fetch: page metadata exceeds the result budget")
	}
	return result, nil
}

func normalizeFetchURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("fetch: url is empty; pass the http(s) URL to read")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("fetch: URL must be HTTP(S), with a hostname and no embedded credentials")
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Fragment, u.RawFragment = "", ""
	return u.String(), nil
}

type pageFlight struct {
	done chan struct{}
	page WebPage
	err  error
}

// Cache lookup and in-flight admission share one lock. Concurrent reads of the
// same URL join one request; cancellation never leaves an orphaned fetch worker.
func (client *Client) loadPage(ctx context.Context, rawURL string) (WebPage, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return WebPage{}, false, err
		}
		client.fetchMu.Lock()
		if page, ok := client.pages.get(rawURL); ok {
			client.fetchMu.Unlock()
			return page, true, nil
		}
		if flight := client.fetching[rawURL]; flight != nil {
			client.fetchMu.Unlock()
			select {
			case <-ctx.Done():
				return WebPage{}, false, ctx.Err()
			case <-flight.done:
				if errors.Is(flight.err, context.Canceled) && ctx.Err() == nil {
					continue
				}
				return flight.page, true, flight.err
			}
		}
		flight := &pageFlight{done: make(chan struct{})}
		if client.fetching == nil {
			client.fetching = make(map[string]*pageFlight)
		}
		client.fetching[rawURL] = flight
		client.fetchMu.Unlock()
		flight.page, flight.err = client.fetchPage(ctx, rawURL)
		client.fetchMu.Lock()
		if flight.err == nil {
			client.pages.put(rawURL, flight.page)
		}
		delete(client.fetching, rawURL)
		close(flight.done)
		client.fetchMu.Unlock()
		return flight.page, false, flight.err
	}
}

func formatPage(page WebPage, offset int, content string) string {
	var sb strings.Builder
	if page.Title != "" {
		fmt.Fprintf(&sb, "Title: %s\n", page.Title)
	}
	fmt.Fprintf(&sb, "URL: %s\n", page.URL)
	if page.Reader != "" {
		fmt.Fprintf(&sb, "Read via: %s\n", page.Reader)
	}
	first := max(1, offset)
	last := first + len([]rune(content)) - 1
	if first > page.TotalChars {
		fmt.Fprintf(&sb, "End of document (%d characters); offset=%d is past the end.\n\n", page.TotalChars, first)
	} else if last < page.TotalChars {
		fmt.Fprintf(&sb, "Characters %d–%d of %d; pass offset=%d to continue.\n\n", first, max(first-1, last), page.TotalChars, last+1)
	} else if first > 1 {
		fmt.Fprintf(&sb, "Characters %d–%d of %d; end of document.\n\n", first, last, page.TotalChars)
	} else {
		fmt.Fprintf(&sb, "Total characters: %d\n\n", page.TotalChars)
	}
	sb.WriteString(content)
	return sb.String()
}

// encodedLen is the size of text inside a JSON result.
func encodedLen(text string) int {
	encoded, _ := json.Marshal(text)
	return len(encoded)
}

// fetchPage reads one document directly, falling back to a configured reader
// when the site refuses automated requests or yields no readable text.
func (client *Client) fetchPage(ctx context.Context, rawURL string) (WebPage, error) {
	keyExa, keyJina := client.lookup("EXA_API_KEY"), client.lookup("JINA_API_KEY")
	direct := ctx
	stop := func() {}
	if keyExa != "" || keyJina != "" {
		direct, stop = context.WithTimeout(ctx, 12*time.Second)
	}
	page, err := fetchWebContent(direct, rawURL)
	stop()
	if err == nil && strings.TrimSpace(page.Content) != "" {
		return *page, nil
	}
	if ctx.Err() != nil {
		return WebPage{}, ctx.Err()
	}
	var readerErrs []string
	if keyExa != "" {
		attempt, cancel := context.WithTimeout(ctx, 6*time.Second)
		read, readErr := exaContents(attempt, client.endpoints.exa, keyExa, rawURL)
		cancel()
		if readErr == nil && strings.TrimSpace(read.Content) != "" {
			return read, nil
		}
		if readErr != nil {
			readerErrs = append(readerErrs, readErr.Error())
		}
	}
	if keyJina != "" {
		read, readErr := jinaRead(ctx, client.endpoints.jina, keyJina, rawURL)
		if readErr == nil && strings.TrimSpace(read.Content) != "" {
			return read, nil
		}
		if readErr != nil {
			readerErrs = append(readerErrs, readErr.Error())
		}
	}
	if err == nil {
		return *page, nil
	}
	err = explainFetchError(rawURL, err)
	if len(readerErrs) > 0 {
		err = fmt.Errorf("%w; readers also failed: %s", err, strings.Join(readerErrs, "; "))
	}
	return WebPage{}, err
}

func explainFetchError(rawURL string, err error) error {
	text := err.Error()
	switch {
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
		return fmt.Errorf("fetch: request timed out for %s; the site may be slow or blocking automated requests", rawURL)
	case strings.Contains(text, "connection refused") || strings.Contains(text, "no such host"):
		return fmt.Errorf("fetch: could not connect to %s; verify the URL", rawURL)
	case strings.Contains(text, "TLS") || strings.Contains(text, "certificate"):
		return fmt.Errorf("fetch: TLS/certificate error for %s: %w", rawURL, err)
	}
	return fmt.Errorf("fetch: %w", err)
}

func exaContents(ctx context.Context, base, key, rawURL string) (WebPage, error) {
	var response struct {
		Results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"results"`
	}
	request := map[string]any{"urls": []string{rawURL}, "text": true}
	if err := postJSON(ctx, "Exa", "POST", base+"/contents", map[string]string{"x-api-key": key}, request, &response); err != nil {
		return WebPage{}, err
	}
	if len(response.Results) == 0 {
		return WebPage{}, errors.New("exa returned no content for the URL")
	}
	result := response.Results[0]
	return WebPage{URL: firstNonEmpty(result.URL, rawURL), Title: result.Title, Content: result.Text, TotalChars: len([]rune(result.Text)), Reader: "Exa"}, nil
}

func jinaRead(ctx context.Context, base, key, rawURL string) (WebPage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/"+rawURL, nil)
	if err != nil {
		return WebPage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/plain")
	httpClient := network.NewClient(60 * time.Second)
	defer httpClient.CloseIdleConnections()
	resp, err := httpClient.Do(req)
	if err != nil {
		return WebPage{}, redactRequestURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return WebPage{}, fmt.Errorf("jina reader returned HTTP %d", resp.StatusCode)
	}
	body, err := readResponse(resp.Body, maxResponseBytes)
	if err != nil {
		return WebPage{}, err
	}
	content := strings.ReplaceAll(string(body), "\r\n", "\n")
	return WebPage{URL: rawURL, Content: content, TotalChars: len([]rune(content)), Reader: "Jina Reader"}, nil
}

// mimeToExt maps document MIME types to file extensions for docconv.
var mimeToExt = map[string]string{
	"application/pdf": ".pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.ms-excel": ".xls",
	"application/epub+zip":     ".epub",
	"application/x-ipynb+json": ".ipynb",
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

func fetchWebContent(ctx context.Context, fetchURL string) (*WebPage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fetchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/112.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := network.NewClient(30 * time.Second)
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, fetchURL)
	}

	finalURL := resp.Request.URL.String()

	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	ext := mimeToExt[contentType]
	if ext == "" && !strings.HasPrefix(contentType, "text/") && contentType != "application/json" {
		ext = docExtFromURL(finalURL)
	}
	maxBytes := maxResponseBytes
	if ext != "" {
		maxBytes = 25 << 20
	}
	body, err := readResponse(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	if contentType == "" || contentType == "application/octet-stream" {
		contentType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
		if ext == "" {
			ext = mimeToExt[contentType]
		}
	}
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		if ext == "" && (strings.HasPrefix(contentType, "text/") || contentType == "application/json" || strings.HasSuffix(contentType, "+json") || contentType == "application/xml" || strings.HasSuffix(contentType, "+xml") || utf8.Valid(body) && !bytes.ContainsRune(body, 0) && contentType == "application/octet-stream") {
			if !utf8.Valid(body) {
				return nil, errors.New("fetch: text is not valid UTF-8")
			}
			content := string(body)
			content = strings.ReplaceAll(content, "\r\n", "\n")
			content = strings.ReplaceAll(content, "\r", "")
			totalChars := len([]rune(content))
			return &WebPage{
				URL:        finalURL,
				Title:      "",
				Content:    content,
				TotalChars: totalChars,
			}, nil
		}

		// Try document conversion (PDF, DOCX, PPTX, XLSX, etc.)
		if ext != "" {
			logIf(func(lg *logger.Logger) {
				lg.Info("SEARCH", "fetch_doc", "url", finalURL, "mime", contentType, "ext", ext)
			})
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
			return &WebPage{
				URL:        finalURL,
				Title:      "Document: " + docconv.FormatName(ext),
				Content:    content,
				TotalChars: totalChars,
			}, nil
		}

		return nil, fmt.Errorf("fetch: unsupported binary content type %q; use read_file after downloading it for native media inspection", contentType)
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

	// Select only outer roots: <main><article class=content> is one document,
	// not three copies of the same text. Markdown retains code, paragraphs/links.
	selector := "article, main, .content, .main-content, #content, .post-content, .article-content, .entry-content, .post-body"
	selections := doc.Find(selector).FilterFunction(func(_ int, s *goquery.Selection) bool { return s.ParentsFiltered(selector).Length() == 0 })
	if selections.Length() == 0 {
		selections = doc.Find("body")
	}
	var content strings.Builder
	var conversionErr error
	selections.Each(func(_ int, s *goquery.Selection) {
		if conversionErr != nil {
			return
		}
		md, err := htmltomarkdown.ConvertNode(s.Get(0), converter.WithContext(ctx), converter.WithDomain(finalURL))
		if err != nil {
			conversionErr = err
			return
		}
		content.Write(md)
		content.WriteString("\n\n")
	})
	if conversionErr != nil {
		return nil, fmt.Errorf("fetch: converting HTML: %w", conversionErr)
	}
	rawContent := strings.TrimSpace(content.String())

	totalChars := len([]rune(rawContent))

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

	end := start + min(limit, total-start)
	return string(runes[start:end])
}

func readResponse(reader io.Reader, limit int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, redactRequestURL(err)
	}
	if len(body) > limit {
		return nil, fmt.Errorf("fetch: response too large (limit %d bytes); content was not silently truncated", limit)
	}
	return body, nil
}
