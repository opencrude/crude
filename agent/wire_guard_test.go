package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"crude/provider"
)

func TestWireGuardRejectsMutationBeforeHTTPAndAllowsAppend(t *testing.T) {
	guard := NewWireGuard()
	if _, err := guard.EnsureEnvelope("model", "system", nil); err != nil {
		t.Fatal(err)
	}
	first := []provider.ChatMessage{
		provider.NewTextMessage("system", "system"),
		provider.NewTextMessage("user", "one"),
	}
	candidate, err := guard.Check(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Accept(candidate); err != nil {
		t.Fatal(err)
	}
	extended := append(append([]provider.ChatMessage(nil), first...), provider.NewTextMessage("assistant", "two"))
	if _, err := guard.Check(extended); err != nil {
		t.Fatalf("append-only extension rejected: %v", err)
	}
	mutated := append([]provider.ChatMessage(nil), extended...)
	mutated[1] = provider.NewTextMessage("user", "changed")
	if _, err := guard.Check(mutated); err == nil {
		t.Fatal("mutated accepted prefix was allowed")
	}
	guard.Rotate("explicit_compaction")
	if _, err := guard.EnsureEnvelope("model", "system", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Check(mutated); err != nil {
		t.Fatalf("explicit new epoch rejected: %v", err)
	}
}

func TestAgentWirePrefixContinuesAcrossUserTurns(t *testing.T) {
	var count atomic.Int32
	requests := make(chan []provider.ChatMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request.Messages
		writeChatSSE(w, "answer", "stop")
		count.Add(1)
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "test-model"
	a := newTestAgent(t, p, p.Model)
	a.WireGuard = NewWireGuard()
	firstLedger := a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "first")},
		"system", func(AgentEvent) {}, nil, nil, "", nil)
	firstSession := append([]provider.ChatMessage(nil), firstLedger[1:]...)
	secondInput := append(firstSession, provider.NewTextMessage("user", "second"))
	_ = a.Run(context.Background(), secondInput, "system", func(AgentEvent) {}, nil, nil, "", nil)
	close(requests)
	var captured [][]provider.ChatMessage
	for request := range requests {
		captured = append(captured, request)
	}
	if count.Load() != 2 || len(captured) != 2 || len(captured[1]) <= len(captured[0]) {
		t.Fatalf("requests=%d captured=%d", count.Load(), len(captured))
	}
	for i := range captured[0] {
		first, _ := json.Marshal(captured[0][i])
		second, _ := json.Marshal(captured[1][i])
		if string(first) != string(second) {
			t.Fatalf("cross-turn prefix mutated at %d\nfirst=%s\nsecond=%s", i, first, second)
		}
	}
}

func TestCompactWireEpochBoundsActiveProjectionAndKeepsCheckpoint(t *testing.T) {
	var messages []provider.ChatMessage
	for i := 0; i < 40; i++ {
		messages = append(messages,
			provider.NewTextMessage("assistant", strings.Repeat("analysis ", 500)),
			provider.NewTextMessage("tool", strings.Repeat("tool output ", 500)),
		)
	}
	compacted := compactWireEpoch(messages, 8192, "test-model")
	if len(compacted) >= len(messages) || !compacted[0].WireHidden ||
		!strings.Contains(compacted[0].Content.String(), "<wire_checkpoint") {
		t.Fatalf("active epoch was not compacted: before=%d after=%d first=%q",
			len(messages), len(compacted), compacted[0].Content.String())
	}
	if len(messages) != 80 || !strings.HasPrefix(messages[0].Content.String(), "analysis") {
		t.Fatal("immutable archive input was mutated")
	}
}

func TestWireGuardDoesNotCommitRejectedCandidate(t *testing.T) {
	guard := NewWireGuard()
	_, _ = guard.EnsureEnvelope("model", "system", nil)
	rejected, err := guard.Check([]provider.ChatMessage{provider.NewTextMessage("user", "unsent")})
	if err != nil {
		t.Fatal(err)
	}
	_ = rejected // no Accept: provider rejected before model admission
	if _, err := guard.Check([]provider.ChatMessage{provider.NewTextMessage("user", "replacement")}); err != nil {
		t.Fatalf("unaccepted candidate polluted prefix state: %v", err)
	}
}

func TestWireGuardRotatesExplicitlyWhenEnvelopeOptionsChange(t *testing.T) {
	guard := NewWireGuard()
	rotated, err := guard.EnsureEnvelope("model", "system", nil, map[string]any{"temperature": 0.1})
	if err != nil || rotated {
		t.Fatalf("initial envelope rotated=%v err=%v", rotated, err)
	}
	before := guard.Epoch()
	rotated, err = guard.EnsureEnvelope("model", "system", nil, map[string]any{"temperature": 0.2})
	if err != nil || !rotated || guard.Epoch() != before+1 || guard.Reason() != "envelope_changed" {
		t.Fatalf("changed envelope rotated=%v epoch=%d reason=%q err=%v", rotated, guard.Epoch(), guard.Reason(), err)
	}
}
