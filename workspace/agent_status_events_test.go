package workspace

import (
	"testing"
	"time"

	"crude/session"
	ttypes "crude/types"
	"crude/types/recovery"
)

func newAgentStatusEventWorkspace(t *testing.T) (*Workspace, *AgentNode) {
	t.Helper()
	w := &Workspace{AgentMgr: NewAgentManager(), Status: NewStatusCenter(nil, nil), SessionsByAgent: map[string]*Session{}}
	t.Cleanup(w.Status.Close)
	node := w.AgentMgr.AddOrchestrator("root", NewAgentState("root", NewConversation(), nil))
	w.SessionsByAgent[node.ID] = &Session{SessionID: "session", AgentID: node.ID, backend: &session.Session{}}
	return w, node
}

func requireAgentStatusPhase(t *testing.T, w *Workspace, id string, phase ttypes.AgentPhase, detail string, action bool) ttypes.AgentStatusSnapshot {
	t.Helper()
	got, ok := w.Status.Agent(id)
	if !ok || got.Phase != phase || got.Detail != detail || got.NeedsAction != action {
		t.Fatalf("status=%+v, want %s %q action=%v", got, phase, detail, action)
	}
	return got
}

func TestApplyAgentStatusEventLifecycleAndRequestDetails(t *testing.T) {
	w, node := newAgentStatusEventWorkspace(t)
	identity := ttypes.TurnIdentity{Generation: 9, ModelID: "runtime-model", AccountID: "account", ProviderID: "provider"}
	node.Model, node.State.TurnIdentity, node.State.Running = "configured-model", identity, true
	apply := func(kind string, data any) {
		w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: kind, Data: data})
	}
	apply("turn_started", identity)
	got := requireAgentStatusPhase(t, w, node.ID, ttypes.AgentPreparing, "Preparing context", false)
	if got.Ref.SessionID != "session" || got.Label != "Main Agent" || got.TurnGeneration != 9 || got.ModelID != "runtime-model" || got.AccountID != "account" || got.ProviderID != "provider" || got.StartedAt.IsZero() {
		t.Fatalf("lost owner/request identity: %+v", got)
	}
	apply("phase", map[string]any{"name": "preparing", "request_id": "request-1", "provider": "Gateway", "uploaded_bytes": int64(32), "total_bytes": int64(128), "progress": 25})
	got = requireAgentStatusPhase(t, w, node.ID, ttypes.AgentUploading, "Uploading context", false)
	if got.UploadedBytes != 32 || got.TotalBytes != 128 || !got.ProgressKnown || got.Progress != 25 {
		t.Fatalf("upload telemetry lost: %+v", got)
	}
	apply("phase", map[string]any{"name": "waiting_provider", "request_id": "request-1", "provider": "Gateway"})
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentWaiting, "Waiting for Gateway", false)
	ops := w.Status.Operations()
	if len(ops) != 1 || ops[0].ID != "request-1" || ops[0].Owner.SessionID != "session" || ops[0].Progress != 32 || ops[0].Total != 128 || ops[0].Unit != "bytes" || ops[0].State != ttypes.OperationWaiting || !ops[0].Cancellable {
		t.Fatalf("request operation lost exact telemetry: %+v", ops)
	}
	apply("thinking", nil)
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentThinking, "Reasoning", false)
	apply("stream_token", "response")
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentResponding, "Receiving response", false)
	for _, kind := range []string{"tool_call_streaming", "tool_call", "tool_update", "tool_result"} {
		apply(kind, map[string]any{"name": "execute_command", "args": `{"command":"printf exact"}`})
		requireAgentStatusPhase(t, w, node.ID, ttypes.AgentTool, "Running execute_command", false)
	}
	apply("phase", map[string]any{"name": "preparing", "request_id": "request-2"})
	got = requireAgentStatusPhase(t, w, node.ID, ttypes.AgentPreparing, "Preparing context", false)
	if got.ProgressKnown || got.UploadedBytes != 0 || got.TotalBytes != 0 {
		t.Fatalf("new request retained previous upload: %+v", got)
	}
	apply("done", map[string]any{"reason": "cancelled"})
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentCancelled, "Cancelled", false)
	for _, op := range w.Status.Operations() {
		if op.State != ttypes.OperationSucceeded || op.EndedAt.IsZero() {
			t.Fatalf("done left request running: %+v", op)
		}
	}
}

