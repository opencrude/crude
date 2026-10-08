package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"crude/memory"
	"crude/provider"
)

func TestMemoryShutdownCancelsMaintenanceDelay(t *testing.T) {
	s := &Session{sessionID: "closing", memoryStore: newTestMemoryStore(t)}
	s.StartMemoryMaintenance()
	start := time.Now()
	s.finishMemoryJobs()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown waited on optional memory work: %s", elapsed)
	}
	if s.memoryJobs.maintaining.Load() || s.memoryJobs.extracting.Load() {
		t.Fatal("memory worker outlived shutdown")
	}
}

func TestMemoryShutdownFinishesDurableTurnMirror(t *testing.T) {
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "mirror", memoryStore: store}
	if err := s.afterTurnMemory(1, []provider.ChatMessage{{Role: "user", Content: "remember this turn"}}, "finished answer"); err != nil {
		t.Fatal(err)
	}
	s.finishMemoryJobs()
	turns, err := store.TurnsForSession(context.Background(), s.sessionID, 0, 12)
	if err != nil || len(turns) != 1 || turns[0].AssistantText != "finished answer" {
		t.Fatalf("shutdown lost durable turn mirror: count=%d err=%v", len(turns), err)
	}
}

func TestMemoryMaintenanceRecoversSameSessionShortTurn(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"memories\":[]}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	store := newTestMemoryStore(t)
	cfg := modelAttributionConfig(server.URL, "test-model")
	cfg.Memory.Consolidation = "off"
	s := &Session{sessionID: "resumed", memoryStore: store, cfg: cfg}
	if err := store.RecordTurn(context.Background(), memory.TurnRecord{SessionID: s.sessionID, Turn: 1, UserText: "pending short turn", AssistantText: "completed answer"}); err != nil {
		t.Fatal(err)
	}
	s.runMemoryMaintenance(t.Context())
	if watermark := s.extractWatermark(t.Context(), store, s.sessionID); watermark != 1 {
		t.Fatalf("same-session maintenance skipped short turn: watermark=%d", watermark)
	}
	s.finishMemoryJobs()
	if requests.Load() != 1 {
		t.Fatalf("expected recovery request only, got %d requests", requests.Load())
	}
}

func TestMemoryShutdownCancelsProviderAndPreservesPendingTurns(t *testing.T) {
	started, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			cancelled <- struct{}{}
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "closing", memoryStore: store, cfg: modelAttributionConfig(server.URL, "test-model")}
	if err := store.RecordTurn(context.Background(), memory.TurnRecord{SessionID: s.sessionID, Turn: 3, UserText: "remember pending turn", AssistantText: "completed answer"}); err != nil {
		t.Fatal(err)
	}
	s.maybeExtractMemories(3)
	defer s.finishMemoryJobs()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("memory provider request did not start")
	}
	start := time.Now()
	s.finishMemoryJobs()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown waited for provider: %s", elapsed)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider request outlived memory shutdown")
	}
	if watermark := s.extractWatermark(context.Background(), store, s.sessionID); watermark != 0 {
		t.Fatalf("cancelled extraction advanced watermark to %d", watermark)
	}
	turns, err := store.TurnsForSession(context.Background(), s.sessionID, 0, 12)
	if err != nil || len(turns) != 1 {
		t.Fatalf("pending turns lost: count=%d err=%v", len(turns), err)
	}
	s.StartMemoryMaintenance()
	s.maybeExtractMemories(3)
	if s.memoryJobs.maintaining.Load() || s.memoryJobs.extracting.Load() {
		t.Fatal("closed session admitted more memory jobs")
	}
}
