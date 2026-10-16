// Package activity defines immutable activity projections. It has no storage,
// provider, actor, or rendering dependencies.
package activity

// Plan is published only after a committed domain update or off-UI resume.
// Consumers borrow its slices read-only; they never execute or advance steps.
type Plan struct {
	ID                                   string
	Revision                             uint64
	Objective, Status                    string
	Completed, Total, Skipped, Cancelled int
	Steps                                []Step // complete ordered snapshot for read-only plan inspection
	Current                              []Step
	Next                                 *Step
}

// SameRevision reports whether two snapshots describe the same committed plan
// revision. Connected shells receive a fresh copy on every read, so pointer
// identity is never plan identity.
func SameRevision(a, b *Plan) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Revision == b.Revision && a.Status == b.Status
}

type Step struct {
	Number               int
	Text, Status, Reason string
}
