package capability

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TransportBuiltin marks a connection served in-process by Crude itself. Its
// server is Crude's own code: it needs no launch approval, its tools keep their
// plain names, and it can be disabled but not removed.
const TransportBuiltin = "builtin"

// TransportSSE is the 2024-11-05 HTTP+SSE remote transport.
const TransportSSE = "sse"

// WebConnectionID is the built-in web search and fetch connection.
const WebConnectionID = "web"

const (
	// WebSearchOutage is a local result code, not MCP server text. Every
	// search method failed, so the failed built-in search row links to
	// Search methods.
	WebSearchOutage = "web_search_outage"
	// Search method operations act on one route of the built-in web
	// connection. Save and Remove change Application settings; Test runs one
	// search and changes nothing.
	SaveWebSearchRoute   = "save-web-search-route"
	RemoveWebSearchRoute = "remove-web-search-route"
	TestWebSearchRoute   = "test-web-search-route"
	// MaxWebSearchRoutes bounds the account methods one search starts.
	MaxWebSearchRoutes = 8
)

// WebSearchRoute is one account search method. The built-in web_search races
// every route that is on with its other engines. It stores identities only,
// never secrets. Model is empty when the account's protocol searches without
// a model.
type WebSearchRoute struct {
	ID       string `json:"id"`
	Account  string `json:"account"`
	Model    string `json:"model,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Valid checks the stored identities. It does not check the account.
func (route WebSearchRoute) Valid() bool {
	return validSearchIdentity(route.ID, 64) && validSearchIdentity(route.Account, 256) &&
		(route.Model == "" || validSearchIdentity(route.Model, 1024))
}

// WebSearchObservation is the latest outcome Crude saw for one route, from a
// Test or from a live search. Detail is a short reason when Working is false.
type WebSearchObservation struct {
	Working   bool   `json:"working"`
	Detail    string `json:"detail,omitempty"`
	Results   int    `json:"results"`
	LatencyMS int64  `json:"latency_ms"`
	At        int64  `json:"at"`
}

func validSearchIdentity(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && strings.TrimSpace(value) == value &&
		utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func (config ConnectionConfig) IsBuiltinWeb() bool {
	return config.ID == WebConnectionID && config.Command == WebConnectionID && config.IsBuiltin()
}

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
