package session

import (
	"context"
	"testing"
	"time"

	"crude/agent/memory"
	"crude/agent/provider"
	"crude/internal/providersim"
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
	server := providersim.New(t)
	server.Script(providersim.Text(`{"memories":[]}`).WithoutUsage())
	store := newTestMemoryStore(t)
	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "test-model")
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
	if got := len(server.Requests()); got != 1 {
		t.Fatalf("expected recovery request only, got %d requests", got)
	}
}

func TestMemoryShutdownCancelsProviderAndPreservesPendingTurns(t *testing.T) {
	gate := providersim.NewGate()
	server := providersim.New(t)
	server.Script(providersim.Text(`{"memories":[]}`).WithoutUsage().WithGate(gate))
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "closing", memoryStore: store, cfg: modelAttributionConfig(server.BaseURL(providersim.Chat), "test-model")}
	if err := store.RecordTurn(context.Background(), memory.TurnRecord{SessionID: s.sessionID, Turn: 3, UserText: "remember pending turn", AssistantText: "completed answer"}); err != nil {
		t.Fatal(err)
	}
	s.maybeExtractMemories(3)
	defer s.finishMemoryJobs()
	arrival, cancelArrival := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelArrival()
	if err := gate.Wait(arrival); err != nil {
		t.Fatal("memory provider request did not start")
	}
	start := time.Now()
	s.finishMemoryJobs()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown waited for provider: %s", elapsed)
	}
	cancelled, cancelWait := context.WithTimeout(t.Context(), time.Second)
	defer cancelWait()
	if err := gate.WaitCanceled(cancelled); err != nil {
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
