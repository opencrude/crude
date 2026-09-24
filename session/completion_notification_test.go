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

// A child finishes during an active parent turn that has another tool round.
// The next model request, rather than a new idle wake turn, receives the result.
func TestCompletionNotificationInActiveParentRequest(t *testing.T) {
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
	cfg := modelAttributionConfig(server.URL, "wake-model")
	sess, err := NewWithSessionDir("wake-active", cfg, t.TempDir(), t.TempDir())
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
	sess.wakeCh <- WakeEvent{Type: WakeSubAgentDone, SourceID: "child", Data: agent.SubAgentResult{AgentID: "child", Name: "child", Status: agent.SubAgentCompleted, SummaryGist: "notification sentinel"}}
	close(releaseFirst)
	var next provider.ChatRequest
	select {
	case next = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("no second request during active turn")
	}
	hits := 0
	for _, m := range next.Messages {
		if m.Role == "user" && strings.Contains(m.ContentString(), "notification sentinel") {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("active request notification count = %d, messages = %+v", hits, next.Messages)
	}
	waitTurnDone(t, sender, orch.ID)
	select {
	case extra := <-requests:
		t.Fatalf("redundant wake turn request: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}
