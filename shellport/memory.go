package shellport

import (
	"context"

	"crude/memory"
	"crude/workspace"
)

// MemoryStore is the memory browse and curation surface.
type MemoryStore interface {
	SearchMemoryEpisodes(ctx context.Context, query string, limit int) ([]memory.Episode, error)
	SearchMemories(ctx context.Context, query memory.ListQuery) ([]memory.ScoredRecord, error)
	InspectMemory(ctx context.Context, id string) (workspace.MemoryDetail, error)
	PinMemory(ctx context.Context, id string, expectedRevision int64, pinned bool) error
	ForgetMemoryRecord(ctx context.Context, id string, expectedRevision int64) error
	CorrectMemory(ctx context.Context, replacement memory.Record) (memory.Record, error)
}

// Memory is the workspace memory surface.
type Memory interface {
	MemoryStore
	// BindMemory returns a store pinned to the current workspace database so late work can never
	// touch a replacement workspace.
	BindMemory() MemoryStore
}
