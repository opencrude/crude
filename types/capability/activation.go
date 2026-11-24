package capability

import (
	"context"

	"crude/types/command"
)

// ActivationWork is one admitted operation, not a reusable permission.
type ActivationWork func(context.Context) (Result, error)

// ActivationRunner schedules work for its initiating agent. A Settings action
// without an agent executes the same work under its own request lifetime.
type ActivationRunner func(context.Context, string, ActivationWork) (Result, error)

type runnerKey struct{}
type trackedKey struct{}
type progressKey struct{}

// WithActivationRunner binds the existing job owner to this invocation only.
func WithActivationRunner(ctx context.Context, run ActivationRunner) context.Context {
	return context.WithValue(ctx, runnerKey{}, run)
}

// RunActivation schedules only after exact admission. Nested acquisition and
// loading are phases of the same job; they cannot schedule another lifetime.
func RunActivation(ctx context.Context, label string, work ActivationWork) (Result, error) {
	if run, _ := ctx.Value(runnerKey{}).(ActivationRunner); run != nil {
		return run(context.WithValue(WithActivationRunner(ctx, nil), trackedKey{}, true), label, work)
	}
	return work(ctx)
}

// WithProgress binds a bounded phase observer, never approval or credentials.
func WithProgress(ctx context.Context, report func(command.Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

// ReportProgress publishes code-authored phases, not arbitrary server output.
func ReportProgress(ctx context.Context, phase string, current, total int64) {
	if report := ProgressObserver(ctx); report != nil {
		report(command.Progress{Phase: phase, Current: current, Total: total})
	}
}

// ProgressObserver returns nil for calls with no interested job.
func ProgressObserver(ctx context.Context) func(command.Progress) {
	report, _ := ctx.Value(progressKey{}).(func(command.Progress))
	return report
}

// TrackedActivation distinguishes a cancellable admitted job from ordinary
// protocol reads, independently of whether its progress is being displayed.
func TrackedActivation(ctx context.Context) bool {
	tracked, _ := ctx.Value(trackedKey{}).(bool)
	return tracked
}
