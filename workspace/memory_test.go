package workspace

import (
	"context"
	"testing"

	"crude/memory"
)

// TestWorkspaceMemoryStoreAttached verifies the workspace DB carries the
// durable memory store end to end: schema migrated, FTS active, and legacy
// v0 semantics (stable ID dedup by kind+title, forget) preserved through
// the crude/memory implementation.
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
	updated, err := store.Memory.Remember(ctx, memory.Record{
		Kind: "convention", Title: "Validation command",
		Content: "Run go test -race ./... before handoff.", SourceSessionID: "session-2",
	})
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}
	if updated.ID != first.ID {
		t.Fatalf("stable memory ID changed: %q != %q", updated.ID, first.ID)
	}
	results, err := store.Memory.Recall(ctx, memory.RecallQuery{Text: "validation race"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 || results[0].Content != "Run go test -race ./... before handoff." {
		t.Fatalf("results = %+v", results)
	}
	if err := store.Memory.Supersede(ctx, first.ID, "", memory.ActorTool, "session-2"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	results, err = store.Memory.Recall(ctx, memory.RecallQuery{Text: "validation"})
	if err != nil {
		t.Fatalf("Recall after forget: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("forgotten memory returned: %+v", results)
	}
}
