package providersim

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWireRoutesShareOneServer(t *testing.T) {
	s := New(t)
	chat, err := url.Parse(s.BaseURL(Chat))
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []Wire{Chat, Responses, Codex, Messages, Gemini, Interactions, Ollama} {
		endpoint, err := url.Parse(s.BaseURL(wire))
		if err != nil || endpoint.Host != chat.Host {
			t.Fatalf("%s is not served by the same HTTP server: %s, %v", wire, endpoint, err)
		}
		if wireHandlers[wire] != nil {
			continue // The codec owns the behavior of its routes.
		}
		res := get(t, endpoint.String())
		raw := readResponse(t, res)
		if res.StatusCode != http.StatusNotImplemented || !strings.Contains(string(raw), "not simulated yet") {
			t.Errorf("%s = %d %s", wire, res.StatusCode, raw)
		}
	}
}
