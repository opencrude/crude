package workspace

import (
	"fmt"
	"time"

	"crude/tools"
	ttypes "crude/types"
	"crude/types/recovery"
)

// ApplyAgentStatusEvent reduces an accepted agent event into workspace status.
// Call after applying the event to the owning agent state; ingress owns identity
// and stale-upload rejection. Like AgentManager, it requires serialized admission.
func (w *Workspace) ApplyAgentStatusEvent(evt AgentEvent) {
	if w == nil || w.Status == nil || w.AgentMgr == nil || evt.AgentID == "" {
		return
	}
	center := w.Status
	node := w.AgentMgr.Get(evt.AgentID)
	if node == nil {
		return
	}
	previous, _ := center.Agent(evt.AgentID)
	snapshot := previous
	snapshot.Ref = ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: evt.AgentID}
	if session := w.SessionForAgent(evt.AgentID); session != nil {
		snapshot.Ref.SessionID = session.SessionID
	}
	snapshot.Label = node.Label
	if snapshot.Label == "" {
		snapshot.Label = "Main Agent"
	}
	snapshot.ModelID = node.Model
	if node.Parent != nil {
		snapshot.Ref.ParentID = node.Parent.ID
	}
	if node.State != nil {
		snapshot.TurnGeneration = node.State.TurnIdentity.Generation
		snapshot.ReadOnly = node.State.Completed
		if node.State.IsActive() && node.State.TurnIdentity.ModelID != "" {
			identity := node.State.TurnIdentity
			snapshot.ModelID, snapshot.ProviderID, snapshot.AccountID = identity.ModelID, identity.ProviderID, identity.AccountID
		}
	}
	snapshot.UpdatedAt = time.Now()
	// Background checkpoint preparation outlives runs and never changes the
	// agent's lifecycle phase.
	if evt.Type == "context_precompaction" {
		id := agentEventString(evt.Data, "request_id")
		phase := agentEventString(evt.Data, "phase")
		if id == "" {
			return
		}
		if phase != "started" && (id != snapshot.CheckpointRequestID || snapshot.Checkpoint == "") {
			return
		}
		switch phase {
		case "started":
			if id == snapshot.CheckpointRequestID {
				return
			}
			snapshot.CheckpointRequestID = id
			snapshot.Checkpoint = "preparing"
			snapshot.CheckpointDetail = "Preparing a context summary in the background; agent work is not blocked."
		case "progress":
			if snapshot.Checkpoint != "preparing" {
				return
			}
			if stage := agentEventString(evt.Data, "stage_label"); stage != "" {
				snapshot.CheckpointDetail = "Preparing a context summary in the background: " + stage + ". Agent work is not blocked."
			}
		case "ready":
			snapshot.Checkpoint = "ready"
			snapshot.CheckpointDetail = "Context summary ready; it will be used when compaction is needed. Agent work is not blocked."
		case "failed", "cancelled", "discarded":
			snapshot.Checkpoint = ""
			snapshot.CheckpointDetail = ""
		default:
			return
		}
		center.SetAgent(snapshot)
		return
	}
	// Retained semantic evidence is not a new lifecycle transition. Inactive
	// AgentState accepts late tokens/tool results for history, but it must never
	// reactivate status before a later inventory repaint repairs it.
	if state := node.State; state != nil && evt.Type != "done" && evt.Type != "error" &&
		evt.Type != "sub_agent_completed" && evt.Type != "safety_alert" && evt.Type != "recovery" &&
		(state.Cancelling || state.terminal || state.Completed) &&
		(!state.terminal || state.Cancelling || state.Completed || !isManualCompactionEvent(evt)) {
		if state.Cancelling {
			snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentCancelling, "Stopping", false
			center.SetAgent(snapshot)
		}
		return
	}

	switch evt.Type {
	case "sub_agent_started":
		snapshot.Phase, snapshot.Detail = ttypes.AgentOpening, "Opening"
		snapshot.StartedAt = snapshot.UpdatedAt
	case "sub_agent_completed":
		// A child owns its preparation slot only for this assignment. Unlike a
		// root between human turns, it cannot adopt a retained summary later.
		snapshot.Checkpoint, snapshot.CheckpointDetail = "", ""
		result := "completed"
		if values, ok := evt.Data.(map[string]string); ok {
			result = values["status"]
		}
		snapshot.ReadOnly, snapshot.NeedsAction = true, false
		snapshot.Phase, snapshot.Detail = agentTerminalPhase(result)
	case "connection_changed":
		if identity, ok := evt.Data.(ttypes.TurnIdentity); ok && identity.Generation == snapshot.TurnGeneration {
			snapshot.ProviderID, snapshot.ModelID = identity.ProviderID, identity.ModelID
			snapshot.AccountID = identity.AccountID
		}
	case "turn_started":
		snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentPreparing, "Preparing context", false
		snapshot.RequestID = ""
		snapshot.StartedAt = snapshot.UpdatedAt
		snapshot.UploadedBytes, snapshot.TotalBytes, snapshot.ProgressKnown = 0, 0, false
		if identity, ok := evt.Data.(ttypes.TurnIdentity); ok {
			snapshot.TurnGeneration = identity.Generation
			snapshot.ProviderID, snapshot.ModelID = identity.ProviderID, identity.ModelID
			snapshot.AccountID = identity.AccountID
		}
	case "phase":
		phase, ok := ParseAgentPhase(evt.Data)
		if !ok {
			return
		}
		if data, ok := evt.Data.(map[string]any); ok {
			if requestID, _ := data["request_id"].(string); requestID != "" && requestID != snapshot.RequestID {
				snapshot.UploadedBytes, snapshot.TotalBytes, snapshot.ProgressKnown = 0, 0, false
				snapshot.RequestID = requestID
			}
		}
		snapshot.ProviderID, snapshot.Progress = phase.Provider, phase.Progress
		if phase.TotalBytes > 0 {
			snapshot.UploadedBytes, snapshot.TotalBytes = phase.UploadedBytes, phase.TotalBytes
			snapshot.ProgressKnown = true
		}
		if phase.Phase == "waiting_provider" {
			snapshot.Phase, snapshot.Detail = ttypes.AgentWaiting, "Waiting for provider"
			if phase.Provider != "" {
				snapshot.Detail = "Waiting for " + phase.Provider
			}
		} else if phase.Progress > 0 || phase.UploadedBytes > 0 {
			snapshot.Phase, snapshot.Detail = ttypes.AgentUploading, "Uploading context"
		} else {
			snapshot.Phase, snapshot.Detail = ttypes.AgentPreparing, "Preparing context"
		}
		center.SetOperation(agentRequestOperation(snapshot))
	case "thinking":
		snapshot.Phase, snapshot.Detail = ttypes.AgentThinking, "Reasoning"
	case "stream_token":
		snapshot.Phase, snapshot.Detail = ttypes.AgentResponding, "Receiving response"
	case "tool_call_streaming", "tool_call", "tool_update", "tool_result":
		snapshot.Phase, snapshot.Detail = ttypes.AgentTool, AgentToolDetail(evt.Data)
	case "blocking", "confirmation":
		operation := ""
		switch data := evt.Data.(type) {
		case map[string]any:
			operation, _ = data["operation"].(string)
		case map[string]string:
			operation = data["operation"]
		}
		if operation == "question" || operation == "questions" {
			snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentQuestion, "Needs answer", true
		} else if !w.YoloForAgent(evt.AgentID) {
			snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentApproval, "Needs approval", true
		} else {
			snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentTool, "Working", false
		}
	case "blocking_auto", "permission_resolved", "blocking_complete":
		snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentTool, "Working", false
	case "question":
		snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentQuestion, "Needs answer", true
	case "safety_alert":
		detail := "Command safety warning"
		severity := ttypes.SeverityWarning
		if values, ok := evt.Data.(map[string]any); ok {
			if reason, _ := values["reason"].(string); reason != "" {
				detail = reason
			}
			if risk, _ := values["risk"].(string); risk == "blocked" {
				severity = ttypes.SeverityCritical
			}
		}
		// This event records a command assessment, not a pending decision or
		// an agent phase. Permission prompts own NeedsAction; Notices retains
		// the warning without replacing subsequent work or terminal status.
		center.Notify(ttypes.NotificationRecord{DedupKey: "safety:" + evt.AgentID, Owner: snapshot.Ref, Severity: severity, Source: "safety", Summary: detail, Detail: detail})
		return
	case "retry":
		snapshot.Phase, snapshot.Detail = ttypes.AgentRetrying, "Retrying provider request"
		if values, ok := evt.Data.(map[string]any); ok {
			backoff, _ := values["backoff"].(float64)
			snapshot.RetryAt = time.Now().Add(time.Duration(backoff * float64(time.Second)))
			snapshot.RequestID, _ = values["request_id"].(string)
		}
	case "recovery":
		status, ok := evt.Data.(recovery.Status)
		if !ok || status.ID == "" || node.State == nil || node.State.RecoveryStatus() != status {
			return
		}
		snapshot.Detail, snapshot.RetryAt = status.Summary, status.RetryAt
		snapshot.ReadOnly = false
		snapshot.NeedsAction = status.State == recovery.NeedsAttention || status.State == recovery.Interrupted
		switch status.State {
		case recovery.Recovering:
			if status.RetryAt.IsZero() {
				snapshot.Phase, snapshot.Detail = ttypes.AgentPreparing, "Resuming run"
			} else {
				snapshot.Phase = ttypes.AgentRecovering
			}
			center.ResolveIssue("agent:" + evt.AgentID)
		case recovery.NeedsAttention:
			snapshot.Phase = ttypes.AgentNeedsAttention
		case recovery.Interrupted:
			snapshot.Phase = ttypes.AgentInterrupted
		case recovery.Recovered:
			snapshot.Phase = ttypes.AgentPreparing
			center.ResolveIssue("agent:" + evt.AgentID)
		case recovery.Cancelled:
			snapshot.Phase = ttypes.AgentCancelled
			center.ResolveIssue("agent:" + evt.AgentID)
		}
		if snapshot.NeedsAction {
			actions := []ttypes.StatusAction{
				{ID: "agent.retry", Label: "Continue run", Enabled: true},
				{ID: "status.switch", Label: "Inspect agent", Enabled: true},
			}
			center.SetIssue(ttypes.IssueSnapshot{
				ID: "agent:" + evt.AgentID, Owner: snapshot.Ref, Severity: ttypes.SeverityWarning,
				Summary: snapshot.Label + " needs attention", Detail: status.Detail, Actions: actions,
			})
			center.Notify(ttypes.NotificationRecord{
				DedupKey: "agent-recovery:" + status.ID, Owner: snapshot.Ref,
				Severity: ttypes.SeverityWarning, Source: "recovery",
				Summary: snapshot.Label + " needs attention", Detail: status.Detail + "\n" + status.Action,
				Actions: actions,
			})
		}
	case "context_compaction_started", "context_compaction_stage", "context_compaction_retrying", "context_compaction_committing":
		snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentCompacting, "Compacting context", false
		if values, ok := evt.Data.(map[string]any); ok {
			if id, _ := values["request_id"].(string); id != "" && id != snapshot.RequestID {
				snapshot.StartedAt = snapshot.UpdatedAt
				snapshot.RequestID = id
			}
			if label, _ := values["stage_label"].(string); label != "" {
				snapshot.Detail = "Compacting context · " + label
			}
		}
	case "context_compacted", "context_compaction_cancelled", "context_compaction_failed":
		snapshot.NeedsAction = false
		if evt.Type == "context_compacted" {
			snapshot.Checkpoint = ""
			snapshot.CheckpointDetail = ""
		}
		if values, ok := evt.Data.(map[string]any); ok {
			if id, _ := values["request_id"].(string); id != "" {
				snapshot.RequestID = id
			}
		}
		// Automatic compaction resumes the same turn; the next request may
		// update its phase. Manual compaction ends as a separate operation.
		if node.State != nil && node.State.IsActive() {
			snapshot.Phase, snapshot.Detail = ttypes.AgentPreparing, "Preparing context"
		} else {
			snapshot.Phase = ttypes.AgentCompactionResult
			switch evt.Type {
			case "context_compacted":
				snapshot.Detail = "Context compacted"
			case "context_compaction_cancelled":
				snapshot.Detail = "Compaction cancelled"
			case "context_compaction_failed":
				snapshot.Detail = "Compaction failed"
			}
		}
	case "done":
		reason := "completed"
		if values, ok := evt.Data.(map[string]any); ok {
			reason, _ = values["reason"].(string)
		}
		if reason == "cancelled" {
			snapshot.Phase, snapshot.Detail = ttypes.AgentCancelled, "Cancelled"
		} else {
			snapshot.Phase, snapshot.Detail = ttypes.AgentCompleted, "Completed"
		}
		snapshot.NeedsAction = false
		center.FinishOperations(evt.AgentID, ttypes.OperationSucceeded, "")
	case "error":
		detail := fmt.Sprint(evt.Data)
		snapshot.Phase, snapshot.Detail, snapshot.NeedsAction = ttypes.AgentFailed, detail, false
		center.FinishOperations(evt.AgentID, ttypes.OperationFailed, detail)
		center.SetIssue(ttypes.IssueSnapshot{
			ID: "agent:" + evt.AgentID, Owner: snapshot.Ref, Severity: ttypes.SeverityError,
			Summary: snapshot.Label + " failed", Detail: detail,
		})
		center.Notify(ttypes.NotificationRecord{
			DedupKey: "agent-failure:" + evt.AgentID, Owner: snapshot.Ref,
			Severity: ttypes.SeverityError, Source: "agent", Summary: snapshot.Label + " failed", Detail: detail,
			Actions: []ttypes.StatusAction{{ID: "status.switch", Label: "Inspect agent", Enabled: true}},
		})
	}
	center.SetAgent(snapshot)
}

