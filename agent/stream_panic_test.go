package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
	"crude/types/recovery"
)

type panicAfterSemanticFrame struct{ frame string }

func (reader *panicAfterSemanticFrame) Read(buffer []byte) (int, error) {
	if reader.frame == "" {
		panic("injected decoder read failure")
	}
	n := copy(buffer, reader.frame)
	reader.frame = reader.frame[n:]
	return n, nil
}

func (*panicAfterSemanticFrame) Close() error { return nil }

func TestStreamPanicPreservesAcceptedOutputAndRequiresExplicitContinuation(t *testing.T) {
	agent := newTestAgent(t, provider.FromPreset("openai-chat", "key", "http://provider.invalid"), "model")
	agent.ContextCompactionPercent = 0
	var requests, executions atomic.Int32
	agent.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executions.Add(1)
		return tools.ToolResult{}, nil
	}
	agent.SetHTTPClient(&http.Client{Transport: agentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: &panicAfterSemanticFrame{frame: "data: {\"choices\":[{\"delta\":{\"content\":\"Accepted before panic.\",\"tool_calls\":[{\"index\":0,\"id\":\"partial\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\"}}]}}]}\n\n"},
			}, nil
		}
		var decoded provider.ChatRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Error(err)
		}
		prefixes := 0
		for _, message := range decoded.Messages {
			if message.ContentString() == "Accepted before panic." {
				prefixes++
			}
			if len(message.ToolCalls) > 0 {
				t.Error("partial tool from panicked response was replayed")
			}
		}
		if prefixes != 1 || !strings.Contains(decoded.Messages[len(decoded.Messages)-1].ContentString(), `runtime_recovery kind="uncertain_effects"`) {
			t.Errorf("accepted prefix/continuation missing after explicit wake: %+v", decoded.Messages)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"Completed safely.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
	})})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	supervisor := NewSupervisor("owner", 1, nil)
	wake, attention := make(chan struct{}, 1), make(chan recovery.Status, 1)
	supervisor.Wake = wake
	supervisor.Persist = func(recovery.Status) error { return nil }
	supervisor.Emit = func(event AgentEvent) {
		if status := event.Data.(recovery.Status); status.State == recovery.NeedsAttention {
			attention <- status
		}
	}
	done := make(chan SupervisedRunResult, 1)
	go func() {
		done <- agent.RunSupervised(ctx, []provider.ChatMessage{provider.NewTextMessage("user", "finish safely")},
			"system", func(AgentEvent) {}, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor})
	}()
	select {
	case <-attention:
	case <-time.After(3 * time.Second):
		t.Fatal("internal stream failure did not park its owner")
	}
	if requests.Load() != 1 || executions.Load() != 0 {
		t.Fatal("panic caused automatic stochastic/tool replay")
	}
	wake <- struct{}{}
	select {
	case result := <-done:
		accepted := 0
		for _, message := range result.Archive {
			if message.ContentString() == "Accepted before panic." {
				accepted++
			}
		}
		if accepted != 1 || executions.Load() != 0 || requests.Load() != 2 {
			t.Fatalf("accepted work lost/duplicated: accepted=%d tools=%d requests=%d", accepted, executions.Load(), requests.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("explicit continuation did not complete")
	}
}
