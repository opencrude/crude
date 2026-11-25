package turnloop

import (
	"context"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/internal/providersim"
	"crude/types/reasoning"
)

// TestCompleteStreamRecordsReasoningDuration verifies that reasoning time is
// measured across the reasoning deltas of a real stream.
func TestCompleteStreamRecordsReasoningDuration(t *testing.T) {
	server := providersim.New(t)
	gate := providersim.NewGate()
	defer gate.Release()
	server.Script(providersim.Text("answer").WithReasoning("reason", "", "").WithGateAfter(4, gate))

	p := provider.FromPreset("anthropic", "key", server.BaseURL(providersim.Messages))
	p.Model = "claude-test"
	agent := newTestAgent(t, p, p.Model)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := agent.completeStream(
		ctx,
		[]provider.ChatMessage{provider.NewTextMessage("user", "hello")},
		nil, nil, func(reasoning.Delta) { time.AfterFunc(20*time.Millisecond, gate.Release) }, nil, nil, nil, nil, nil,
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
