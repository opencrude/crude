package core

import (
	"context"
	"crude/internal/fileuri"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"crude/agent/session"
	"crude/agent/worktree"
	"crude/types"
	"crude/workspace"
)

// preparePlanInvestigationCleanup captures the exact owners on the actor. The
// worker only acts on closed cycles and does not turn a successful approval
// into an error when a checkout still needs attention.
func (a *actor) preparePlanInvestigationCleanup(agentID string) func(context.Context) {
	if a.ws == nil || a.ws.Executions == nil {
		return nil
	}
	frontend := a.ws.SessionForAgent(agentID)
	if frontend == nil {
		return nil
	}
	sessionID := frontend.SessionID
	backend := a.ws.BackendSession(sessionID)
	if backend == nil || !a.ws.Executions.HasPlanInvestigations(sessionID) {
		return nil
	}
	service, check, status := a.ws.Executions, a.executionRetentionCheck(), a.ws.Status
	return func(ctx context.Context) {
		ids, err := backend.ClosedPlanInvestigationIDs(ctx)
		if err != nil {
			return // Closing or unavailable ownership is never permission to remove.
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			err := discardClosedPlanInvestigation(ctx, service, check, sessionID, id)
			if err != nil && ctx.Err() == nil && status != nil {
				status.Notify(types.NotificationRecord{
					Severity: types.SeverityWarning,
					Source:   "Plan mode",
					DedupKey: "plan-investigation-retained:" + id,
					Summary:  "Plan investigation retained",
					Detail:   fmt.Sprintf("Checkout %s was not removed. %s Review it for discard before cleanup.", id, err),
				})
			}
		}
	}
}

// Stopped runs and commands can still retain the checkout. These are normal
// deferred states, not an unsafe discard. Once review begins, later changes or
// evidence faults require an explicit new review, not a new automatic digest.
func discardClosedPlanInvestigation(ctx context.Context, service *workspace.ExecutionService, check func(context.Context, worktree.Owner, string, string) error, sessionID, id string) error {
	c, err := service.InspectSummary(id)
	if err != nil {
		return err
	}
	if c.Removed || c.Kept || c.InvestigationDiscardBlocked {
		return nil
	}
	if c.Investigation == nil || c.Investigation.SessionID != sessionID {
		return errors.New("the checkout does not belong to this planning session")
	}
	retained := func(ctx context.Context) error { return check(ctx, c.Owner, c.ID, c.Root) }
	if err := retained(ctx); err != nil {
		return nil
	}
	if len(c.Pins) != 0 {
		result, err := reconcileExecution(ctx, service.Service, id, check)
		if err != nil || len(result.Retained) != 0 {
			return err
		}
	}
	digest, err := service.PrepareInvestigationDiscard(ctx, id)
	if err == nil && digest != "" {
		deferred := false
		err = service.Cleanup(ctx, id, c.Owner, worktree.CleanupOptions{
			Discard: true, Digest: digest, InvestigationAutomatic: true,
			Check: func(ctx context.Context) error {
				err := retained(ctx)
				deferred = err != nil
				return err
			},
		})
		if deferred {
			return nil
		}
	}
	if err == nil || ctx.Err() != nil {
		return err
	}
	// A racing successful cleanup or intentional Keep is not an uncertain
	// disposition. Otherwise freeze this automatic decision for explicit review.
	if latest, inspectErr := service.InspectSummary(id); inspectErr == nil && (latest.Removed || latest.Kept || latest.InvestigationDiscardBlocked) {
		return nil
	}
	return errors.Join(err, service.BlockInvestigationDiscard(id))
}

// Capture session handles on the actor; the worker never reads workspace maps.
func (a *actor) executionRetentionCheck() func(context.Context, worktree.Owner, string, string) error {
	store, directory := a.ws.Collaboration, a.ws.WorkspaceDir
	live := map[string]*session.Session{}
	for _, frontend := range a.ws.Sessions {
		if backend := a.ws.BackendSession(frontend.SessionID); backend != nil {
			live[frontend.SessionID] = backend
		}
	}
	terminals := a.ws.TermMgr
	return func(ctx context.Context, owner worktree.Owner, id, root string) error {
		if terminals != nil {
			for _, terminal := range terminals.List() {
				rel, err := filepath.Rel(root, terminal.CWD)
				if terminal.Alive && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return fmt.Errorf("live terminal %s uses the checkout", terminal.ID)
				}
			}
		}
		if !strings.HasPrefix(owner.AgentID, "client:") {
			if store == nil {
				return errors.New("execution run authority unavailable")
			}
			run, err := store.Run(ctx, owner.RunID)
			if err != nil || run.AgentID != owner.AgentID || !run.State.Terminal() {
				return errors.New("execution run is not proven settled")
			}
		}
		for _, backend := range live {
			if err := backend.ExecutionRetention(id, root); err != nil {
				return err
			}
		}
		paths, err := executionSessionDatabases(filepath.Join(directory, "sessions"))
		if err != nil {
			return err
		}
		for _, path := range paths {
			db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=ro"))
			if err != nil {
				return err
			}
			err = session.ExecutionDatabaseRetention(db, id)
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				return fmt.Errorf("retained session %s: %w", filepath.Base(path), errors.Join(err, closeErr))
			}
		}
		return nil
	}
}

// Only the session database layout is enumerated, not transcript/log/temp trees.
// Cold consumers matter just as much as the execution creator's own session.
func executionSessionDatabases(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".db") {
			paths = append(paths, path)
		}
		if !entry.IsDir() {
			continue
		}
		children, err := os.ReadDir(filepath.Join(path, "subagents"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.Type().IsRegular() && strings.HasSuffix(child.Name(), ".db") {
				paths = append(paths, filepath.Join(path, "subagents", child.Name()))
			}
		}
	}
	return paths, nil
}

type executionReconciliation struct {
	ID       string                 `json:"id"`
	Released []worktree.Owner       `json:"released"`
	Retained []executionRetainedPin `json:"retained"`
}

type executionRetainedPin struct {
	Owner  worktree.Owner `json:"owner"`
	Reason string         `json:"reason"`
}

func reconcileExecution(ctx context.Context, service *worktree.Service, id string, check func(context.Context, worktree.Owner, string, string) error) (executionReconciliation, error) {
	c, err := service.InspectSummary(id)
	result := executionReconciliation{ID: id, Released: []worktree.Owner{}, Retained: []executionRetainedPin{}}
	if err != nil {
		return result, err
	}
	for _, pin := range c.Pins {
		if err := check(ctx, pin, id, c.Root); err != nil {
			result.Retained = append(result.Retained, executionRetainedPin{pin, err.Error()})
			continue
		}
		if err := service.Unpin(id, pin); err != nil {
			// Another exact reconciliation may have already released this pin.
			latest, inspectErr := service.InspectSummary(id)
			if inspectErr != nil || slices.Contains(latest.Pins, pin) {
				return result, err
			}
		}
		result.Released = append(result.Released, pin)
	}
	return result, nil
}
