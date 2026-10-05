package session

import (
	"context"
	"errors"

	"crude/collaboration"
)

// A lifecycle control may commit between the execution owner's version snapshot
// and finalization. Retry only that exact owned run, never another generation.
func finishCollaborationRun(store *collaboration.Store, request collaboration.FinishRunRequest) (collaboration.Run, error) {
	for attempt := 0; attempt < 3; attempt++ {
		finished, err := store.FinishRun(context.Background(), request)
		if !errors.Is(err, collaboration.ErrStale) {
			return finished, err
		}
		current, readErr := store.Run(context.Background(), request.RunID)
		if readErr != nil {
			return collaboration.Run{}, readErr
		}
		if current.AgentID != request.AgentID || current.RuntimeEpoch != request.RuntimeEpoch || current.State.Terminal() {
			return collaboration.Run{}, err
		}
		request.ExpectedVersion = current.Version
	}
	return collaboration.Run{}, collaboration.ErrStale
}
