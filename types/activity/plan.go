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
	Current                              []Step
	Next                                 *Step
}

type Step struct {
	Number               int
	Text, Status, Reason string
}
