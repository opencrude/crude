package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
)

func TestConsumedCompletionDoesNotNotifyActiveOrIdleParent(t *testing.T) {
	requests := make(chan provider.ChatRequest, 4)
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		requests <- req
		w.Header().Set("Content-Type", "text/event-stream")
		hasToolResult := false
		for _, m := range req.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
		}
		if !hasToolResult {
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-plan\",\"type\":\"function\",\"function\":{\"name\":\"get_plan\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	sess, err := NewWithSessionDir("wake-consumed", modelAttributionConfig(server.URL, "wake-model"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sender := newMockEventSender()
	orch, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !orch.SubmitInput(provider.PlainInput("do work")) {
		t.Fatal("turn rejected")
	}
	select {
	case <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("first request missing")
	}
	sess.wakeCh <- WakeEvent{Type: WakeSubAgentDone, SourceID: "child", Data: agent.SubAgentResult{AgentID: "child", Status: agent.SubAgentCompleted, SummaryGist: "should not repeat"}}
	sess.MarkReportConsumed("child") // synchronous wait, poll or explicit cancellation consumed the result
	close(releaseFirst)
	select {
	case next := <-requests:
		for _, m := range next.Messages {
			if strings.Contains(m.ContentString(), "should not repeat") {
				t.Fatal("already consumed child was notified")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no continuation request")
	}
	waitTurnDone(t, sender, orch.ID)
	select {
	case <-requests:
		t.Fatal("already consumed child triggered wake turn")
	case <-time.After(200 * time.Millisecond):
	}
}
