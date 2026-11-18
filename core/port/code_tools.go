package shellport

import (
	"context"

	"crude/types"
)

// CodeTools is the optional, blocking code-intelligence tool inventory service.
// Call it from a worker, never from the UI update or paint path.
type CodeTools interface {
	ReadCodeToolInventory(context.Context, int, int) (types.CodeToolInventory, error)
	PreviewCodeToolRemoval(context.Context, []string, bool) (types.CodeToolRemovalPreview, error)
	RemoveCodeTools(context.Context, types.CodeToolRemovalPreview) error
}
