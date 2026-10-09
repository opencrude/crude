package shellport

import (
	"context"

	"crude/document"
	"crude/workspace"
)

// LiveDocumentCommands preserves save/format request identities across lost
// responses. Receipt observation never executes an earlier command again.
type LiveDocumentCommands interface {
	SaveDocumentRequest(context.Context, string, uint64, workspace.DocumentSaveReason, string) (document.Snapshot, error)
	FormatDocumentRequest(context.Context, string, uint64, string) (document.Snapshot, error)
	DocumentSnapshotReceipt(context.Context, string) (document.Snapshot, bool, error)
}
