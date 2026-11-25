package provider

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
)

// mountTestWire exposes an existing wire at a gateway base URL. The wrapper
// can observe the original request or answer an operation the wire lacks.
// Requests passed to next use paths relative to the simulator's account base.
func mountTestWire(t *testing.T, base string, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	transport := &http.Transport{DisableKeepAlives: true}
	proxy.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	server := httptest.NewServer(wrap(proxy))
	t.Cleanup(server.Close)
	return server.URL
}
