package coreprotocol

import (
	"crude/types"
	"crude/workspace"
)

const (
	LSPCodeActions        = "lsp.code_actions"
	LSPPrepareCodeAction  = "lsp.action_preview"
	LSPReleaseCodeActions = "lsp.actions_release"
)

// Requests carry acknowledged canonical source and source-byte selection, never
// caller-authored raw actions, diagnostics, provider identities or file edits.
type CodeActionsRequest struct {
	Source     types.InsightSource
	Start, End int
}

type CodeActionRequest struct {
	ID, Digest string
	Index      int
}

type CodeActionsReply struct {
	Set     workspace.CodeActionSet
	Preview workspace.ReviewedEditPreview
}
