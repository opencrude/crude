package coreprotocol

import "crude/types"

const (
	ProjectToolsReview = "code_tools.project_review"
	ProjectToolsDecide = "code_tools.project_decide"
)

type ProjectToolsReviewRequest struct {
	// Explicit requests a fresh human review of still-missing tools, including
	// earlier refusals or failed attempts. It never authorizes installation.
	Explicit bool
}

type ProjectToolsReviewResult struct {
	Preview types.ProjectToolSetupReview
}

// ProjectToolsDecideRequest selects only items from the original, retained
// native-shell preview. It never supplies a replacement source or approval.
type ProjectToolsDecideRequest struct {
	PreviewID   string
	SelectedIDs []string
}

type ProjectToolsDecideResult struct {
	Result types.ProjectToolSetupResult
}
