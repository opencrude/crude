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

// mountURL is where a wire is mounted; tests append the real API path, such
// as /v1/messages, to it. Accounts use BaseURL, which includes the API root.
func mountURL(s *Server, wire Wire) string { return s.http.URL + wireRoutes[wire].mount }

func TestProxyBodyLimitRefusesLargeBodiesBeforeTheModel(t *testing.T) {
	s := New(t, WithProxyBodyLimit(1024))
	s.Script(Text("small request served"))
	post := func(size int) (*http.Response, string) {
		t.Helper()
		body := `{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("x", size) + `"}]}`
		res, err := http.Post(s.BaseURL(Chat)+"/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return res, string(readResponse(t, res))
	}
	res, page := post(4096)
	if res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(page, "413 Request Entity Too Large") || s.ProxyRejections() != 1 {
		t.Fatalf("large body = %d %q rejections=%d", res.StatusCode, page, s.ProxyRejections())
	}
	if len(s.Requests()) != 0 {
		t.Fatal("a refused body reached the model")
	}
	if res, reply := post(16); res.StatusCode != http.StatusOK || !strings.Contains(reply, "small request served") || len(s.Requests()) != 1 {
		t.Fatalf("small body = %d %q", res.StatusCode, reply)
	}
}
