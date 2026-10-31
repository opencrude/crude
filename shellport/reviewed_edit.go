package shellport

import (
	"context"

	"crude/types"
	"crude/workspace"
)

// ReviewedEdits is an exact connected-document capability. The server binds the
// negotiated owner; the shell supplies only its acknowledged source at preview
// and an opaque ID/digest thereafter. All calls run off Update.
type ReviewedEdits interface {
	PrepareLSPRename(context.Context, types.InsightSource, int, int, string) (workspace.ReviewedEditPreview, error)
	ApplyReviewedEdit(context.Context, string, string) (workspace.ReviewedEditResult, error)
	ReleaseReviewedEdit(context.Context, string, string) error
	InspectReviewedEdit(context.Context, string, string) (workspace.ReviewedEditResult, error)
}
