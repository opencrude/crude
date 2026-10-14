package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"crude/collaboration"
	"crude/tools"
)

type collaborationWaitObservation struct {
	Status     string                  `json:"status"`
	ElapsedMs  int64                   `json:"elapsed_ms"`
	Unmet      []string                `json:"unmet_conditions,omitempty"`
	Reason     string                  `json:"reason,omitempty"`
	Guidance   string                  `json:"guidance,omitempty"`
	Agent      *collaboration.Agent    `json:"agent,omitempty"`
	Run        *collaboration.Run      `json:"run,omitempty"`
	Artifact   *collaboration.Artifact `json:"artifact,omitempty"`
	Event      *collaboration.Event    `json:"event,omitempty"`
	Recovery   *agentRecoveryView      `json:"recovery,omitempty"`
	Snapshot   int64                   `json:"snapshot"`
	ObservedAt time.Time               `json:"observed_at"`
	Source     string                  `json:"source"`
}

var errCollaborationWaitElapsed = errors.New("collaboration wait duration elapsed")

func (s *Session) waitForCollaboration(ctx context.Context, store *collaboration.Store, params map[string]any) (any, error) {
	agentID := getStrParam(params, "agent_id")
	runID := getStrParam(params, "run_id")
	artifactID := getStrParam(params, "artifact_id")
	afterRevision := getIntParam(params, "after_revision")
	if agentID == "" && runID == "" && artifactID == "" {
		return nil, fmt.Errorf("agent_wait requires agent_id, run_id, or artifact_id")
	}
	timeout := getIntParam(params, "timeout_seconds")
	if timeout <= 0 {
		timeout = 60
	}
	started := time.Now()
	waitCtx, stopWait := context.WithDeadlineCause(ctx, started.Add(time.Duration(timeout)*time.Second), errCollaborationWaitElapsed)
	defer stopWait()
	identity, _ := ctx.Value(tools.ExecutionIdentityKey{}).(tools.ExecutionIdentity)
	if err := ctx.Err(); err != nil {
		return collaborationWaitObservation{Status: "caller_interrupted", Reason: context.Cause(ctx).Error()}, nil
	}
	notifications, unsubscribe := store.Subscribe()
	defer unsubscribe()
	var snapshot int64
	if identity.AgentID != "" {
		fence, err := store.Page(waitCtx, collaboration.PageQuery{RecipientID: identity.AgentID, Limit: 1})
		if err != nil {
			if ctx.Err() != nil {
				return collaborationWaitObservation{Status: "caller_interrupted", Reason: context.Cause(ctx).Error(), ElapsedMs: time.Since(started).Milliseconds(), ObservedAt: time.Now(), Source: "wait_control"}, nil
			}
			if errors.Is(context.Cause(waitCtx), errCollaborationWaitElapsed) {
				return collaborationWaitObservation{Status: "timeout_no_match", Reason: "the wait duration elapsed before the initial collaboration snapshot could be read", Guidance: "No fresh snapshot was available; retry only if another observation is still required.", ElapsedMs: time.Since(started).Milliseconds(), ObservedAt: time.Now(), Source: "wait_control"}, nil
			}
			return nil, err
		}
		snapshot = fence.LatestSequence
	}
	observe := func(readCtx context.Context) (collaborationWaitObservation, bool, error) {
		result := collaborationWaitObservation{Status: "ready", ElapsedMs: time.Since(started).Milliseconds(), Snapshot: snapshot, ObservedAt: time.Now(), Source: "collaboration_store"}
		if agentID != "" {
			value, err := store.Agent(readCtx, agentID)
			if err != nil {
				return result, false, err
			}
			result.Agent = &value
			if runID == "" && artifactID == "" && value.CurrentRunID != "" {
				result.Unmet = append(result.Unmet, "agent_terminal_or_unavailable")
			}
		}
		if runID != "" {
			value, err := store.Run(readCtx, runID)
			if err != nil {
				return result, false, err
			}
			if agentID != "" && value.AgentID != agentID {
				return result, false, fmt.Errorf("agent_wait run %q belongs to agent %q, not %q", value.ID, value.AgentID, agentID)
			}
			if result.Agent != nil && value.RuntimeEpoch != result.Agent.RuntimeEpoch {
				return result, false, fmt.Errorf("agent_wait run %q belongs to stale runtime epoch %d; agent %q is at epoch %d", value.ID, value.RuntimeEpoch, result.Agent.ID, result.Agent.RuntimeEpoch)
			}
			result.Run = &value
			if !value.State.Terminal() {
				result.Unmet = append(result.Unmet, "run_terminal")
			}
		}
		if artifactID != "" {
			value, err := store.Artifact(readCtx, artifactID, 0)
			if errors.Is(err, collaboration.ErrNotFound) {
				result.Unmet = append(result.Unmet, "artifact_revision")
				err = nil
			} else if err == nil {
				if agentID != "" && value.AuthorID != agentID {
					return result, false, fmt.Errorf("agent_wait artifact %q belongs to author %q, not %q", value.ID, value.AuthorID, agentID)
				}
				if runID != "" && value.RunID != runID {
					return result, false, fmt.Errorf("agent_wait artifact %q belongs to run %q, not %q", value.ID, value.RunID, runID)
				}
				result.Artifact = &value
				if value.Revision <= afterRevision {
					result.Unmet = append(result.Unmet, "artifact_revision")
				}
			}
			if err != nil {
				return result, false, err
			}
		}
		return result, len(result.Unmet) == 0, nil
	}
	observeDirected := func(readCtx context.Context, result *collaborationWaitObservation) error {
		if identity.AgentID == "" {
			return nil
		}
		for {
			page, err := store.Page(readCtx, collaboration.PageQuery{Since: snapshot, RecipientID: identity.AgentID, Limit: collaboration.MaxPageLimit})
			if err != nil {
				return err
			}
			for i := range page.Events {
				event := page.Events[i]
				status := ""
				targetAgentID := agentID
				if targetAgentID == "" && result.Run != nil {
					targetAgentID = result.Run.AgentID
				}
				switch {
				case event.Origin == collaboration.OriginHuman, event.Kind == collaboration.EventLifecycleControl:
					status = "directed_event"
				case event.SenderID == targetAgentID && hasCollaborationReference(event.References, "recovery_incident"):
					status = "needs_attention"
				}
				if status != "" {
					result.Status, result.Event, result.Snapshot = status, &event, event.Sequence
					result.Reason = "a new addressed event requires normal runtime handling"
					result.Guidance = "Inspect the exact event and lifecycle state before continuing, repairing, retrying, or stopping."
					snapshot = page.Snapshot
					return nil
				}
			}
			if !page.HasMore {
				snapshot, result.Snapshot = page.Snapshot, page.Snapshot
				return nil
			}
			snapshot = page.Cursor
		}
	}
	observeAll := func(readCtx context.Context) (collaborationWaitObservation, bool, error) {
		result, ready, err := observe(readCtx)
		if err != nil {
			return result, false, err
		}
		// A stopped agent cannot make further progress on its own: end the wait
		// with its exact incident instead of reporting a plain timeout/offline.
		targetAgentID := agentID
		if targetAgentID == "" && result.Run != nil {
			targetAgentID = result.Run.AgentID
		}
		if recovery := s.agentRecovery(targetAgentID); recovery != nil {
			result.Recovery = recovery
			if !ready {
				result.Status = "needs_attention"
				result.Reason = "the agent stopped and needs attention: " + recovery.Summary
				result.Guidance = "Address the cause, then agent_control operation continue (or cancel) with the returned recovery_id and recovery_generation."
				return result, true, nil
			}
		}
		if result.Agent != nil {
			switch {
			case result.Agent.State == collaboration.AgentClosed:
				result.Status, result.Reason = "closed", "the exact agent is closed"
			case result.Agent.State == collaboration.AgentSuspended:
				result.Status, result.Reason = "suspended", "the exact agent is suspended"
			case result.Agent.Presence == collaboration.RuntimeOffline:
				result.Status, result.Reason = "offline", "the exact agent runtime is offline"
			}
			if result.Status != "ready" && (!ready || runID == "" && artifactID == "") {
				result.Guidance = "Inspect or explicitly resume/reopen the exact agent before waiting again."
				return result, true, nil
			}
		}
		if ready {
			return result, true, nil
		}
		if err := observeDirected(readCtx, &result); err != nil {
			return result, false, err
		}
		if result.Event != nil {
			return result, true, nil
		}
		if result.Run != nil && result.Run.State.Terminal() {
			result.Status = "terminal_without_match"
			result.Reason = "the exact run is terminal but another requested condition remains unmet"
			result.Guidance = "Inspect the terminal outcome and unmet conditions; report publication is separate from run completion."
			return result, true, nil
		}
		return result, false, nil
	}
	var last collaborationWaitObservation
	for {
		result, ready, err := observeAll(waitCtx)
		if err != nil {
			if ctx.Err() != nil {
				result.Status, result.Reason = "caller_interrupted", context.Cause(ctx).Error()
				return result, nil
			}
			if errors.Is(context.Cause(waitCtx), errCollaborationWaitElapsed) {
				return collaborationWaitTimeoutSnapshot(ctx, observeAll, last, started), nil
			}
			return nil, err
		}
		last = result
		if ready {
			return result, nil
		}
		select {
		case <-ctx.Done():
			result.Status, result.Reason = "caller_interrupted", context.Cause(ctx).Error()
			return result, nil
		case <-waitCtx.Done():
			return collaborationWaitTimeoutSnapshot(ctx, observeAll, last, started), nil
		case _, open := <-notifications:
			if !open {
				result.Status = "store_closed"
				result.Reason = "collaboration observation closed before the requested conditions matched"
				return result, nil
			}
		}
	}
}

