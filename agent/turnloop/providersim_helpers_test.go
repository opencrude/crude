package turnloop

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

// The compaction budget tests compare Crude's canonical token estimates, not
// the simulator's synthetic tokenizer. Decode the request actually sent.
func canonicalChatRequest(t *testing.T, r providersim.Request) provider.ChatRequest {
	t.Helper()
	var req provider.ChatRequest
	if err := json.Unmarshal(r.Raw, &req); err != nil {
		t.Errorf("decode Chat request #%d: %v", r.Index, err)
	}
	return req
}

func simulatedText(parts []providersim.Part) string {
	var text strings.Builder
	for _, p := range parts {
		text.WriteString(p.Text)
	}
	return text.String()
}

// A gateway may translate a native provider failure to a foreign envelope.
// Keep the simulator authoritative for both the model turn and the journal;
// rewrite only the response that this particular gateway would translate.
func translatingGateway(t *testing.T, sim *providersim.Server, wire providersim.Wire, rewrite func(*http.Response) error) string {
	t.Helper()
	upstream, err := url.Parse(sim.BaseURL(wire))
	if err != nil {
		t.Fatal(err)
	}
	path := upstream.Path
	upstream.Path = ""
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ModifyResponse = rewrite
	gateway := httptest.NewServer(proxy)
	t.Cleanup(gateway.Close)
	return gateway.URL + path
}

func gatewayBody(r *http.Response, text string) {
	r.Body = io.NopCloser(strings.NewReader(text))
	r.ContentLength = int64(len(text))
	r.Header.Del("Content-Length")
	r.TransferEncoding = nil
}
