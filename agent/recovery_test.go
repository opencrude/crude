package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
)

func TestGatewayInterruptionContinuesWithoutReplayingOrExecutingPartialTools(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Verified prefix.\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"partial\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"unfinished\"}}]}}]}\n\ndata: {\"error\":{\"type\":\"gateway_stream_terminated\",\"message\":\"upstream closed\"}}\n\n")
		case 2:
			last := request.Messages[len(request.Messages)-1].ContentString()
			if !strings.Contains(last, "No tool from that response was executed") {
				t.Errorf("unsafe stochastic replay instead of continuation: %q", last)
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"complete\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"real.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		default:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished safely.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	a := newTestAgent(t, p, "test")
	a.Yolo = true
	executed := 0
	a.ToolFn = func(_ context.Context, name string, args map[string]any) (tools.ToolResult, error) {
		executed++
		if name != "read_file" || args["path"] != "real.go" {
			t.Errorf("partial operation executed: %s %v", name, args)
		}
		return tools.ToolResult{Content: "contents"}, nil
	}
	errorsSeen := 0
	history := a.Run(t.Context(), nil, "system", func(event AgentEvent) {
		if event.Type == "error" {
			errorsSeen++
			t.Log(event.Data)
		}
	}, nil, "", nil)
	if requests.Load() != 3 || executed != 1 || errorsSeen != 0 {
		t.Fatalf("requests=%d executed=%d errors=%d", requests.Load(), executed, errorsSeen)
	}
	partial, controls := 0, 0
	for _, msg := range history {
		if msg.ContentString() == "Verified prefix." {
			partial++
		}
		if msg.EffectiveOrigin() == provider.OriginContinuation {
			controls++
		}
	}
	if partial != 1 || controls != 1 {
		t.Fatalf("lost or duplicated accepted prefix: partial=%d controls=%d", partial, controls)
	}
}

func TestStopDuringRetryDoesNotStartAnotherAttempt(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, `{"error":{"type":"server_error","message":"unavailable"}}`)
	}))
	defer s.Close()
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", s.URL), "test")
	a.MaxRetries = 3
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := time.Now()
	retries := 0
	history := a.Run(ctx, nil, "system", func(event AgentEvent) {
		if event.Type == "retry" {
			retries++
			cancel()
		}
	}, nil, "", nil)
	if time.Since(started) > time.Second || requests.Load() != 1 || retries != 1 {
		t.Fatalf("stop failed to interrupt backoff: requests=%d retries=%d elapsed=%s", requests.Load(), retries, time.Since(started))
	}
	if history[len(history)-1].EffectiveOrigin() != provider.OriginTurnStopped {
		t.Fatal("stop boundary missing from durable context")
	}
}

func TestStopPreservesPartialResponseWithoutCallingProviderAgain(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Useful findings so far.\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", s.URL), "test")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	history := a.Run(ctx, nil, "system", func(event AgentEvent) {
		if event.Type == "stream_token" {
			cancel()
		}
	}, nil, "", nil)
	found := false
	for _, msg := range history {
		found = found || msg.ContentString() == "Useful findings so far."
	}
	if !found || requests.Load() != 1 || history[len(history)-1].EffectiveOrigin() != provider.OriginTurnStopped {
		t.Fatalf("stop lost accepted work or reissued request: found=%v requests=%d", found, requests.Load())
	}
}

func TestRepeatedPartialFailuresHaveBoundedContinuations(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"fragment %d\"}}]}\n\ndata: {\"error\":{\"type\":\"gateway_stream_terminated\",\"message\":\"closed\"}}\n\n", n)
	}))
	defer s.Close()
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", s.URL), "test")
	errorsSeen := 0
	history := a.Run(t.Context(), nil, "system", func(event AgentEvent) {
		if event.Type == "error" {
			errorsSeen++
		}
	}, nil, "", nil)
	if requests.Load() != 4 || errorsSeen != 1 {
		t.Fatalf("unbounded recovery: requests=%d errors=%d", requests.Load(), errorsSeen)
	}
	fragments := 0
	for _, msg := range history {
		if msg.Role == "assistant" && strings.HasPrefix(msg.ContentString(), "fragment ") {
			fragments++
		}
	}
	if fragments != 4 {
		t.Fatalf("final recovery failure lost accepted content: %d", fragments)
	}
}

func TestPermissionFailureDoesNotBlameWorkingCredentials(t *testing.T) {
	a := newTestAgent(t, provider.FromPreset("openai-chat", "key", ""), "test")
	message := ""
	a.emitError(func(event AgentEvent) { message, _ = event.Data.(string) }, &apiCallError{StatusCode: 403, Cause: &provider.StatusError{StatusCode: 403, Message: "model not allowed"}})
	if !strings.Contains(message, "permissions") || strings.Contains(message, "API key") {
		t.Fatalf("misleading permanent-failure guidance: %q", message)
	}
}
