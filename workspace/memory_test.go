package workspace

import (
	"context"
	"testing"

	"crude/session"
)

func TestWorkspaceMemoryRememberRecallAndForget(t *testing.T) {
	store := openTestWorkspaceDB(t)
	ctx := context.Background()
	first, err := store.RememberMemory(ctx, session.MemoryRecord{
		Kind: "convention", Title: "Validation command",
		Content: "Run go test ./... before handoff.", SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("RememberMemory: %v", err)
	}
	updated, err := store.RememberMemory(ctx, session.MemoryRecord{
		Kind: "convention", Title: "Validation command",
		Content: "Run go test -race ./... before handoff.", SourceSessionID: "session-2",
	})
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}
	if updated.ID != first.ID {
		t.Fatalf("stable memory ID changed: %q != %q", updated.ID, first.ID)
	}
	results, err := store.RecallMemories(ctx, "validation race", 8)
	if err != nil {
		t.Fatalf("RecallMemories: %v", err)
	}
	if len(results) != 1 || results[0].Content != "Run go test -race ./... before handoff." {
		t.Fatalf("results = %+v", results)
	}
	if err := store.ForgetMemory(ctx, first.ID); err != nil {
		t.Fatalf("ForgetMemory: %v", err)
	}
	results, err = store.RecallMemories(ctx, "", 8)
	if err != nil {
		t.Fatalf("RecallMemories after forget: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("forgotten memory returned: %+v", results)
	}
}
