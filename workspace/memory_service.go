package workspace

import (
	"context"
	"time"

	"crude/memory"
)

const memoryServiceTimeout = 5 * time.Second

// MemoryAvailable reports whether the durable memory store is open.
func (w *Workspace) MemoryAvailable() bool {
	return w != nil && w.WorkspaceDB != nil && w.WorkspaceDB.Memory != nil
}

// SearchMemories serves the memory browser: empty query lists recent
// records; otherwise a scored FTS search (archived included so the browser
// shows the full auditable picture).
func (w *Workspace) SearchMemories(query string, limit int) []memory.ScoredRecord {
	if !w.MemoryAvailable() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryServiceTimeout)
	defer cancel()
	records, err := w.WorkspaceDB.Memory.List(ctx, memory.ListQuery{Text: query, Limit: limit})
	if err != nil {
		w.Logger.Warn("MEMORY", "browser_search_failed", "error", err.Error())
		return nil
	}
	return records
}

// PinMemory toggles a memory's pinned flag from the browser.
func (w *Workspace) PinMemory(id string, pinned bool) error {
	if !w.MemoryAvailable() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryServiceTimeout)
	defer cancel()
	return w.WorkspaceDB.Memory.SetPinned(ctx, id, pinned, memory.ActorUser)
}

// ForgetMemoryRecord supersedes a memory from the browser (audit-preserving
// forget).
func (w *Workspace) ForgetMemoryRecord(id string) error {
	if !w.MemoryAvailable() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryServiceTimeout)
	defer cancel()
	return w.WorkspaceDB.Memory.Supersede(ctx, id, "", memory.ActorUser, "")
}
