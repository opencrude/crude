package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/internal/providersim"
)

func simText(parts []providersim.Part) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

// A valid tool round needs both its call and the matching result.
func denseToolHistory() []provider.ChatMessage {
	history := make([]provider.ChatMessage, 0, 40)
	for i := range 20 {
		id := fmt.Sprintf("evidence-%d", i)
		call := provider.NewTextMessage("assistant", strings.Repeat("analysis ", 1000))
		call.ToolCalls = []provider.ToolCall{{
			ID: id, Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"evidence.txt"}`},
		}}
		result := provider.NewTextMessage("tool", strings.Repeat("output ", 1000))
		result.ToolCallID = id
		history = append(history, call, result)
	}
	return history
}

func simTool(id, name, arguments string) providersim.Turn {
	return providersim.CallTools(providersim.ToolCall{ID: id, Name: name, Arguments: arguments})
}

// simRawRequest preserves wire fields for assertions that inspect the exact JSON shape.
func simRawRequest(request providersim.Request) map[string]any {
	var raw map[string]any
	// A responder runs only for a request already decoded and validated by the simulator.
	_ = json.Unmarshal(request.Raw, &raw)
	return raw
}

// simDecodeRequest keeps assertions that depend on exact wire shape on the journal.
func simDecodeRequest[T any](t *testing.T, request providersim.Request) T {
	var value T
	if err := json.Unmarshal(request.Raw, &value); err != nil {
		t.Errorf("decode simulator request: %v", err)
	}
	return value
}

func simReply(t *testing.T, replies <-chan providersim.Turn) providersim.Turn {
	select {
	case reply := <-replies:
		return reply
	case <-t.Context().Done():
		return providersim.HTTPError(providersim.InvalidRequest)
	case <-time.After(5 * time.Second):
		t.Error("scripted provider response was not released within its bound")
		return providersim.HTTPError(providersim.InvalidRequest)
	}
}

func simToolArgs(id, name string, params map[string]any) providersim.ToolCall {
	arguments, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	return providersim.ToolCall{ID: id, Name: name, Arguments: string(arguments)}
}

var sessionSimWires = map[string]providersim.Wire{
	"openai": providersim.Chat, "openai-responses": providersim.Responses, "codex": providersim.Codex,
	"anthropic": providersim.Messages, "gemini": providersim.Gemini,
	"gemini-interactions": providersim.Interactions, "ollama": providersim.Ollama,
}

func sessionSimWire(protocol string) providersim.Wire { return sessionSimWires[protocol] }

func sessionSimProtocol(wire providersim.Wire) string {
	for protocol, registered := range sessionSimWires {
		if registered == wire {
			return protocol
		}
	}
	return ""
}

// simStableEndpoint keeps the connection URL unchanged when a test switches
// wire. The proxy adds only the simulator mount; it does not serve responses.
func simStableEndpoint(t *testing.T, sim *providersim.Server, initial providersim.Wire) (string, func(providersim.Wire)) {
	t.Helper()
	type dispatch struct {
		target *url.URL
		api    string
	}
	var selected atomic.Pointer[dispatch]
	setWire := func(wire providersim.Wire) {
		t.Helper()
		base, err := url.Parse(sim.BaseURL(wire))
		if err != nil || base.Host == "" {
			t.Fatalf("no simulator endpoint for wire %q: %v", wire, err)
		}
		mount := ""
		if wire != providersim.Chat {
			mount, _, _ = strings.Cut(strings.TrimPrefix(base.Path, "/"), "/")
			mount = "/" + mount
		}
		selected.Store(&dispatch{target: base, api: strings.TrimPrefix(base.Path, mount)})
	}
	setWire(initial)
	server := httptest.NewServer(&httputil.ReverseProxy{
		Transport: simProxyTransport(t),
		Rewrite: func(r *httputil.ProxyRequest) {
			current := selected.Load()
			if strings.HasPrefix(r.Out.URL.Path, current.api+"/") {
				r.Out.URL.Path = strings.TrimPrefix(r.Out.URL.Path, current.api)
				r.Out.URL.RawPath = strings.TrimPrefix(r.Out.URL.RawPath, current.api)
			}
			r.SetURL(current.target)
		},
	})
	t.Cleanup(server.Close)
	return server.URL, setWire
}

func simReverseProxy(t *testing.T, endpoint func() *url.URL) http.Handler {
	t.Helper()
	return &httputil.ReverseProxy{
		Transport: simProxyTransport(t),
		Rewrite:   func(r *httputil.ProxyRequest) { r.SetURL(endpoint()) },
	}
}

func simProxyTransport(t *testing.T) *http.Transport {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

// simChatForwarder sends the ordinary branches of an exact gateway or
// transport fault test to the simulator. Keep the original attached HTTP
// request (including its cancellation) and replace only its consumed body.
func simChatForwarder(t *testing.T, sim *providersim.Server) func(http.ResponseWriter, *http.Request, []byte) {
	t.Helper()
	target, err := url.Parse(sim.BaseURL(providersim.Chat))
	if err != nil {
		t.Fatal(err)
	}
	target.Path = ""
	proxy := simReverseProxy(t, func() *url.URL { return target })
	return func(w http.ResponseWriter, r *http.Request, raw []byte) {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		proxy.ServeHTTP(w, r)
	}
}
