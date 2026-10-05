package search

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crude/logger"
	"crude/network"
)

const arxivAPIURL = "https://export.arxiv.org/api/query"

type arxivFeed struct {
	XMLName xml.Name     `xml:"feed"`
	Entries []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	Title     string        `xml:"title"`
	Summary   string        `xml:"summary"`
	Published string        `xml:"published"`
	Authors   []arxivAuthor `xml:"author"`
	Links     []arxivLink   `xml:"link"`
	Category  []struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
}

type arxivAuthor struct {
	Name string `xml:"name"`
}

type arxivLink struct {
	Href  string `xml:"href,attr"`
	Title string `xml:"title,attr"`
	Type  string `xml:"type,attr"`
	Rel   string `xml:"rel,attr"`
}

// ArxivResult is a single arXiv search result.
type ArxivResult struct {
	Title      string
	Authors    []string
	Date       string
	Categories []string
	PDFURL     string
	AbsURL     string
	Abstract   string
}

// ArxivSearch queries the arXiv API and returns formatted results.
func ArxivSearch(query string, maxResults int) (string, error) {
	if query == "" {
		return "", fmt.Errorf("search: empty query")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	logIf(func(lg *logger.Logger) { lg.Info("SEARCH", "arxiv", "query", query, "max_results", maxResults) })
	start := time.Now()

	params := url.Values{}
	params.Set("search_query", "all:"+query)
	params.Set("start", "0")
	params.Set("max_results", fmt.Sprintf("%d", maxResults))
	params.Set("sortBy", "relevance")
	params.Set("sortOrder", "descending")

	reqURL := arxivAPIURL + "?" + params.Encode()

	client := network.NewClient(15 * time.Second)
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Crude/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search: arxiv request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("search: reading response: %w", err)
	}

	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("search: parsing arxiv response: %w", err)
	}

	dur := time.Since(start).Milliseconds()
	logIf(func(lg *logger.Logger) {
		lg.Info("SEARCH", "arxiv_ok", "query", query, "results", len(feed.Entries), "latency_ms", dur)
	})

	if len(feed.Entries) == 0 {
		return "No arXiv papers found for: " + query, nil
	}

	results := parseArxivEntries(feed.Entries)
	return formatArxivResults(query, results), nil
}

func parseArxivEntries(entries []arxivEntry) []ArxivResult {
	results := make([]ArxivResult, 0, len(entries))
	for _, entry := range entries {
		title := cleanWhitespace(entry.Title)
		abstract := cleanWhitespace(entry.Summary)
		if len([]rune(abstract)) > 400 {
			abstract = string([]rune(abstract)[:400]) + "…"
		}

		authors := make([]string, 0, len(entry.Authors))
		for _, a := range entry.Authors {
			authors = append(authors, strings.TrimSpace(a.Name))
		}

		var cats []string
		for _, c := range entry.Category {
			cats = append(cats, c.Term)
		}

		pdfURL, absURL := extractArxivLinks(entry.Links)

		date := ""
		if len(entry.Published) >= 10 {
			date = entry.Published[:10]
		}

		results = append(results, ArxivResult{
			Title:      title,
			Authors:    authors,
			Date:       date,
			Categories: cats,
			PDFURL:     pdfURL,
			AbsURL:     absURL,
			Abstract:   abstract,
		})
	}
	return results
}

func extractArxivLinks(links []arxivLink) (pdfURL, absURL string) {
	for _, link := range links {
		if link.Title == "pdf" || link.Type == "application/pdf" {
			pdfURL = link.Href
		}
		if link.Rel == "alternate" {
			absURL = link.Href
		}
	}
	if pdfURL == "" && absURL != "" {
		pdfURL = strings.Replace(absURL, "/abs/", "/pdf/", 1)
	}
	return
}

func formatArxivResults(query string, results []ArxivResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "arXiv search results for: %s\n\n", query)

	for i, r := range results {
		authorStr := strings.Join(r.Authors, ", ")
		if len(r.Authors) > 5 {
			authorStr = strings.Join(r.Authors[:5], ", ") + " et al."
		}

		fmt.Fprintf(&sb, "%d. **%s**\n", i+1, r.Title)
		fmt.Fprintf(&sb, "   Authors: %s\n", authorStr)
		if r.Date != "" {
			fmt.Fprintf(&sb, "   Published: %s\n", r.Date)
		}
		if len(r.Categories) > 0 {
			fmt.Fprintf(&sb, "   Categories: %s\n", strings.Join(r.Categories, ", "))
		}
		if r.PDFURL != "" {
			fmt.Fprintf(&sb, "   PDF: %s\n", r.PDFURL)
		}
		if r.AbsURL != "" {
			fmt.Fprintf(&sb, "   URL: %s\n", r.AbsURL)
		}
		fmt.Fprintf(&sb, "   Abstract: %s\n\n", r.Abstract)
	}
	return sb.String()
}

func cleanWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
