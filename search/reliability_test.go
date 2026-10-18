package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestKeylessSearchReturnsUsefulResultsWithoutWaitingForStalledEngine(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	engines := []keyedEngine{
		{"stalled", func(ctx context.Context, _ string, _ int) ([]SearchResult, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}},
		{"working", func(context.Context, string, int) ([]SearchResult, error) {
			<-started
			return []SearchResult{{Title: "Go documentation", URL: "https://go.dev/doc/"}}, nil
		}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	results, _, _ := runKeylessSearch(ctx, engines, "go", 1)
	if ctx.Err() != nil || len(results) != 1 || results[0].URL != "https://go.dev/doc/" {
		t.Fatalf("working result lost behind stalled engine: results=%v err=%v", results, ctx.Err())
	}
	select {
	case <-stopped:
	default:
		t.Fatal("search returned without cancelling and joining its pending request")
	}
}

func TestDeduplicationUsesDestinationNotProviderTitle(t *testing.T) {
	results := deduplicateResults([]SearchResult{
		{Title: "Go documentation", URL: "https://go.dev/doc/#overview"},
		{Title: "Documentation - Go", URL: "https://go.dev/doc/"},
		{Title: "Different case-sensitive resource", URL: "https://go.dev/Doc/"},
	}, 10)
	if len(results) != 2 || results[1].URL != "https://go.dev/Doc/" {
		t.Fatalf("duplicate destination or case-sensitive path lost: %v", results)
	}
}

func TestFetchTextMediaAndNestedHTMLWithoutDuplication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/guide.md" {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = w.Write([]byte("# Guide\n\n```python\n# comment\nprint('hi')\n```\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><title>Guide</title><body><nav>Noise</nav><main><article class=\"content\"><h1>One title</h1><p>Unique paragraph.</p><pre><code>one()\ntwo()</code></pre><p><a href=\"/details\">Details</a></p></article></main></body></html>"))
	}))
	defer server.Close()
	client := New(nil)
	text, err := client.Fetch(t.Context(), server.URL+"/guide.md", 1, 1000)
	if err != nil || !strings.Contains(text, "# comment\nprint('hi')") {
		t.Fatalf("ordinary Markdown rejected or damaged: %s %v", text, err)
	}
	text, err = client.Fetch(t.Context(), server.URL, 1, 1000)
	if err != nil || strings.Count(text, "Unique paragraph.") != 1 || strings.Contains(text, "Noise") || !strings.Contains(text, "one()\ntwo()") || !strings.Contains(text, server.URL+"/details") {
		t.Fatalf("readable HTML lost structure or duplicated nested content: %s %v", text, err)
	}
}

func TestFetchRejectsOversizeAndInvalidURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer server.Close()
	client := New(nil)
	if _, err := client.Fetch(t.Context(), server.URL, 1, 100); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized response silently accepted: %v", err)
	}
	for _, address := range []string{"file:///etc/passwd", "https://user:password@example.com", "://bad"} {
		if _, err := client.Fetch(t.Context(), address, 1, 10); err == nil {
			t.Fatalf("invalid URL accepted: %s", address)
		}
	}
}

func TestSearchTotalFailureIsNotAnEmptySuccess(t *testing.T) {
	client := New(nil)
	client.keyless = func(context.Context, string, int) ([]SearchResult, []string, map[string]string) {
		return nil, nil, map[string]string{"provider": "request timed out"}
	}
	_, err := client.Search(t.Context(), "go", 1)
	if err == nil || !strings.Contains(err.Error(), "request timed out") {
		t.Fatalf("outage falsely reported as zero matches: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Search(ctx, "go", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestFetchConcurrentPagesShareOneRequestAndEndCleanly(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("αβγδεζηθ"))
	}))
	defer server.Close()
	client := New(nil)
	var workers sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := client.Fetch(t.Context(), server.URL+"#anchor", 1, 3)
			errors <- err
		}()
	}
	<-entered
	close(release)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("parallel reads made %d duplicate requests", calls.Load())
	}
	text, err := client.Fetch(t.Context(), server.URL, 7, 100)
	if err != nil || strings.Contains(text, "pass offset=") || !strings.Contains(text, "ηθ") {
		t.Fatalf("last page loops: %q %v", text, err)
	}
	text, err = client.Fetch(t.Context(), server.URL, 9, 100)
	if err != nil || !strings.Contains(text, "past the end") || strings.Contains(text, "pass offset=") {
		t.Fatalf("out-of-range page loops: %q %v", text, err)
	}
}

func TestExaUsesSDKProtocolForJSONAndSSE(t *testing.T) {
	for _, jsonResponse := range []bool{false, true} {
		t.Run(map[bool]string{false: "SSE", true: "JSON"}[jsonResponse], func(t *testing.T) {
			sdk := mcp.NewServer(&mcp.Implementation{Name: "exa-fixture", Version: "1"}, nil)
			sdk.AddTool(&mcp.Tool{Name: "web_search_exa", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Title: SDK evidence\nURL: https://example.com/evidence\nHighlights: Actual protocol result"}}}, nil
			})
			server := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return sdk }, &mcp.StreamableHTTPOptions{JSONResponse: jsonResponse}))
			defer server.Close()
			results, err := searchExaMCP(t.Context(), server.URL, "evidence", 3)
			if err != nil || len(results) != 1 || results[0].URL != "https://example.com/evidence" {
				t.Fatalf("MCP response lost: %v %v", results, err)
			}
		})
	}
}
