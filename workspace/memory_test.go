package workspace

import (
	"context"
	"errors"
	"testing"

	"crude/memory"
)

func TestWorkspaceMemoryStoreAttached(t *testing.T) {
	store := openTestWorkspaceDB(t)
	if store.Memory == nil {
		t.Fatal("workspace DB did not attach a memory store")
	}
	ctx := context.Background()
	first, err := store.Memory.Remember(ctx, memory.Record{
		Kind: "convention", Title: "Validation command",
		Content: "Run go test ./... before handoff.", SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	duplicate, err := store.Memory.Remember(ctx, memory.Record{
		Kind: "convention", Title: "Validation command",
		Content: "Run go test -race ./... before handoff.", SourceSessionID: "session-2",
	})
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}
	if duplicate.ID == first.ID {
		t.Fatal("same-title memory replaced a distinct assertion")
	}
	replacement := first
	replacement.Content = "Run go test -race ./... before handoff."
	updated, err := store.Memory.Update(ctx, first.ID, replacement, memory.ActorUser, "session-2")
	if err != nil || updated.ID != first.ID || updated.Revision != first.Revision+1 {
		t.Fatalf("correction: %#v %v", updated, err)
	}
	results, err := store.Memory.Recall(ctx, memory.RecallQuery{Text: "validation race"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if err := store.Memory.Supersede(ctx, first.ID, updated.Revision, "", memory.ActorTool, "session-2"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	results, err = store.Memory.Recall(ctx, memory.RecallQuery{Text: "validation"})
	if err != nil {
		t.Fatalf("Recall after forget: %v", err)
	}
	if len(results) != 1 || results[0].ID != duplicate.ID {
		t.Fatalf("retirement touched another assertion: %+v", results)
	}
}

func TestWorkspaceMemoryBrowserServiceCASAndAudit(t *testing.T) {
	store := openTestWorkspaceDB(t)
	ws := &Workspace{WorkspaceDB: store}
	ctx := context.Background()
	first, err := store.Memory.Remember(ctx, memory.Record{Kind: memory.KindFact, Title: "Browser service", Content: "Initial content", AppliesWhen: "In this workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.PinMemory(ctx, first.ID, first.Revision, true); err != nil {
		t.Fatal(err)
	}
	if err := ws.ForgetMemoryRecord(ctx, first.ID, first.Revision); !errors.Is(err, memory.ErrRevisionConflict) {
		t.Fatalf("retire should reject stale preview: %v", err)
	}
	first.Content = "Stale correction"
	if _, err := ws.CorrectMemory(ctx, first); !errors.Is(err, memory.ErrRevisionConflict) {
		t.Fatalf("stale correction should fail: %v", err)
	}
	detail, err := ws.InspectMemory(ctx, first.ID)
	if err != nil || detail.Record.Revision != 2 || !detail.Record.Pinned || len(detail.Revisions) != 2 || detail.Revisions[0].Content != "Initial content" {
		t.Fatalf("audit: %#v %v", detail, err)
	}
	if err := ws.ForgetMemoryRecord(ctx, first.ID, detail.Record.Revision); err != nil {
		t.Fatal(err)
	}
	records, err := ws.SearchMemories(ctx, memory.ListQuery{})
	if err != nil || len(records) != 0 {
		t.Fatalf("retired record recalled: %#v %v", records, err)
	}
	detail, err = ws.InspectMemory(ctx, first.ID)
	if err != nil || detail.Record.Status != memory.StatusSuperseded || len(detail.Revisions) != 3 {
		t.Fatalf("retired audit: %#v %v", detail, err)
	}
}

func TestWorkspaceMemoryBrowserServiceErrorsAndCancellation(t *testing.T) {
	var unavailable *Workspace
	if _, err := unavailable.SearchMemories(context.Background(), memory.ListQuery{}); !errors.Is(err, ErrMemoryUnavailable) {
		t.Fatalf("unavailable search: %v", err)
	}
	if err := unavailable.PinMemory(context.Background(), "missing", 1, true); !errors.Is(err, ErrMemoryUnavailable) {
		t.Fatalf("unavailable mutation: %v", err)
	}
	ws := &Workspace{WorkspaceDB: openTestWorkspaceDB(t)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ws.SearchMemories(ctx, memory.ListQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("search lost cancellation: %v", err)
	}
	if _, err := ws.InspectMemory(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("detail lost cancellation: %v", err)
	}
	if err := ws.WorkspaceDB.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if records, err := ws.SearchMemories(context.Background(), memory.ListQuery{}); err == nil || len(records) != 0 {
		t.Fatalf("closed DB became empty success: %#v %v", records, err)
	}
}
