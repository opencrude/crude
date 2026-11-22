package shellport

import (
	"context"

	"crude/types/insight"
)

// CodeTools is the optional, blocking code-intelligence tool inventory service.
// Call it from a worker, never from the UI update or paint path.
type CodeTools interface {
	ReadCodeToolInventory(context.Context, int, int) (insight.CodeToolInventory, error)
	PreviewCodeToolRemoval(context.Context, []string, bool) (insight.CodeToolRemovalPreview, error)
	RemoveCodeTools(context.Context, insight.CodeToolRemovalPreview) error
}
