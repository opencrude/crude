package viewmodel

import (
	"fmt"
	"time"

	"crude/tools"
	"crude/tui/types"
	ttypes "crude/types"
	"crude/types/recovery"
	"crude/workspace"
)

type statusHydrateErrMsg struct{ Err error }

func (m *App) syncAgentStatusEvent(evt types.AgentEventMsg) {
	if m.Workspace == nil || m.Status == nil || evt.AgentID == "" {
		return
	}
	center := m.Status
	node := m.AgentMgr.Get(evt.AgentID)
	if node == nil {
		return
	}
	previous, _ := center.Agent(evt.AgentID)
	snapshot := previous
	snapshot.Ref = ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: evt.AgentID}
	if session := m.SessionsByAgent[evt.AgentID]; session != nil {
		snapshot.Ref.SessionID = session.SessionID
	}
	if node != nil {
		snapshot.Label, snapshot.Hidden = node.Label, node.IsHidden()
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
	}
	snapshot.UpdatedAt = time.Now()

	switch evt.Type {
	case "sub_agent_started":
		snapshot.Phase, snapshot.Detail = ttypes.AgentOpening, "Opening"
		snapshot.StartedAt = snapshot.UpdatedAt
	case "sub_agent_completed":
		result := "completed"
		if values, ok := evt.Data.(map[string]string); ok {
			result = values["status"]
		}
		snapshot.ReadOnly, snapshot.NeedsAction = true, false
		switch result {
		case "cancelled":
			snapshot.Phase, snapshot.Detail = ttypes.AgentCancelled, "Cancelled"
		case "failed", "error", "crashed", "failed_error", "failed_timeout", "failed_quota", "failed_policy":
			snapshot.Phase, snapshot.Detail = ttypes.AgentFailed, "Failed"
		default:
			snapshot.Phase, snapshot.Detail = ttypes.AgentCompleted, "Completed"
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
		phase, ok := workspace.ParseAgentPhase(evt.Data)
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
		snapshot.Phase, snapshot.Detail = ttypes.AgentTool, agentToolDetail(evt.Data)
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
		} else if !m.agentYoloEnabled(evt.AgentID) {
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

func agentToolDetail(data any) string {
	if values, ok := data.(map[string]any); ok {
		if name, _ := values["name"].(string); name != "" {
			return "Running " + tools.DisplayToolName(name)
		}
	}
	return "Running tool"
}

func (m *App) notifyStatus(severity ttypes.StatusSeverity, source, summary, detail string, owner ttypes.ResourceRef) string {
	if m.Workspace == nil || m.Status == nil {
		return ""
	}
	return m.Status.Notify(ttypes.NotificationRecord{
		DedupKey: source + ":" + owner.ID + ":" + summary,
		Owner:    owner, Severity: severity, Source: source, Summary: summary, Detail: detail,
		Transient: severity == ttypes.SeverityInfo,
	})
}

func (m *App) syncLSPStatusIssues() {
	if m.Workspace == nil || m.Status == nil || m.LSPMgr == nil {
		return
	}
	center := m.Status
	failed := make(map[string]bool)
	for _, server := range m.LSPMgr.ServerHealthSnapshots() {
		issueID := "lsp:" + server.ID
		if server.Error == "" {
			center.ResolveIssue(issueID)
			continue
		}
		failed[issueID] = true
		center.SetIssue(ttypes.IssueSnapshot{
			ID: issueID, Owner: ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID},
			Severity: ttypes.SeverityError, Summary: server.Name + " failed", Detail: server.Error,
			Actions: []ttypes.StatusAction{{ID: "lsp:restart", Label: "Restart Server", Enabled: true}},
		})
	}
	for _, issue := range center.Issues() {
		if issue.Owner.Kind == ttypes.ResourceLSP && !failed[issue.ID] {
			center.ResolveIssue(issue.ID)
		}
	}
}

func (m *App) syncAgentStatusInventory() {
	if m.Workspace == nil || m.Status == nil || m.AgentMgr == nil {
		return
	}
	for _, node := range m.AgentMgr.AllNodes() {
		if node == nil || node.State == nil {
			continue
		}
		status, exists := m.Status.Agent(node.ID)
		if !exists {
			status = ttypes.AgentStatusSnapshot{
				Ref:   ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: node.ID},
				Phase: ttypes.AgentIdle, Detail: "Idle",
			}
		}
		status.Label, status.Hidden = node.Label, node.IsHidden()
		status.ModelID = firstNonempty(node.Model, status.ModelID)
		status.ReadOnly = node.State.Completed
		// PendingPermTool is a display tag for an already-decided permission,
		// not a pending request. Only a correlated block can need input.
		blocked := node.State.PendingBlockID != "" && node.State.PendingPermTag == ""
		question := blocked && status.Phase == ttypes.AgentQuestion
		approval := blocked && !question && !m.agentYoloEnabled(node.ID)
		status.NeedsAction = question || approval
		recoveryStatus := node.State.RecoveryStatus()
		switch {
		case node.State.Cancelling:
			status.Phase, status.Detail = ttypes.AgentCancelling, "Stopping"
			status.NeedsAction = false
		case question:
			status.Phase, status.Detail = ttypes.AgentQuestion, "Needs answer"
		case approval:
			status.Phase, status.Detail = ttypes.AgentApproval, "Needs approval"
		case recoveryStatus.State == recovery.NeedsAttention:
			status.Phase, status.Detail, status.NeedsAction = ttypes.AgentNeedsAttention, recoveryStatus.Summary, true
		case recoveryStatus.State == recovery.Interrupted:
			status.Phase, status.Detail, status.NeedsAction = ttypes.AgentInterrupted, recoveryStatus.Summary, true
		case recoveryStatus.State == recovery.Recovering && recoveryStatus.CanRetry():
			status.Phase, status.Detail, status.RetryAt = ttypes.AgentRecovering, recoveryStatus.Summary, recoveryStatus.RetryAt
		case node.State.Completed && status.Phase != ttypes.AgentCancelled && status.Phase != ttypes.AgentFailed:
			status.Phase, status.Detail = ttypes.AgentCompleted, "Completed"
		case !node.State.IsActive() && status.Phase != ttypes.AgentCompleted && status.Phase != ttypes.AgentCancelled && status.Phase != ttypes.AgentFailed && status.Phase != ttypes.AgentCompactionResult:
			status.Phase, status.Detail = ttypes.AgentIdle, "Idle"
		case node.State.IsActive() && (!exists || status.Phase == ttypes.AgentIdle || status.Phase == ttypes.AgentCompactionResult || status.Phase == ttypes.AgentApproval || status.Phase == ttypes.AgentQuestion):
			status.Phase, status.Detail = ttypes.AgentThinking, "Working"
		}
		// Preserve the event-owned phase while working. Thinking/Streaming are
		// broad lifecycle flags, not permission to overwrite each tool/retry phase.
		m.Status.SetAgent(status)
	}
}
