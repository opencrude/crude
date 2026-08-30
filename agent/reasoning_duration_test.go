package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"crude/provider"
)

// TestCompleteStreamRecordsReasoningDuration verifies that reasoning time is
// measured across the reasoning deltas of a real stream.
func TestCompleteStreamRecordsReasoningDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"event: content_block_start\n"+
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n"+
				"event: content_block_delta\n"+
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"reason\"}}\n\n"+
				"event: content_block_stop\n"+
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
				"event: content_block_start\n"+
				"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
				"event: content_block_delta\n"+
				"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n"+
				"event: message_delta\n"+
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"+
				"event: message_stop\n"+
				"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	p := provider.FromPreset("anthropic", "key", server.URL)
	p.Model = "claude-test"
	agent := newTestAgent(t, p, p.Model)

	result, err := agent.completeStream(
		context.Background(),
		[]provider.ChatMessage{provider.NewTextMessage("user", "hello")},
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("completeStream: %v", err)
	}
	if result.ReasoningContent != "reason" {
		t.Fatalf("reasoning = %q", result.ReasoningContent)
	}
	if result.ReasoningDuration <= 0 {
		t.Fatalf("reasoning duration = %v, want > 0", result.ReasoningDuration)
	}
	if result.Content != "answer" {
		t.Fatalf("content = %q", result.Content)
	}
}