func TestBackgroundSummaryStatusSettlesAndRejectsStaleProgress(t *testing.T) {
	w, node := newAgentStatusEventWorkspace(t)
	node.State.Running = true
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "thinking"})
	apply := func(id, phase string) {
		w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "context_precompaction", Data: map[string]any{"phase": phase, "request_id": id, "stage_label": "Summarizing prior work"}})
	}
	apply("old", "started")
	apply("old", "discarded")
	if state, _ := w.Status.Agent(node.ID); state.Checkpoint != "" || state.Phase != ttypes.AgentThinking {
		t.Fatalf("discarded background work remained active: %+v", state)
	}
	apply("new", "started")
	apply("old", "progress")
	apply("old", "ready")
	if state, _ := w.Status.Agent(node.ID); state.Checkpoint != "preparing" {
		t.Fatalf("stale completion changed replacement job: %+v", state)
	}
	apply("new", "cancelled")
	apply("new", "progress")
	apply("new", "started")
	if state, _ := w.Status.Agent(node.ID); state.Checkpoint != "" || state.CheckpointDetail != "" {
		t.Fatalf("late progress resurrected cancelled work: %+v", state)
	}
	apply("ready", "started")
	apply("ready", "ready")
	apply("ready", "progress")
	if state, _ := w.Status.Agent(node.ID); state.Checkpoint != "ready" || state.Phase != ttypes.AgentThinking {
		t.Fatalf("late progress replaced readiness or changed agent work: %+v", state)
	}
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "context_compacted", Data: map[string]any{"request_id": "ready"}})
	apply("ready", "ready")
	if state, _ := w.Status.Agent(node.ID); state.Checkpoint != "" || state.CheckpointDetail != "" {
		t.Fatalf("adopted summary remained in details: %+v", state)
	}
	apply("failed", "started")
	apply("failed", "failed")
	if state, _ := w.Status.Agent(node.ID); state.Checkpoint != "" || state.CheckpointDetail != "" {
		t.Fatalf("failed background work remained active: %+v", state)
	}
}

func TestApplyAgentStatusEventPermissionsUseFamilyPolicy(t *testing.T) {
	w, root := newAgentStatusEventWorkspace(t)
	child := w.AgentMgr.AddSubAgent("child", root.ID, "Edward", "model", "crafter", NewAgentState("child", NewConversation(), nil))
	apply := func(kind string, data any) {
		w.ApplyAgentStatusEvent(AgentEvent{AgentID: child.ID, Type: kind, Data: data})
	}
	apply("blocking", map[string]string{"operation": "execute"})
	got := requireAgentStatusPhase(t, w, child.ID, ttypes.AgentApproval, "Needs approval", true)
	if got.Hidden || got.Ref.ParentID != root.ID || got.Ref.SessionID != "session" || got.Label != "Edward" {
		t.Fatalf("core inventory lost its owner or acquired shell visibility: %+v", got)
	}
	for _, kind := range []string{"blocking_auto", "permission_resolved", "blocking_complete"} {
		apply(kind, nil)
		requireAgentStatusPhase(t, w, child.ID, ttypes.AgentTool, "Working", false)
	}
	if !w.SetYoloForAgent(root.ID, true) {
		t.Fatal("could not update owning policy")
	}
	// A different selected root must not supply this family's permission policy.
	w.AgentMgr.AddOrchestrator("foreground", NewAgentState("foreground", nil, nil))
	apply("confirmation", map[string]any{"operation": "execute"})
	requireAgentStatusPhase(t, w, child.ID, ttypes.AgentTool, "Working", false)
	apply("blocking", map[string]any{"operation": "questions"})
	requireAgentStatusPhase(t, w, child.ID, ttypes.AgentQuestion, "Needs answer", true)
	apply("blocking_complete", nil)
	requireAgentStatusPhase(t, w, child.ID, ttypes.AgentTool, "Working", false)
}

