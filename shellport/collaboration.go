package shellport

import (
	"context"

	"crude/workspace"
)

// Collaboration is the agent board surface.
type Collaboration interface {
	BoardSnapshot(ctx context.Context, query, thread string, since, snapshot int64) (workspace.BoardSnapshot, error)
	MarkBoardRead(ctx context.Context, through int64) error
}
