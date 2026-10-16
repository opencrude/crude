package chatprojection

import (
	"testing"

	"crude/types"
	"crude/types/activity"
)

// A connected shell's per-frame plan copy is the same source; a new revision
// is not. Treating every copy as new rebuilt the strip on every frame.
func TestActivitySourceComparesPlansByRevision(t *testing.T) {
	plan := &activity.Plan{ID: "p", Revision: 3, Status: "active", Steps: []activity.Step{{Number: 1, Text: "a", Status: "pending"}}}
	copied := *plan
	a, b := types.AgentActivityView{Plan: plan}, types.AgentActivityView{Plan: &copied}
	if !sameActivitySource(a, b) {
		t.Fatal("an equal-revision copy was treated as a new source")
	}
	copied.Revision = 4
	if sameActivitySource(a, b) {
		t.Fatal("a newer plan revision was treated as unchanged")
	}
}
