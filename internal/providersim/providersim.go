// Package providersim runs local, independently specified provider HTTP wires
// for tests. It does not load credentials or contact providers.
package providersim

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// Wire identifies a provider HTTP dialect.
type Wire string

const (
	// Chat is the OpenAI Chat Completions wire.
	Chat Wire = "chat"
	// Responses is the OpenAI Responses wire.
	Responses Wire = "responses"
	// Codex is the ChatGPT Codex Responses wire.
	Codex Wire = "codex"
	// Messages is the Anthropic Messages wire.
	Messages Wire = "messages"
	// Gemini is the Gemini generateContent wire.
	Gemini Wire = "gemini"
	// Interactions is the Gemini Interactions wire.
	Interactions Wire = "interactions"
	// Ollama is the native Ollama wire.
	Ollama Wire = "ollama"
	// FIMOpenAI is OpenAI's legacy text completion wire.
	FIMOpenAI Wire = "openai-completions"
	// FIMDeepSeek is DeepSeek's beta fill-in-the-middle wire.
	FIMDeepSeek Wire = "deepseek-fim"
	// FIMCodestral is Mistral's Codestral fill-in-the-middle wire.
	FIMCodestral Wire = "codestral-fim"
)

type wireRoute struct {
	mount string
	base  string
}

// Distinct mounts keep common operation paths (such as /v1/models) private to
// their dialect. Chat uses the unprefixed paths of the public OpenAI API.
var wireRoutes = map[Wire]wireRoute{
	Chat:         {base: "/v1"},
	Responses:    {mount: "/responses", base: "/v1"},
	Codex:        {mount: "/codex", base: "/backend-api/codex"},
	Messages:     {mount: "/messages", base: "/v1"},
	Gemini:       {mount: "/gemini", base: "/v1beta"},
	Interactions: {mount: "/interactions", base: "/v1beta"},
	Ollama:       {mount: "/ollama", base: "/api"},
	FIMOpenAI:    {mount: "/openai-completions", base: "/v1"},
	FIMDeepSeek:  {mount: "/deepseek-fim", base: "/beta"},
	FIMCodestral: {mount: "/codestral-fim", base: "/v1"},
}

// Each codec registers its local handler once, during package initialization.
var wireHandlers = make(map[Wire]func(*Server) http.Handler)

func registerWire(wire Wire, handler func(*Server) http.Handler) {
	if _, known := wireRoutes[wire]; !known || wireHandlers[wire] != nil {
		panic("invalid simulator wire registration: " + string(wire))
	}
	wireHandlers[wire] = handler
}

// Option configures a Server before it starts accepting requests.
type Option func(*Server)

// WithCacheProfile sets the synthetic cache profile for one wire and model.
func WithCacheProfile(wire Wire, model string, profile CacheProfile) Option {
	return func(s *Server) {
		if profile.Minimum < 0 || profile.Granularity < 0 {
			s.t.Fatalf("simulate %s model %q: cache thresholds must be non-negative", wire, model)
		}
		s.cacheProfiles[prefixKey{wire: wire, model: model}] = profile
	}
}

// WithProxyBodyLimit puts a reverse proxy with a request body limit in front
// of every wire, as nginx's client_max_body_size does (1 MiB by default): a
// larger request gets 413 with the proxy's HTML page and never reaches the
// model. https://nginx.org/en/docs/http/ngx_http_core_module.html#client_max_body_size
func WithProxyBodyLimit(limit int64) Option {
	return func(s *Server) { s.bodyLimit = limit }
}

// proxyLimitPage is the body nginx sends with its 413.
const proxyLimitPage = "<html>\r\n<head><title>413 Request Entity Too Large</title></head>\r\n<body>\r\n<center><h1>413 Request Entity Too Large</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

// WithModels sets the model IDs returned by the wire's model inventory.
func WithModels(wire Wire, ids ...string) Option {
	rows := make([]Model, len(ids))
	for i, id := range ids {
		rows[i].ID = id
	}
	return WithModelCatalog(wire, rows...)
}

// Server is one test-owned HTTP endpoint for all configured wires. Its journal
// and scenario methods can be used concurrently.
type Server struct {
	t      testing.TB
	report func(string, ...any)
	http   *httptest.Server
	done   chan struct{}

	cacheProfiles map[prefixKey]CacheProfile
	catalogs      map[Wire]map[string][]Model
	chatGateways  map[string]ChatGateway
	responses     *responseState

	mu        sync.Mutex
	journal   []Request
	prefixes  map[prefixKey]*prefixNode
	turns     []Turn
	responder func(Request) Turn

	bodyLimit       int64
	proxyRejections atomic.Int32
}

// New starts an HTTP simulator and registers its cleanup with t.
func New(t testing.TB, opts ...Option) *Server {
	return newServer(t, t.Errorf, opts)
}

