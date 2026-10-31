package coreprotocol

import "crude/types"

const (
	LSPInlayHintsMethod   = "lsp.inlay_hints"
	LSPInlayInspectMethod = "lsp.inlay_inspect"
	LSPInlayReleaseMethod = "lsp.inlay_release"
)

type InlayHintsRequest struct {
	Source   types.InsightSource
	ViewID   string
	Sequence uint64
	Range    types.LSPInlayRange
}

// A selected row can reveal only retained original JSON and resolved tooltips.
// No caller-supplied provider object, edit, command or location is accepted.
type InlayInspectRequest struct {
	ID, Digest string
	Index      int
	Resolve    bool
}

type InlayReleaseRequest struct {
	ViewID   string
	Sequence uint64
}

type InlayReply struct {
	Set        types.LSPInlaySet
	Inspection types.LSPInlayInspection
}
