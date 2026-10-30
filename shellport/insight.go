package shellport

import "crude/types"

// DocumentInsightMsg is a complete immutable derived publication, independent
// of the text topic. Valid fences close/reopen, attachment and workspace epochs.
// Done acknowledges shell installation, not semantic correctness.
type DocumentInsightMsg struct {
	Owner Workspace
	View  types.DocumentInsight
	Valid func() bool
	Done  func(error)
}
