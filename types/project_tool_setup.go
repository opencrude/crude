package types

// ProjectToolSetupReview is a native, owner-bound project setup preview. ID is
// an expiring one-shot reference retained by the setup owner, not a grant.
type ProjectToolSetupReview struct {
	ID            string
	Review        ProjectToolReview
	NeedsDecision bool
	// Quiet marks later needs after a saved project decision. Present these as
	// a notice; do not automatically open another question.
	Quiet   bool
	Message string
}

// ProjectToolSetupItem reports each selected tool, including tools not attempted
// after a cancellation or failure. It does not claim semantic feature coverage.
type ProjectToolSetupItem struct {
	ItemID, ToolID, Message string
	Attempted, Installed    bool
	// Evaluation describes protocol health only, never semantic feature coverage.
	Evaluation, EvaluationMessage string
}

// ProjectToolSetupResult preserves committed decisions and individual outcomes.
// A successful download is distinct from a running or evaluated language server.
type ProjectToolSetupResult struct {
	DecisionID string
	Items      []ProjectToolSetupItem
	Message    string
}