func newServer(t testing.TB, report func(string, ...any), opts []Option) *Server {
	s := &Server{
		t: t, report: report, done: make(chan struct{}),
		cacheProfiles: make(map[prefixKey]CacheProfile),
		chatGateways:  make(map[string]ChatGateway), prefixes: make(map[prefixKey]*prefixNode),
	}
	for _, opt := range opts {
		opt(s)
	}
	mux := http.NewServeMux()
	for wire, route := range wireRoutes {
		var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, err := receive(wire, r)
			if err != nil {
				req.Violation = err.Error()
			}
			s.observe(req)
			http.Error(w, string(wire)+": not simulated yet", http.StatusNotImplemented)
		})
		if factory := wireHandlers[wire]; factory != nil {
			handler = factory(s)
		}
		mux.Handle(route.mount+"/", http.StripPrefix(route.mount, handler))
		if route.mount != "" {
			mux.Handle(route.mount, http.StripPrefix(route.mount, handler))
		}
	}
	var front http.Handler = mux
	if s.bodyLimit > 0 {
		front = s.proxy(mux)
	}
	s.http = httptest.NewServer(front)
	t.Cleanup(func() {
		close(s.done)
		s.http.CloseClientConnections()
		s.http.Close()
	})
	return s
}

// proxy reads the whole body before it answers, so the client always
// receives the 413 instead of a reset connection.
func (s *Server) proxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if int64(len(body)) > s.bodyLimit {
			s.proxyRejections.Add(1)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = io.WriteString(w, proxyLimitPage)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// ProxyRejections counts requests the WithProxyBodyLimit proxy refused.
func (s *Server) ProxyRejections() int { return int(s.proxyRejections.Load()) }

// BaseURL returns the account base URL for wire. It returns an empty string
// for an unknown Wire value.
func (s *Server) BaseURL(wire Wire) string {
	if route, ok := wireRoutes[wire]; ok {
		return s.http.URL + route.mount + route.base
	}
	return ""
}

// Script appends turns to the FIFO used by valid model requests on any wire.
func (s *Server) Script(turns ...Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, turn := range turns {
		s.turns = append(s.turns, turn.snapshot())
	}
}

// Respond sets the fallback used when no scripted turns remain. The callback
// runs with an independent journal entry and no server lock. It may run
// concurrently for overlapping requests. A nil callback disables the fallback.
func (s *Server) Respond(responder func(Request) Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responder = responder
}

// Requests returns independent copies of the journal in model dispatch order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	entries := slices.Clone(s.journal)
	s.mu.Unlock()
	for i := range entries {
		entries[i] = entries[i].snapshot()
	}
	return entries
}

// Last returns an independent copy of the latest journal entry and whether
// any request has been observed.
func (s *Server) Last() (Request, bool) {
	s.mu.Lock()
	if len(s.journal) == 0 {
		s.mu.Unlock()
		return Request{}, false
	}
	req := s.journal[len(s.journal)-1]
	s.mu.Unlock()
	return req.snapshot(), true
}

func receive(wire Wire, r *http.Request) (Request, error) {
	raw, err := io.ReadAll(r.Body)
	// A cancelled upload is not a malformed JSON request. Stop the HTTP
	// handler without consuming a scripted turn; complete bodies still go
	// through every wire's independent schema validation.
	if err != nil && r.Context().Err() != nil {
		panic(http.ErrAbortHandler)
	}
	return Request{Wire: wire, Method: r.Method, Path: r.URL.Path, Target: r.RequestURI, Header: r.Header.Clone(), Raw: raw}, err
}

// observe journals metadata and rejected requests without consuming a turn or
// modifying the model prefix history.
func (s *Server) observe(req Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req.Index = len(s.journal) + 1
	s.journal = append(s.journal, req)
}

// dispatch makes the accepted request and its prefix visible before invoking
// a responder or waiting at a gate.
func (s *Server) dispatch(req *Request, blocks []block, fallback CacheProfile) (Turn, bool) {
	return s.dispatchWithPrefix(req, blocks, fallback, nil)
}

// dispatchWithPrefix is dispatch for a wire whose cache accounting depends on
// state beyond the shared prefix, such as explicit breakpoints. adjust runs
// under the server lock after the prefix is recorded and before the request
// enters the journal, so concurrent requests see consistent cache state.
func (s *Server) dispatchWithPrefix(req *Request, blocks []block, fallback CacheProfile, adjust func(*Prefix, CacheProfile)) (Turn, bool) {
	key := prefixKey{wire: req.Wire, model: req.Model}
	profile, ok := s.cacheProfiles[key]
	if !ok {
		profile = fallback
	}
	s.mu.Lock()
	req.Index = len(s.journal) + 1
	root := s.prefixes[key]
	if root == nil {
		root = &prefixNode{}
		s.prefixes[key] = root
	}
	req.Prefix = root.record(req.Index, blocks, profile)
	if adjust != nil {
		adjust(&req.Prefix, profile)
	}
	s.journal = append(s.journal, *req)
	if len(s.turns) != 0 {
		turn := s.turns[0]
		s.turns[0] = Turn{}
		s.turns = s.turns[1:]
		s.mu.Unlock()
		return s.checkedTurn(req.Wire, turn), true
	}
	responder := s.responder
	s.mu.Unlock()
	if responder != nil {
		return s.checkedTurn(req.Wire, responder(req.snapshot()).snapshot()), true
	}
	s.report("simulate %s %s: no turn scripted; use Script or Respond", req.Wire, req.Path)
	return Turn{}, false
}

func (s *Server) checkedTurn(wire Wire, t Turn) Turn {
	if err := t.validateToolDeltas(wire); err != nil {
		s.report("simulate %s: %v", wire, err)
		// Synthetic fixture failure; use the codec's own internal-error body.
		return HTTPError("").WithFaultMessage(err.Error())
	}
	return t
}
