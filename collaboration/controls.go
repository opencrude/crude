package collaboration

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

type ControlRequest struct {
	AgentID         string
	RuntimeEpoch    uint64
	RunID           string
	ExpectedVersion uint64
	Operation       string
}

// ApplyControl commits only the exact observed identity. The runtime then
// cancels that same execution, not whichever turn happens to be selected later.
func (store *Store) ApplyControl(ctx context.Context, request ControlRequest) (*Run, error) {
	var result *Run
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		agent, err := loadAgent(ctx, tx, request.AgentID)
		if err != nil {
			return err
		}
		if request.RuntimeEpoch == 0 || agent.RuntimeEpoch != request.RuntimeEpoch ||
			agent.Presence != RuntimeAttached {
			return ErrStale
		}
		now := time.Now().UTC().UnixNano()
		switch request.Operation {
		case "cancel", "pause":
			if request.RunID == "" || request.ExpectedVersion == 0 {
				return fmt.Errorf("%w: exact run_id and expected_version required", ErrInvalid)
			}
			run, err := loadRun(ctx, tx, request.RunID)
			if err != nil {
				return err
			}
			if agent.CurrentRunID != run.ID || run.AgentID != agent.ID ||
				run.RuntimeEpoch != request.RuntimeEpoch || run.Version != request.ExpectedVersion || run.State.Terminal() {
				return ErrStale
			}
			run.State = RunStopping
			if request.Operation == "pause" {
				run.State = RunPausing
				if _, err := tx.ExecContext(ctx, `UPDATE collaboration_agents
					SET state='suspended',updated_at=? WHERE id=?`, now, agent.ID); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_runs
				SET state=?,version=version+1,updated_at=? WHERE id=?`, run.State, now, run.ID); err != nil {
				return err
			}
			run.Version++
			run.UpdatedAt = timeFromUnixNano(now)
			result = &run
		case "resume", "reopen":
			if request.RunID != "" || agent.CurrentRunID != "" {
				return fmt.Errorf("%w: availability control requires no active run", ErrConflict)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_agents
				SET state='available',updated_at=? WHERE id=?`, now, agent.ID); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: invalid control operation", ErrInvalid)
		}
		return nil
	})
	if err == nil {
		store.signal()
	}
	return result, err
}

func ControlIdentity(event Event) (operation string, version, epoch uint64, err error) {
	for _, reference := range event.References {
		switch reference.Kind {
		case "control":
			if operation != "" {
				return "", 0, 0, fmt.Errorf("%w: ambiguous control", ErrInvalid)
			}
			operation = reference.ID
			if reference.Revision > 0 {
				version = uint64(reference.Revision)
			}
		case "runtime_epoch":
			if epoch != 0 {
				return "", 0, 0, fmt.Errorf("%w: ambiguous runtime epoch", ErrInvalid)
			}
			epoch, err = strconv.ParseUint(reference.ID, 10, 64)
			if err != nil || epoch == 0 {
				return "", 0, 0, fmt.Errorf("%w: invalid runtime epoch", ErrInvalid)
			}
		}
	}
	switch operation {
	case "cancel", "pause":
		if event.RunID == "" || version == 0 {
			return "", 0, 0, fmt.Errorf("%w: exact run control required", ErrInvalid)
		}
	case "resume", "reopen":
		if event.RunID != "" || epoch == 0 {
			return "", 0, 0, fmt.Errorf("%w: exact availability epoch required", ErrInvalid)
		}
	default:
		return "", 0, 0, fmt.Errorf("%w: invalid control", ErrInvalid)
	}
	return operation, version, epoch, nil
}
