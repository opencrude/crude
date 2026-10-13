package viewmodel

import (
	"strings"
	"testing"
	"time"

	ttypes "crude/types"
)

func TestAgentStatusAutoAllowedTagsNeverRequestApproval(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	state := app.ws.AgentMgr().Get("root").State
	state.Running, state.Thinking = true, true
	for _, decision := range []string{"auto", "allowed", "always", "denied", "reviewer-allowed", "reviewer-denied"} {
		state.PendingPermTool, state.PendingPermTag = "execute_command", decision
		app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "blocking_auto"})
		app.syncAgentStatusInventory()
		status, _ := app.ws.Status().Agent("root")
		if status.NeedsAction || status.Phase == ttypes.AgentApproval || !strings.HasPrefix(formatAgentState(status), "Working") {
			t.Fatalf("permission result %q became approval: %+v", decision, status)
		}
	}
}

func TestAgentStatusYoloAndQuestionLifecycle(t *testing.T) {
	app := newFullTestApp(t)
	id := addBackendTestOrchestrator(t, app)
	state := app.ws.AgentMgr().Get(id).State
	state.Running = true
	state.PendingBlockID = "block-1"
	if !app.ws.SetYoloForAgent(id, true) {
		t.Fatal("could not enable backend YOLO")
	}
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: id, Type: "blocking", Data: map[string]any{"operation": "execute"}})
	app.syncAgentStatusInventory()
	status, _ := app.ws.Status().Agent(id)
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatalf("YOLO agent incorrectly needs approval: %+v", status)
	}
	// YOLO bypasses permission requests, not genuine questions to the user.
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: id, Type: "blocking", Data: map[string]any{"operation": "questions"}})
	app.syncAgentStatusInventory()
	status, _ = app.ws.Status().Agent(id)
	if !status.NeedsAction || status.Phase != ttypes.AgentQuestion || formatAgentState(status) != "Needs answer" {
		t.Fatalf("question lost its input state under YOLO: %+v", status)
	}
	state.PendingBlockID = ""
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: id, Type: "blocking_complete"})
	app.syncAgentStatusInventory()
	status, _ = app.ws.Status().Agent(id)
	if status.NeedsAction || status.Phase == ttypes.AgentQuestion {
		t.Fatalf("answered question still needs input: %+v", status)
	}
	if !app.ws.SetYoloForAgent(id, false) {
		t.Fatal("could not disable backend YOLO")
	}
	state.PendingBlockID = "block-2"
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: id, Type: "blocking", Data: map[string]string{"operation": "execute"}})
	app.syncAgentStatusInventory()
	status, _ = app.ws.Status().Agent(id)
	if !status.NeedsAction || status.Phase != ttypes.AgentApproval {
		t.Fatalf("real approval request was hidden: %+v", status)
	}
	state.PendingBlockID = ""
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: id, Type: "permission_resolved"})
	app.syncAgentStatusInventory()
	status, _ = app.ws.Status().Agent(id)
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatalf("resolved approval remained pending: %+v", status)
	}
}

func TestAgentStatusUsesOwningSessionsYoloPolicy(t *testing.T) {
	app := newFullTestApp(t)
	id := addBackendTestOrchestrator(t, app)
	if !app.ws.SetYoloForAgent(id, true) {
		t.Fatal("could not enable backend YOLO")
	}
	app.AddOrchestrator("foreground")
	app.OverlayState.SetYolo(false)
	state := app.ws.AgentMgr().Get(id).State
	state.Running, state.PendingBlockID = true, "background-block"
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: id, Type: "blocking"})
	app.syncAgentStatusInventory()
	status, _ := app.ws.Status().Agent(id)
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatalf("foreground policy replaced owning session's YOLO: %+v", status)
	}
}

func TestAgentStatusInventoryPreservesPhasesAndStableRow(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	state := app.ws.AgentMgr().Get("root").State
	state.Running, state.Thinking = true, true
	started := time.Now().Add(-10 * time.Second)
	for _, phase := range []ttypes.AgentPhase{ttypes.AgentPreparing, ttypes.AgentUploading, ttypes.AgentWaiting, ttypes.AgentThinking, ttypes.AgentResponding, ttypes.AgentTool, ttypes.AgentChildren, ttypes.AgentRetrying} {
		wsOf(app).Status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "root"}, Phase: phase, Detail: "phase detail", StartedAt: started})
		for range 3 {
			app.syncAgentStatusInventory()
			status, _ := app.ws.Status().Agent("root")
			if status.Phase != phase || !status.StartedAt.Equal(started) {
				t.Fatalf("inventory overwrote event phase %s: %+v", phase, status)
			}
			if phase != ttypes.AgentRetrying && formatAgentState(status) != "Working · 10s" {
				t.Fatalf("subsecond phase leaked into row: %q", formatAgentState(status))
			}
		}
	}
}