func TestApplyAgentStatusEventRecoveryAcknowledgementAndError(t *testing.T) {
	w, node := newAgentStatusEventWorkspace(t)
	node.State.Running = true
	node.State.TurnIdentity.Generation = 3
	status := recovery.Status{ID: "incident", AgentID: node.ID, Generation: 3, State: recovery.NeedsAttention, Summary: "Check account", Detail: "Exact diagnostic", Action: "Repair account"}
	applyRecovery := func() {
		node.State.Recovery = status
		w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "recovery", Data: status})
	}
	applyRecovery()
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentNeedsAttention, "Check account", true)
	if issues := w.Status.Issues(); len(issues) != 1 || issues[0].Detail != "Exact diagnostic" {
		t.Fatalf("missing actionable issue: %+v", issues)
	}
	status.State, status.Summary, status.RetryAt = recovery.Recovering, "Recovering", time.Now().Add(time.Minute)
	applyRecovery()
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentRecovering, "Recovering", false)
	status.RetryAt = time.Time{}
	applyRecovery()
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentPreparing, "Resuming run", false)
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "phase", Data: map[string]any{"name": "waiting_provider"}})
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentWaiting, "Waiting for provider", false)
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "thinking"})
	before := requireAgentStatusPhase(t, w, node.ID, ttypes.AgentThinking, "Reasoning", false)
	stale := status
	stale.State = recovery.NeedsAttention
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "recovery", Data: stale})
	if after, _ := w.Status.Agent(node.ID); after != before {
		t.Fatal("unaccepted recovery rewound current phase")
	}
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "error", Data: "Exact provider failure"})
	requireAgentStatusPhase(t, w, node.ID, ttypes.AgentFailed, "Exact provider failure", false)
	for _, op := range w.Status.Operations() {
		if op.State != ttypes.OperationFailed {
			t.Fatalf("error left request running: %+v", op)
		}
	}
	notices := w.Status.Notifications(false)
	if len(notices) != 2 {
		t.Fatalf("recovery/failure history=%+v", notices)
	}
	for _, notice := range notices {
		if notice.Owner.ID != node.ID || notice.Read || notice.Dismissed {
			t.Fatalf("history acknowledged or misattributed: %+v", notice)
		}
	}
}

func TestApplyAgentStatusEventSafetyAndMissingOwners(t *testing.T) {
	w, node := newAgentStatusEventWorkspace(t)
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: "missing", Type: "error", Data: "missing"})
	if len(w.Status.Agents()) != 0 || len(w.Status.Notifications(false)) != 0 {
		t.Fatal("unknown owner created status")
	}
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "done"})
	before, _ := w.Status.Agent(node.ID)
	const reason = "Blocked exact command: rm /protected"
	w.ApplyAgentStatusEvent(AgentEvent{AgentID: node.ID, Type: "safety_alert", Data: map[string]any{"risk": "blocked", "reason": reason}})
	if after, _ := w.Status.Agent(node.ID); after != before {
		t.Fatal("safety history replaced terminal status")
	}
	notices := w.Status.Notifications(false)
	if len(notices) != 1 || notices[0].Detail != reason || notices[0].Severity != ttypes.SeverityCritical || len(w.Status.Issues()) != 0 {
		t.Fatalf("safety lost exact evidence or became pending decision: %+v", notices)
	}
	if got := AgentToolDetail(map[string]any{"name": `{"private_id":"agent-8f21"}`}); got != "Running tool" {
		t.Fatalf("structured tool name leaked: %q", got)
	}
}
