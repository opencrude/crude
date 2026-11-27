package session

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
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

// Maintenance used to archive every unpinned record beyond 200 active ones, the
// lowest importance first, and every automatic record unused for 45 days. Both
// hid stated user preferences from recall: 90 records in one workspace were
// archived for the count alone, among them how the user wants edits made.
// Knowledge now leaves recall only by forget, merge or supersession.
func TestMemoryMaintenanceNeverArchivesKnowledgeForCountOrAge(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "workspace.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := memory.Attach(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	evidence := memory.Evidence{SessionID: "earlier", MessageID: "human-1", Kind: memory.EvidenceUser, Summary: "Make code edits with inline Python.", Outcome: memory.OutcomeUnmeasured}
	if err := store.RecordTurn(ctx, memory.TurnRecord{SessionID: "earlier", Turn: 1, UserText: evidence.Summary, Observations: []memory.Observation{{Evidence: evidence, Turn: 1}}}); err != nil {
		t.Fatal(err)
	}
	const count = 230
	for i := range count {
		if _, err := store.Remember(ctx, memory.Record{Kind: memory.KindPreference, Title: fmt.Sprintf("Preference %d", i),
			Content: fmt.Sprintf("Stated preference number %d.", i), Source: memory.SourceAuto, Importance: 10 + i%50,
			Evidence: []memory.Evidence{evidence}}); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-120 * 24 * time.Hour).UnixNano()
	if _, err := db.ExecContext(ctx, `UPDATE workspace_memories SET created_at=?, updated_at=?`, old, old); err != nil {
		t.Fatal(err)
	}
	// The model consolidation pass ran recently, so only local maintenance runs.
	if err := store.SetMeta(ctx, "consolidate_last", fmt.Sprintf("%d", time.Now().UnixNano())); err != nil {
		t.Fatal(err)
	}
	s := &Session{sessionID: "maintainer", memoryStore: store, cfg: &Config{}}
	s.cfg.Memory.AutoExtract = "off"
	s.runMemoryMaintenance(ctx)
	s.finishMemoryJobs()
	if active, err := store.CountByStatus(ctx, memory.StatusActive); err != nil || active != count {
		t.Fatalf("maintenance archived knowledge: %d of %d active, err=%v", active, count, err)
	}
	recalled, err := store.Recall(ctx, memory.RecallQuery{Text: "preference number 7", Limit: 3})
	if err != nil || len(recalled) == 0 {
		t.Fatalf("old knowledge no longer recalls: %v %v", recalled, err)
	}
}