func collaborationWaitTimeoutSnapshot(ctx context.Context, observe func(context.Context) (collaborationWaitObservation, bool, error), last collaborationWaitObservation, started time.Time) collaborationWaitObservation {
	finalCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if final, ready, err := observe(finalCtx); err == nil {
		if ready {
			return final
		}
		last = final
	} else if ctx.Err() != nil {
		last.Status, last.Reason = "caller_interrupted", context.Cause(ctx).Error()
		last.ElapsedMs = time.Since(started).Milliseconds()
		return last
	} else {
		last.Guidance = "The returned facts are the last completed collaboration-store observation; the final refresh did not complete within its bounded grace period."
	}
	if last.ObservedAt.IsZero() {
		last.ObservedAt = time.Now()
		last.Source = "wait_control"
		last.Guidance = "No collaboration-store observation completed within the wait budget; no stored facts are being presented as fresh."
	}
	last.Status = "timeout_no_match"
	last.Reason = "the wait duration elapsed without all exact conditions becoming true"
	if last.Guidance == "" {
		last.Guidance = "Use the returned exact snapshot to decide whether to wait again or take another action."
	}
	last.ElapsedMs = time.Since(started).Milliseconds()
	return last
}

func hasCollaborationReference(references []collaboration.Reference, kind string) bool {
	for _, reference := range references {
		if reference.Kind == kind {
			return true
		}
	}
	return false
}
