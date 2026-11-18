package shellport

import (
	"context"
	"errors"

	"crude/types/control"
	"crude/types/handover"
)

// ErrCoreRestartPending means the exact reservation is healthy but its natural
// checkpoint or requested earliest start has not arrived. All other Ready
// errors are failures, not reasons to silently keep retrying.
var ErrCoreRestartPending = errors.New("core restart is waiting for its checkpoint or earliest start")

// CoreRestartPort is the exact attached shell's typed session handover route.
// Reservation admission is rechecked against the pending semantic control;
// these methods are not a generic goal-resume or lifecycle administration API.
type CoreRestartPort interface {
	ReserveCoreRestart(context.Context, string, string, handover.Reservation) (handover.State, error)
	InspectCoreRestart(context.Context, string, string) (handover.State, error)
	CancelCoreRestart(context.Context, string, string, string) (handover.State, error)
	ContinueCoreRestart(context.Context, string, string, string) (handover.State, error)
}

// CoreRestartHost belongs to the local application in the original terminal.
// It stages an exact image and owns a private, durable intent, never workspace
// persistence or a background command escaped from the submitting session.
// Every method which performs I/O runs off Update.
type CoreRestartHost interface {
	Schedule(context.Context, control.Request) (handover.State, error)
	Inspect(context.Context, string) (handover.State, error)
	Cancel(context.Context, string) (handover.State, error)
	Ready(context.Context, string) error
	Continue(context.Context) (handover.State, error)
	StartupOperationID() string
	StartupSessionID() string
}

type CoreRestartInput struct {
	Executable   string `json:"executable"`
	SHA256       string `json:"sha256"`
	GoalID       string `json:"goal_id"`
	DelaySeconds int    `json:"delay_seconds"`
}
