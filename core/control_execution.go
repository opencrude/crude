package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/types/control"
	"crude/worktree"
)

// Identity comes from the admitted gateway caller, never an input owner.
func (a *actor) prepareControlExecution(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	if owner.ClientID == "" || owner.Epoch == 0 {
		return controlFailure(in, control.Invalid, "trusted caller owner is required")
	}
	if a.ws == nil || a.ws.Executions == nil {
		return controlFailure(in, control.Unavailable, "managed execution authority unavailable")
	}
	if in.CommandID == "execution.selection" || in.CommandID == "execution.select" {
		return a.prepareControlExecutionSelection(in)
	}
	service := a.ws.Executions
	retention := a.executionRetentionCheck()
	var id string
	var plan worktree.PlanDelta
	var cleanup worktree.CleanupOptions
	var settle struct {
		ID         string `json:"id"`
		SessionID  string `json:"session_id"`
		AgentID    string `json:"agent_id"`
		RecoveryID string `json:"recovery_id"`
		Generation uint64 `json:"recovery_generation"`
	}
	keep := true
	sourceReview := false
	var create struct {
		Mode           worktree.Mode `json:"mode"`
		BaseCommit     string        `json:"base_commit,omitempty"`
		Independent    bool          `json:"independent,omitempty"`
		IncludeIgnored []string      `json:"include_ignored,omitempty"`
	}
	switch in.CommandID {
	case "execution.list", "execution.integrations":
		if err := controlDecode(in.Input, &struct{}{}); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
	case "execution.create":
		if err := controlDecode(in.Input, &create); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		if create.Mode != worktree.Auto && create.Mode != worktree.Shared && create.Mode != worktree.Isolated {
			return controlFailure(in, control.Invalid, "mode must be auto, shared or worktree")
		}
	case "execution.apply":
		var input struct {
			Plan worktree.PlanDelta `json:"plan"`
		}
		if err := controlDecode(in.Input, &input); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		plan, id = input.Plan, input.Plan.ContextID
		if plan.Digest == "" || plan.SourceDigest == "" || len(plan.Changes) != 0 || plan.DiscardDigest != "" || len(plan.IgnoredPaths) != 0 || plan.HeadCommit != "" {
			return controlFailure(in, control.Invalid, "compact integration review token required")
		}
	case "execution.undo", "execution.recover":
		var input struct {
			OperationID string `json:"operation_id"`
		}
		if err := controlDecode(in.Input, &input); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		id = input.OperationID
	case "execution.inspect", "execution.reconcile":
		var input struct {
			ID string `json:"id"`
		}
		if err := controlDecode(in.Input, &input); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		id = input.ID
	case "execution.settle":
		if err := controlDecode(in.Input, &settle); err != nil || settle.SessionID == "" || settle.AgentID == "" || settle.RecoveryID == "" || settle.Generation == 0 {
			return controlFailure(in, control.Invalid, "id, session_id, agent_id and exact recovery incident/generation required")
		}
		id = settle.ID
	case "execution.keep":
		var input struct {
			ID   string `json:"id"`
			Keep *bool  `json:"keep"`
		}
		if err := controlDecode(in.Input, &input); err != nil || input.Keep == nil {
			return controlFailure(in, control.Invalid, "id and keep boolean are required")
		}
		id, keep = input.ID, *input.Keep
	case "execution.review", "execution.cleanup":
		var input struct {
			ID      string `json:"id"`
			Discard bool   `json:"discard,omitempty"`
			Digest  string `json:"digest,omitempty"`
		}
		if err := controlDecode(in.Input, &input); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		id, sourceReview = input.ID, input.Discard
		cleanup = worktree.CleanupOptions{Discard: input.Discard, Digest: input.Digest}
	default:
		return nil, nil, false
	}
	settlementOwner := a.ws.BackendSession(settle.SessionID)
	discovery := in.CommandID == "execution.list" || in.CommandID == "execution.integrations" || in.CommandID == "execution.create"
	if discovery && in.Target.ResourceID != "" || !discovery && (id == "" || in.Target.ResourceID != id) {
		return controlFailure(in, control.Invalid, "exact resource target must match input identity")
	}
	if in.Preconditions.Revision != nil || in.Preconditions.OpenEpoch != nil {
		return controlFailure(in, control.Invalid, "execution uses immutable plan digests, not revision/epoch preconditions")
	}
	trusted := worktree.Owner{AgentID: "client:" + owner.ClientID, RunID: fmt.Sprintf("epoch:%d", owner.Epoch)}
	agent := in.Caller.Kind == control.AgentCaller
	if agent {
		trusted = worktree.Owner{AgentID: in.Caller.AgentID, RunID: in.Caller.RunID}
	}
	if !discovery {
		var actual worktree.Owner
		if in.CommandID == "execution.undo" || in.CommandID == "execution.recover" {
			status, err := service.Integration(id)
			if err != nil {
				return controlFailure(in, control.Unavailable, err.Error())
			}
			actual = status.Plan.Owner
		} else {
			c, err := service.Inspect(id)
			if err != nil {
				return controlFailure(in, control.Unavailable, err.Error())
			}
			if c.LogicalRoot != a.ws.RootPath {
				return controlFailure(in, control.Denied, "execution belongs to another logical workspace")
			}
			actual = c.Owner
		}
		if err := a.controlExecutionOwner(context.Background(), in, actual); err != nil {
			return controlFailure(in, control.Denied, err.Error())
		}
		if in.CommandID == "execution.apply" && plan.Owner != actual {
			return controlFailure(in, control.Invalid, "plan owner differs from authoritative execution owner")
		}
		trusted = actual // external callers manage the inspected owner under gateway approval.
	}
	var once sync.Once
	var result control.Result
	return func(ctx context.Context) control.Result {
		once.Do(func() {
			result = control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed}
			if id != "" {
				result.ResourceIDs = []string{id}
			}
			if ctx.Err() != nil {
				result.State = control.Failed
				result.Fault = &control.Fault{Code: control.Unavailable, Message: ctx.Err().Error()}
				return
			}
			if !discovery {
				if err := a.controlExecutionOwner(ctx, in, trusted); err != nil {
					result.State, result.Fault = control.Failed, &control.Fault{Code: control.Denied, Message: err.Error()}
					return
				}
			}
			var output any
			var err error
			switch in.CommandID {
			case "execution.list":
				entries := service.List()
				active := []worktree.Context{}
				for _, entry := range entries {
					if !entry.Removed {
						active = append(active, entry)
					}
				}
				output = active
			case "execution.integrations":
				output, err = service.Integrations()
			case "execution.inspect":
				var c worktree.Context
				c, err = service.Inspect(id)
				c.Baseline, c.ArchivedResult, c.DocumentRevisions = nil, nil, nil
				output = c
			case "execution.reconcile":
				output, err = reconcileExecution(ctx, service.Service, id, retention)
			case "execution.settle":
				if settlementOwner == nil {
					err = fmt.Errorf("owner session %s is not open; use session.open then execution.settle with the same exact incident", settle.SessionID)
					break
				}
				err = settlementOwner.CancelExecutionRecovery(id, settle.AgentID, settle.RecoveryID, settle.Generation)
				if err == nil {
					output = map[string]string{"id": id, "agent_id": settle.AgentID, "recovery_id": settle.RecoveryID, "outcome": "cancelled parked task; review retained work before cleanup"}
				}
			case "execution.create":
				var c worktree.Context
				c, err = service.Create(ctx, worktree.Request{Owner: trusted, Mode: create.Mode, BaseCommit: create.BaseCommit, Independent: create.Independent, IncludeIgnored: create.IncludeIgnored})
				// Allocation is not execution. An external connection has no
				// running task whose lifetime could release Create's initial pin.
				if err == nil && !agent && c.ID != "" {
					err = service.Unpin(c.ID, trusted)
					if err == nil {
						c, err = service.Inspect(c.ID)
					}
				}
				output = c
				if c.ID != "" {
					result.ResourceIDs = []string{c.ID}
				}
			case "execution.review":
				if sourceReview {
					output, err = service.ReviewDiscard(ctx, id)
				} else {
					var reviewed worktree.PlanDelta
					reviewed, err = service.Review(ctx, id)
					output = executionReviewToken(reviewed)
				}
			case "execution.apply":
				// An exact committed owner receipt outranks current destination
				// state. A lost reply must not re-review/replay the original effect.
				statuses, readErr := service.Integrations()
				if readErr != nil {
					err = readErr
					break
				}
				for _, status := range statuses {
					if !reflect.DeepEqual(executionReviewToken(status.Plan), executionReviewToken(plan)) {
						continue
					}
					if status.State == "committed" {
						output = status.Receipt
					} else {
						err = fmt.Errorf("integration %s needs explicit recovery (%s)", status.Receipt.OperationID, status.State)
					}
					break
				}
				if output != nil || err != nil {
					break
				}
				var current worktree.PlanDelta
				current, err = service.Review(ctx, id)
				if err != nil || !reflect.DeepEqual(executionReviewToken(current), executionReviewToken(plan)) {
					result.State = control.Failed
					result.Fault = &control.Fault{Code: control.Stale, Message: "reviewed source/destination changed; review again"}
					return
				}
				output, err = service.Apply(ctx, current)
			case "execution.keep":
				err = service.Keep(id, trusted, keep)
				output = map[string]string{"id": id}
			case "execution.cleanup":
				var c worktree.Context
				c, err = service.Inspect(id)
				if err != nil {
					break
				}
				cleanup.Check = func(ctx context.Context) error { return retention(ctx, trusted, id, c.Root) }
				err = service.Cleanup(ctx, id, trusted, cleanup)
				output = map[string]string{"id": id}
			case "execution.undo":
				output, err = service.Undo(ctx, id)
			case "execution.recover":
				output, err = service.RecoverIntegration(ctx, id)
			}
			if receipt, ok := output.(worktree.Receipt); ok && receipt.OperationID != "" {
				result.ResourceIDs = append(result.ResourceIDs, receipt.OperationID)
			}
			result.Output, _ = json.Marshal(output)
			if err != nil {
				result.State = control.Failed
				code := control.OperationFailed
				if in.CommandID == "execution.apply" || in.CommandID == "execution.undo" || in.CommandID == "execution.recover" || in.CommandID == "execution.create" || in.CommandID == "execution.cleanup" {
					result.State, code = control.Uncertain, control.UnknownOutcome
				}
				result.Fault = &control.Fault{Code: code, Message: err.Error()}
			}
		})
		return result.Clone()
	}, nil, true
}

