// Package webmcp is Crude's built-in web MCP server: web_search, web_fetch and
// arxiv_search over the providers configured for the built-in "web"
// connection. It runs in-process; the MCP client owns its lifetime.
package webmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"crude/search"
	captypes "crude/types/capability"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type tool struct {
	name        string
	description string
	schema      string
	run         func(context.Context, *search.Client, map[string]any) (string, error)
}

// defaultFetchPage is the paging granularity Crude uses for bounded reads
// (read_async_output pages the same way). A page is a reading unit, not a
// policy: the agent asks for more with limit or continues with offset.
const defaultFetchPage = 32768

var serverTools = []tool{
	{
		name: "web_search",
		description: "Search the web. Returns titles, URLs and snippets for pages matching the query. " +
			"Use for current information, documentation and general web content; read a result with web_fetch.",
		schema: `{"type":"object","properties":{` +
			`"query":{"type":"string","description":"What to search for"},` +
			`"max_results":{"type":"integer","description":"Results to return (1-10; default 6)"}` +
			`},"required":["query"]}`,
		run: func(ctx context.Context, client *search.Client, args map[string]any) (string, error) {
			return client.Search(ctx, stringArg(args, "query"), boundedArg(args, "max_results", 6, 10))
		},
	},
	{
		name: "web_fetch",
		description: "Fetch a URL and return its readable text. PDFs, Office documents and notebooks are converted to Markdown. " +
			"Results are paged: each reports the total length and the offset to continue from, and later pages come from cache without refetching.",
		schema: `{"type":"object","properties":{` +
			`"url":{"type":"string","description":"http(s) URL to read"},` +
			`"offset":{"type":"integer","description":"First character to return (1-based; default 1)"},` +
			`"limit":{"type":"integer","description":"Characters to return (default 32768, the same page as read_async_output)"}` +
			`},"required":["url"]}`,
		run: func(ctx context.Context, client *search.Client, args map[string]any) (string, error) {
			limit := intArg(args, "limit")
			if limit <= 0 {
				limit = defaultFetchPage
			}
			return client.Fetch(ctx, stringArg(args, "url"), intArg(args, "offset"), limit)
		},
	},
	{
		name:        "arxiv_search",
		description: "Search arXiv papers. Returns titles, authors, dates, abstracts and links.",
		schema: `{"type":"object","properties":{` +
			`"query":{"type":"string","description":"Search query"},` +
			`"max_results":{"type":"integer","description":"Papers to return (1-10; default 5)"}` +
			`},"required":["query"]}`,
		run: func(ctx context.Context, _ *search.Client, args map[string]any) (string, error) {
			return search.ArxivSearch(ctx, stringArg(args, "query"), boundedArg(args, "max_results", 5, 10))
		},
	},
}

// New builds the server. lookup resolves the connection's optional provider
// settings (API keys and instance URLs).
// maxResultBytes is the client's bound on one encoded result; fetched pages
// are cut to fit it with a continuation offset.
func New(lookup func(string) string, maxResultBytes int) (*mcp.Server, map[string]captypes.Effect) {
	client := search.New(lookup)
	client.SetResultBudget(maxResultBytes)
	server := mcp.NewServer(&mcp.Implementation{Name: "crude-web", Version: "1"}, nil)
	effects := make(map[string]captypes.Effect, len(serverTools))
	for _, definition := range serverTools {
		var schema any
		if err := json.Unmarshal([]byte(definition.schema), &schema); err != nil {
			panic(fmt.Sprintf("webmcp: invalid %s schema: %v", definition.name, err))
		}
		run := definition.run
		server.AddTool(&mcp.Tool{
			Name: definition.name, Description: definition.description, InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(true)},
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if len(request.Params.Arguments) > 0 {
				if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
					return errorResult("arguments must be a JSON object: " + err.Error()), nil
				}
			}
			text, err := run(ctx, client, args)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return errorResult(err.Error()), nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
		})
		effects[definition.name] = captypes.EffectRead
	}
	return server, effects
}

// ToolNames lists the tools the server offers, in registration order.
func ToolNames() []string {
	names := make([]string, 0, len(serverTools))
	for _, definition := range serverTools {
		names = append(names, definition.name)
	}
	return names
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: true}
}

func boolPointer(value bool) *bool { return &value }

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

func intArg(args map[string]any, name string) int {
	switch value := args[name].(type) {
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	case string:
		var n int
		_, _ = fmt.Sscan(value, &n)
		return n
	}
	return 0
}

func boundedArg(args map[string]any, name string, fallback, ceiling int) int {
	value := intArg(args, name)
	if value <= 0 {
		return fallback
	}
	return min(value, ceiling)
}
