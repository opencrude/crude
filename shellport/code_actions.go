package shellport

import (
	"context"

	"crude/types"
	"crude/workspace"
)

// CodeActions selects only retained canonical evidence. Applying its reviewed
// result uses ReviewedEdits; this interface adds no second mutation path.
type CodeActions interface {
	RequestLSPCodeActions(context.Context, types.InsightSource, int, int) (workspace.CodeActionSet, error)
	PrepareLSPCodeAction(context.Context, string, string, int) (workspace.ReviewedEditPreview, error)
	ReleaseLSPCodeActions(context.Context, string, string) error
}