// Temporary bounded integration bootstrap using the existing PlanDelta schema.
// Full plans stay with the document/Local History owner; replace this projection
// with the complete bounded/paged review contract, not a full-plan fallback.
func executionReviewToken(p worktree.PlanDelta) worktree.PlanDelta {
	return worktree.PlanDelta{ContextID: p.ContextID, Owner: p.Owner, Digest: p.Digest, SourceDigest: p.SourceDigest, Changes: []worktree.Change{}}
}

// The artifact owner never changes during integration. Authority to manage a
// descendant is derived from the durable parent chain, not inventory labels or
// a caller-supplied owner. The gateway separately fences the caller's live run.
func (a *actor) controlExecutionOwner(ctx context.Context, in control.Request, actual worktree.Owner) error {
	mutation := in.CommandID == "execution.apply" || in.CommandID == "execution.undo" || in.CommandID == "execution.recover" || in.CommandID == "execution.keep" || in.CommandID == "execution.cleanup" || in.CommandID == "execution.settle"
	if in.Caller.Kind != control.AgentCaller && (!mutation || strings.HasPrefix(actual.AgentID, "client:")) {
		return nil
	}
	store := a.ws.Collaboration
	if store == nil {
		return errors.New("execution ancestry authority unavailable")
	}
	// Janitorial inspection and cleanup are workspace collaboration, not an
	// ancestor privilege. Exact settled runs, pins and recovery still govern effects.
	if in.CommandID == "execution.inspect" || in.CommandID == "execution.review" || in.CommandID == "execution.cleanup" || in.CommandID == "execution.reconcile" || in.CommandID == "execution.keep" || in.CommandID == "execution.apply" || in.CommandID == "execution.settle" {
		if in.Caller.Kind == control.AgentCaller {
			if _, err := store.Agent(ctx, in.Caller.AgentID); err != nil {
				return errors.New("workspace janitor identity unavailable")
			}
		}
		if strings.HasPrefix(actual.AgentID, "client:") {
			return nil
		}
		run, err := store.Run(ctx, actual.RunID)
		if err != nil || run.AgentID != actual.AgentID {
			return errors.New("execution owner run unavailable")
		}
		if mutation && !run.State.Terminal() {
			return errors.New("execution owner run is still active")
		}
		return nil
	}
	run, err := store.Run(ctx, actual.RunID)
	if err != nil || run.AgentID != actual.AgentID || mutation && !run.State.Terminal() {
		return errors.New("execution requires an exact settled owner run before mutation")
	}
	if in.Caller.Kind != control.AgentCaller {
		return nil // Explicit external grant, but never an active child's effect.
	}
	caller, err := store.Agent(ctx, in.Caller.AgentID)
	if err != nil {
		return errors.New("execution caller ancestry unavailable")
	}
	seen := map[string]bool{}
	for id := actual.AgentID; id != "" && !seen[id]; {
		seen[id] = true
		participant, err := store.Agent(ctx, id)
		if err != nil {
			break
		}
		if participant.ID == caller.ID {
			if actual.AgentID == caller.ID && actual.RunID != in.Caller.RunID {
				return errors.New("execution belongs to another exact caller run")
			}
			return nil
		}
		id = participant.ParentID
	}
	return errors.New("execution does not belong to this exact agent/run or its descendants")
}

