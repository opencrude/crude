package agent

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crude/provider"
)

func TestCompleteStreamOllamaNDJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ollama-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		// Ollama streams one JSON object per line; the final line carries the
		// done flag and usage counts.
		_, _ = fmt.Fprint(w,
			`{"model":"llama3","message":{"role":"assistant","content":"Hel"}}`+"\n"+
				`{"model":"llama3","message":{"role":"assistant","content":"lo"}}`+"\n"+
				`{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":2}`+"\n")
	}))
	defer server.Close()

	p := provider.FromPreset("ollama-cloud", "ollama-key", server.URL)
	p.Model = "llama3"
	agent := newTestAgent(t, p, p.Model)

	result, err := completeForTest(t, agent)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.Content != "Hello" {
		t.Errorf("content = %q, want Hello", result.Content)
	}
	if result.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", result.FinishReason)
	}
	if result.Usage.PromptTokens != 5 || result.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", result.Usage)
	}
}

func TestNDJSONToSSEReaderReemitsLinesAsDataEvents(t *testing.T) {
	r := newNDJSONToSSEReader(bufio.NewReader(strings.NewReader(
		`{"a":1}` + "\n" +
			`   ` + "\n" + // blank line must be skipped
			`{"b":2}` + "\n")))
	var out strings.Builder
	buf := make([]byte, 64)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	got := out.String()
	if !strings.Contains(got, "data: {\"a\":1}\n\n") || !strings.Contains(got, "data: {\"b\":2}\n\n") {
		t.Errorf("output missing SSE data events: %q", got)
	}
	if strings.Contains(got, "data:    \n") {
		t.Errorf("blank line should not be emitted as an event: %q", got)
	}
}
