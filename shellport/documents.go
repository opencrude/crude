package shellport

import (
	"context"
	"time"

	"crude/workspace"
)

// DocumentService is the editor autosave scheduler and disk-commit authority.
type DocumentService interface {
	AcceptDue(msg workspace.DocumentAutosaveDueMsg) bool
	Cancel(bufferID string)
	Save(ctx context.Context, request workspace.DocumentSaveRequest) workspace.DocumentSaveResult
	Schedule(bufferID, path string, revision uint64, delay time.Duration)
}