func (a *actor) prepareControlExecutionSelection(in control.Request) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	var input struct {
		ID               *string `json:"id"`
		ExpectedRevision *uint64 `json:"expected_revision"`
	}
	if in.CommandID == "execution.selection" {
		if err := controlDecode(in.Input, &struct{}{}); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
	} else if err := controlDecode(in.Input, &input); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	id := ""
	if input.ID != nil {
		id = *input.ID
	}
	if in.Target.SessionID == "" || in.Target.ResourceID != id || in.Preconditions.OpenEpoch != nil {
		return controlFailure(in, control.Invalid, "exact session target and matching selection id are required")
	}
	if in.CommandID == "execution.select" && (input.ExpectedRevision == nil || input.ID == nil) {
		return controlFailure(in, control.Invalid, "id and expected_revision are required")
	}
	if in.Preconditions.Revision != nil && (input.ExpectedRevision == nil || *input.ExpectedRevision != *in.Preconditions.Revision) {
		return controlFailure(in, control.Stale, "revision precondition differs from expected_revision")
	}
	backend := a.ws.BackendSession(in.Target.SessionID)
	if backend == nil {
		return controlFailure(in, control.Unavailable, "exact session is not open")
	}
	return func(ctx context.Context) control.Result {
		selection := backend.ExecutionSelection()
		var err error
		if in.CommandID == "execution.select" {
			selection, err = backend.SelectExecution(ctx, id, *input.ExpectedRevision)
		}
		result := control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed}
		if err != nil {
			code := control.OperationFailed
			if errors.Is(err, collaboration.ErrStale) {
				code = control.Stale
			}
			result.State, result.Fault = control.Failed, &control.Fault{Code: code, Message: err.Error()}
			return result
		}
		result.Output, _ = json.Marshal(selection)
		result.Revision = &selection.Revision
		if selection.ID != "" {
			result.ResourceIDs = []string{selection.ID}
		}
		return result
	}, nil, true
}
