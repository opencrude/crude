package capability

// TransportBuiltin marks a connection served in-process by Crude itself. Its
// server is Crude's own code: it needs no launch approval, its tools keep their
// plain names, and it can be disabled but not removed.
const TransportBuiltin = "builtin"

// TransportSSE is the 2024-11-05 HTTP+SSE remote transport.
const TransportSSE = "sse"

// WebConnectionID is the built-in web search and fetch connection.
const WebConnectionID = "web"

// BuiltinSecret describes one optional credential a built-in server reads from
// Crude's secret store (falling back to the host environment variable of the
// same name).
type BuiltinSecret struct {
	Name        string
	Label       string
	Description string
}

// BuiltinServer describes a built-in server for configuration surfaces. The
// implementation is registered with the MCP client separately.
type BuiltinServer struct {
	Name        string
	Description string
	// Tools are the server's tool names, all with read effect, so settings can
	// offer per-agent switches before the server has started.
	Tools   []string
	Secrets []BuiltinSecret
}

var builtinServers = map[string]BuiltinServer{
	WebConnectionID: {
		Name: "Web search and fetch",
		Description: "Crude's built-in web_search, web_fetch and arxiv_search tools. Works without keys; " +
			"add a search API key for better, faster results. Disable it to use another MCP for the web.",
		Tools: []string{"web_search", "web_fetch", "arxiv_search"},
		Secrets: []BuiltinSecret{
			{Name: "EXA_API_KEY", Label: "Exa API key", Description: "Semantic search with highlights; lets web_fetch read blocked pages."},
			{Name: "BRAVE_SEARCH_API_KEY", Label: "Brave Search API key", Description: "Brave Search API (api.search.brave.com)."},
			{Name: "TAVILY_API_KEY", Label: "Tavily API key", Description: "Tavily search API."},
			{Name: "SEARXNG_URL", Label: "SearXNG URL", Description: "Base URL of a SearXNG instance with JSON output enabled."},
			{Name: "JINA_API_KEY", Label: "Jina Reader key", Description: "Lets web_fetch use r.jina.ai for pages that need a browser."},
		},
	},
}

// Builtin returns the descriptor of a built-in server.
func Builtin(server string) (BuiltinServer, bool) {
	descriptor, exists := builtinServers[server]
	return descriptor, exists
}

// IsBuiltin reports whether Crude serves this connection in-process.
func (config ConnectionConfig) IsBuiltin() bool { return config.Transport == TransportBuiltin }

// DefaultConnections returns the built-in connections every installation has.
func DefaultConnections() map[string]ConnectionConfig {
	return map[string]ConnectionConfig{
		WebConnectionID: {
			ID: WebConnectionID, Name: builtinServers[WebConnectionID].Name,
			Transport: TransportBuiltin, Command: WebConnectionID, Scope: ScopeApplication,
		},
	}
}
