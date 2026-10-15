package mcpclient

import (
	"net/http"
	"sync/atomic"

	captypes "crude/types/capability"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TransportSSE is the 2024-11-05 HTTP+SSE transport. Servers that still speak
// it can be configured explicitly; a Streamable HTTP connection also falls back
// to it automatically when the server rejects the Streamable handshake.
const TransportSSE = captypes.TransportSSE

// handshakeProbe records the first HTTP status of a Streamable HTTP connect.
type handshakeProbe struct {
	status atomic.Int32
}

// legacyServer reports whether the server answered the initialize POST the way
// a legacy HTTP+SSE server does (method, path or media type not accepted).
func (probe *handshakeProbe) legacyServer() bool {
	if probe == nil {
		return false
	}
	switch probe.status.Load() {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusUnsupportedMediaType:
		return true
	}
	return false
}

type probingTransport struct {
	base  http.RoundTripper
	probe *handshakeProbe
}

func (transport probingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost {
		transport.probe.status.CompareAndSwap(0, int32(response.StatusCode))
	}
	return response, err
}

// probeHandshake instruments a Streamable HTTP transport; other transports are
// returned unprobed.
func probeHandshake(transport mcp.Transport) *handshakeProbe {
	streamable, ok := transport.(*mcp.StreamableClientTransport)
	if !ok || streamable.HTTPClient == nil {
		return nil
	}
	// Only a policy-owned transport is wrapped; a client without one is
	// never given the process default.
	if streamable.HTTPClient.Transport == nil {
		return nil
	}
	probe := &handshakeProbe{}
	client := *streamable.HTTPClient
	client.Transport = probingTransport{base: client.Transport, probe: probe}
	streamable.HTTPClient = &client
	return probe
}

// legacySSE builds the HTTP+SSE equivalent of a Streamable HTTP transport,
// keeping its endpoint, headers, proxy and response bounds.
func legacySSE(transport mcp.Transport) mcp.Transport {
	streamable := transport.(*mcp.StreamableClientTransport)
	client := *streamable.HTTPClient
	if probing, ok := client.Transport.(probingTransport); ok {
		client.Transport = probing.base
	}
	return &mcp.SSEClientTransport{Endpoint: streamable.Endpoint, HTTPClient: &client, MaxEventSize: streamable.MaxEventSize}
}
