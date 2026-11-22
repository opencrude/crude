package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestKeylessSearchReturnsUsefulResultsWithoutWaitingForStalledEngine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		wait  time.Duration
	}{
		{"limit met", 1, 0},
		{"partial result", 2, 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started, stopped := make(chan struct{}), make(chan struct{})
				client := New(nil)
				client.keyless = []Method{
					{Name: "stalled", Search: func(ctx context.Context, _ string, _ int) ([]SearchResult, error) {
						close(started)
						defer close(stopped)
						<-ctx.Done()
						return nil, ctx.Err()
					}},
					{Name: "working", Search: func(context.Context, string, int) ([]SearchResult, error) {
						<-started
						return []SearchResult{{Title: "Go documentation", URL: "https://go.dev/doc/"}}, nil
					}},
				}
				start := time.Now()
				text, err := client.Search(t.Context(), "go", tc.limit)
				if err != nil || !strings.Contains(text, "https://go.dev/doc/") ||
					strings.Contains(text, "stalled") || time.Since(start) != tc.wait {
					t.Fatalf("result after %v = %q, %v; want %v", time.Since(start), text, err, tc.wait)
				}
				select {
				case <-stopped:
				default:
					t.Fatal("search returned without cancelling and joining its pending request")
				}
			})
		})
	}
}

func TestSearchConfiguredGraceKeepsReadyFreeResultsAndJoinsStalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var running atomic.Int32
		client := New(nil)
		client.keyless = []Method{
			{Name: "stalled", Search: func(ctx context.Context, _ string, _ int) ([]SearchResult, error) {
				running.Add(1)
				defer running.Add(-1)
				<-ctx.Done()
				time.Sleep(7 * time.Millisecond) // Simulate work required to close the provider.
				return []SearchResult{{Title: "Late reply", URL: "https://example.test/late"}}, nil
			}},
			searchReply("Free Engine", false, 100*time.Millisecond, []SearchResult{
				{Title: "Shared name", URL: "https://example.test/shared"},
			}, nil),
		}
		extra := []Method{searchReply("Account Web", true, 0, []SearchResult{
			{Title: "Configured rank one", URL: "https://example.test/solo"},
			{Title: "Shared name", URL: "https://example.test/shared"},
		}, nil)}
		start := time.Now()
		text, err := client.SearchWith(t.Context(), "go", 1, extra)
		if err != nil || !strings.Contains(text, "(Sources: Account Web, Free Engine)") ||
			!strings.Contains(text, "1. Shared name") || strings.Contains(text, "stalled") ||
			strings.Contains(text, "Late reply") || time.Since(start) != 157*time.Millisecond || running.Load() != 0 {
			t.Fatalf("result after %v with %d workers = %q, %v", time.Since(start), running.Load(), text, err)
		}
	})
}

func TestSearchWaitsForFreeResultAfterConfiguredCompletedEmpty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := New(nil)
		client.keyless = []Method{searchReply("Free Engine", false, time.Second, []SearchResult{
			{Title: "Useful reply", URL: "https://example.test/useful"},
		}, nil)}
		extra := []Method{searchReply("Account Web", true, 0, nil, nil)}
		text, err := client.SearchWith(t.Context(), "go", 1, extra)
		if err != nil || !strings.Contains(text, "(Sources: Account Web, Free Engine)") || !strings.Contains(text, "1. Useful reply") {
			t.Fatalf("search abandoned pending free results: %q, %v", text, err)
		}
	})
}

func TestSearchSharedDeadlineKeepsTimelyResultsAndMarksOutstandingMethods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stalled := func(name string, configured bool) Method {
			return Method{Name: name, Configured: configured, Search: func(ctx context.Context, _ string, _ int) ([]SearchResult, error) {
				<-ctx.Done()
				return []SearchResult{{Title: "Too late", URL: "https://example.test/late"}}, nil
			}}
		}
		client := New(nil)
		client.keyless = []Method{stalled("Free Engine", false)}
		extra := []Method{stalled("Account Web", true)}
		start := time.Now()
		text, err := client.SearchWith(t.Context(), "go", 1, extra)
		outage, ok := errors.AsType[*ProvidersFailed](err)
		if text != "" || !ok || time.Since(start) != 24*time.Second {
			t.Fatalf("shared deadline after %v = %q, %v", time.Since(start), text, err)
		}
		for _, want := range []string{
			"Account Web: did not answer before the search deadline",
			"Free Engine: did not answer before the search deadline",
		} {
			if !strings.Contains(outage.Error(), want) {
				t.Fatalf("deadline failure lacks %q:\n%s", want, outage.Error())
			}
		}
		client.keyless = append(client.keyless, searchReply("Timely engine", false, 24*time.Second-time.Millisecond, []SearchResult{
			{Title: "In time", URL: "https://example.test/intime"},
		}, nil))
		text, err = client.SearchWith(t.Context(), "go", 1, extra)
		if err != nil || !strings.Contains(text, "(Sources: Timely engine)\n(Unavailable: Account Web, Free Engine)") ||
			!strings.Contains(text, "1. In time") || strings.Contains(text, "Too late") {
			t.Fatalf("shared deadline discarded usable results or admitted late results: %q, %v", text, err)
		}
	})
}

func TestSearchParentCancellationDiscardsResultsAndJoinsMethods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var running atomic.Int32
		cleanup := func(ctx context.Context, _ string, _ int) ([]SearchResult, error) {
			running.Add(1)
			defer running.Add(-1)
			<-ctx.Done()
			time.Sleep(8 * time.Millisecond) // Cancellation must join this cleanup.
			return nil, ctx.Err()
		}
		client := New(nil)
		client.keyless = []Method{
			{Name: "Free Pending", Search: cleanup},
			searchReply("Free Ready", false, 0, []SearchResult{{Title: "In time", URL: "https://example.test/intime"}}, nil),
		}
		extra := []Method{{Name: "Account Web", Configured: true, Search: cleanup}}
		type outcome struct {
			text string
			err  error
		}
		done := make(chan outcome, 1)
		go func() {
			text, err := client.SearchWith(ctx, "go", 1, extra)
			done <- outcome{text, err}
		}()
		synctest.Wait()
		if running.Load() != 2 {
			t.Fatalf("expected both methods to start, got %d workers", running.Load())
		}
		start := time.Now()
		cancel()
		got := <-done
		if got.text != "" || got.err != context.Canceled || time.Since(start) != 8*time.Millisecond || running.Load() != 0 {
			t.Fatalf("cancelled search after %v with %d workers = %q, %v", time.Since(start), running.Load(), got.text, got.err)
		}
	})
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

func TestSearchCancellationInterruptsExaSessionClose(t *testing.T) {
	sdk := mcp.NewServer(&mcp.Implementation{Name: "exa-fixture", Version: "1"}, nil)
	sdk.AddTool(&mcp.Tool{Name: "web_search_exa", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Title: SDK evidence\nURL: https://example.test/evidence"}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return sdk }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	deleting, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			close(deleting)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer close(release)
	client := New(nil)
	client.keyless = []Method{{Name: "exa-mcp", Search: func(ctx context.Context, query string, limit int) ([]SearchResult, error) {
		return searchExaMCP(ctx, server.URL, query, limit)
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.Search(ctx, "go", 1)
		done <- err
	}()
	select {
	case <-deleting:
	case err := <-done:
		t.Fatalf("search returned before it tried to close the Exa session: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exa session did not begin to close")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Exa session close blocked search cancellation")
	}
}
