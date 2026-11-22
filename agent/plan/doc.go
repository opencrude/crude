// Package plan owns the presentation-neutral planning domain for a session.
//
// A plan is a revisioned, declarative snapshot that models can create, edit,
// reorder, skip, reopen, pause, resume, and cancel in one atomic update. Both
// the main agent and sub-agents share the same Service implementation. The
// package must never import crude/tui; the TUI consumes a neutral PlanView.
package plan