func agentTerminalPhase(status string) (ttypes.AgentPhase, string) {
	switch status {
	case "cancelled":
		return ttypes.AgentCancelled, "Cancelled"
	case "failed", "error", "crashed", "failed_error", "failed_timeout", "failed_quota", "failed_policy":
		return ttypes.AgentFailed, "Failed"
	default:
		return ttypes.AgentCompleted, "Completed"
	}
}

func agentRequestOperation(snapshot ttypes.AgentStatusSnapshot) ttypes.OperationSnapshot {
	id := snapshot.RequestID
	if id == "" {
		id = fmt.Sprintf("agent-request:%s:%d", snapshot.Ref.ID, snapshot.TurnGeneration)
	}
	state := ttypes.OperationRunning
	if snapshot.Phase == ttypes.AgentWaiting {
		state = ttypes.OperationWaiting
	}
	return ttypes.OperationSnapshot{
		ID: id, Owner: snapshot.Ref, Kind: "provider-request", Label: snapshot.Detail,
		State: state, Progress: snapshot.UploadedBytes, Total: snapshot.TotalBytes, Unit: "bytes",
		StartedAt: snapshot.StartedAt, Cancellable: true,
		Actions: []ttypes.StatusAction{{ID: "agent.cancel", Label: "Cancel", Enabled: true}},
	}
}

// AgentToolDetail derives the status label without exposing structured tool names.
func AgentToolDetail(data any) string {
	if values, ok := data.(map[string]any); ok {
		if name, _ := values["name"].(string); name != "" {
			return "Running " + tools.DisplayToolName(name)
		}
	}
	return "Running tool"
}