func TestFailedChildStatusesNeverRenderAsCompleted(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, status := range []string{"failed_error", "failed_timeout", "failed_quota", "failed_policy", "crashed"} {
		app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "sub_agent_completed", Data: map[string]string{"status": status}})
		got, _ := app.ws.Status().Agent("root")
		if got.Phase != ttypes.AgentFailed {
			t.Fatalf("%s rendered as %s", status, got.Phase)
		}
	}
}

func TestSafetyNoticeDoesNotReplaceLiveOrFinishedAgentStatus(t *testing.T) {
	const reason = "Command targets system files: /tmp/crude-tide-review-160"
	for _, outcome := range []string{"completed", "cancelled", "error"} {
		t.Run(outcome, func(t *testing.T) {
			app := newFullTestApp(t)
			app.AddOrchestrator("root")
			app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "phase", Data: map[string]any{"name": "waiting_provider"}})
			beforeNotice, _ := app.ws.Status().Agent("root")
			app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "safety_alert", Data: map[string]any{
				"risk": "destructive", "reason": reason,
			}})
			if afterNotice, _ := app.ws.Status().Agent("root"); afterNotice != beforeNotice {
				t.Errorf("safety notification changed the live status: %+v", afterNotice)
			}
			app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "stream_token", Data: "Finished checking."})
			app.syncAgentStatusInventory()
			live, _ := app.ws.Status().Agent("root")
			if live.NeedsAction || live.Phase != ttypes.AgentResponding || live.Detail == reason {
				t.Errorf("historical warning replaced live response status: %+v", live)
			}
			wantPhase, wantText := ttypes.AgentCompleted, "Completed"
			event := ttypes.AgentEventMsg{AgentID: "root", Type: "done", Data: map[string]any{"reason": outcome}}
			switch outcome {
			case "cancelled":
				wantPhase, wantText = ttypes.AgentCancelled, "Stopped"
			case "error":
				event.Type, event.Data = "error", "Provider connection failed"
				wantPhase, wantText = ttypes.AgentFailed, "Turn failed · details in transcript"
			}
			app.handleAgentEvent(event)
			for _, frame := range []int{1000, 8000, 20000} {
				app.SpinFrame = frame
				app.syncAgentStatusInventory()
				status, _ := app.ws.Status().Agent("root")
				if status.NeedsAction || status.Phase != wantPhase || status.Detail == reason {
					t.Errorf("terminal status overwritten by old warning: %+v", status)
				}
				if outcome == "error" && status.Detail != "Provider connection failed" {
					t.Errorf("concise presentation discarded the diagnostic: %+v", status)
				}
				app.paintShellFrame()
				line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
				if strings.Contains(line, "Command targets") || !strings.Contains(line, wantText) {
					t.Errorf("finished strip=%q, want %q", line, wantText)
				}
			}
			notices := app.ws.Status().Notifications(false)
			found := false
			for _, notice := range notices {
				if notice.Source == "safety" && notice.Owner.ID == "root" && notice.Summary == reason {
					found = true
					if notice.Severity != ttypes.SeverityWarning {
						t.Error("reviewable command warning was classified as a hard block")
					}
					if notice.Read || notice.Dismissed {
						t.Error("turn completion acknowledged the user's notification")
					}
				}
			}
			if !found {
				t.Fatal("safety notice was lost from notification history")
			}
			for _, issue := range app.ws.Status().Issues() {
				if issue.ID == "safety:root" && issue.ResolvedAt.IsZero() {
					t.Error("completed command assessment left an actionable issue")
				}
			}
		})
	}
}

func TestSafetyNoticesPreserveRealPermissionRequests(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "safety_alert", Data: map[string]any{
		"risk": "blocked", "reason": "Blocked disk write",
	}})
	notice, ok := wsOf(app).Status.LatestUnread(ttypes.SeverityCritical)
	if !ok || notice.Owner.ID != "root" || notice.Summary != "Blocked disk write" {
		t.Fatal("hard block did not retain its critical notification")
	}
	state := app.ws.AgentMgr().Get("root").State
	state.Running, state.PendingBlockID = true, "permission-1"
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "blocking", Data: map[string]any{"operation": "execute"}})
	app.syncAgentStatusInventory()
	status, _ := app.ws.Status().Agent("root")
	if !status.NeedsAction || status.Phase != ttypes.AgentApproval {
		t.Fatal("safety notice hid the real permission request")
	}
	state.PendingBlockID = ""
	app.syncAgentStatusEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "permission_resolved"})
	app.syncAgentStatusInventory()
	status, _ = app.ws.Status().Agent("root")
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatal("resolved permission remained actionable")
	}
}
