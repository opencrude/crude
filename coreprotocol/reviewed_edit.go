package coreprotocol

import (
	"crude/types"
	"crude/workspace"
)

const (
	LSPPrepareRename    = "lsp.rename_preview"
	ReviewedEditApply   = "lsp.edit_apply"
	ReviewedEditRelease = "lsp.edit_release"
	ReviewedEditInspect = "lsp.edit_receipt"
)

type RenamePreviewRequest struct {
	Source    types.InsightSource
	Line, Col int
	NewName   string
}

// No caller-supplied paths, edits, preimages, provider or attribution at Apply.
type ReviewedEditRequest struct {
	ID, Digest string
}

type ReviewedEditReply struct {
	Preview workspace.ReviewedEditPreview
	Result  workspace.ReviewedEditResult
}
