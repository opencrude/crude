package viewmodel

import (
	"strings"
	"testing"
	"time"

	tuitypes "crude/tui/types"
	ttypes "crude/types"
)

func TestAgentStatusAutoAllowedTagsNeverRequestApproval(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	state := app.AgentMgr.Get("root").State
	state.Running, state.Thinking = true, true
	for _, decision := range []string{"auto", "allowed", "always", "denied", "reviewer-allowed", "reviewer-denied"} {
		state.PendingPermTool, state.PendingPermTag = "execute_command", decision
		app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "blocking_auto"})
		app.syncAgentStatusInventory()
		status, _ := app.Status.Agent("root")
		if status.NeedsAction || status.Phase == ttypes.AgentApproval || !strings.HasPrefix(formatAgentState(status), "Working") {
			t.Fatalf("permission result %q became approval: %+v", decision, status)
		}
	}
}

func TestAgentStatusYoloAndQuestionLifecycle(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	state := app.AgentMgr.Get("root").State
	state.Running = true
	state.PendingBlockID = "block-1"
	app.OverlayState.SetYolo(true)
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "blocking", Data: map[string]any{"operation": "execute"}})
	app.syncAgentStatusInventory()
	status, _ := app.Status.Agent("root")
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatalf("YOLO agent incorrectly needs approval: %+v", status)
	}
	// YOLO bypasses permission requests, not genuine questions to the user.
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "blocking", Data: map[string]any{"operation": "questions"}})
	app.syncAgentStatusInventory()
	status, _ = app.Status.Agent("root")
	if !status.NeedsAction || status.Phase != ttypes.AgentQuestion || formatAgentState(status) != "Needs answer" {
		t.Fatalf("question lost its input state under YOLO: %+v", status)
	}
	state.PendingBlockID = ""
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "blocking_complete"})
	app.syncAgentStatusInventory()
	status, _ = app.Status.Agent("root")
	if status.NeedsAction || status.Phase == ttypes.AgentQuestion {
		t.Fatalf("answered question still needs input: %+v", status)
	}
	app.OverlayState.SetYolo(false)
	state.PendingBlockID = "block-2"
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "blocking", Data: map[string]string{"operation": "execute"}})
	app.syncAgentStatusInventory()
	status, _ = app.Status.Agent("root")
	if !status.NeedsAction || status.Phase != ttypes.AgentApproval {
		t.Fatalf("real approval request was hidden: %+v", status)
	}
	state.PendingBlockID = ""
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "permission_resolved"})
	app.syncAgentStatusInventory()
	status, _ = app.Status.Agent("root")
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatalf("resolved approval remained pending: %+v", status)
	}
}

func TestAgentStatusUsesOwningSessionsYoloPolicy(t *testing.T) {
	app := newFullTestApp(t)
	id := addBackendTestOrchestrator(t, app)
	if !app.SetYoloForAgent(id, true) {
		t.Fatal("could not enable backend YOLO")
	}
	app.AddOrchestrator("foreground")
	app.OverlayState.SetYolo(false)
	state := app.AgentMgr.Get(id).State
	state.Running, state.PendingBlockID = true, "background-block"
	app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: id, Type: "blocking"})
	app.syncAgentStatusInventory()
	status, _ := app.Status.Agent(id)
	if status.NeedsAction || status.Phase == ttypes.AgentApproval {
		t.Fatalf("foreground policy replaced owning session's YOLO: %+v", status)
	}
}

func TestAgentStatusInventoryPreservesPhasesAndStableRow(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	state := app.AgentMgr.Get("root").State
	state.Running, state.Thinking = true, true
	started := time.Now().Add(-10 * time.Second)
	for _, phase := range []ttypes.AgentPhase{ttypes.AgentPreparing, ttypes.AgentUploading, ttypes.AgentWaiting, ttypes.AgentThinking, ttypes.AgentResponding, ttypes.AgentTool, ttypes.AgentChildren, ttypes.AgentRetrying} {
		app.Status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "root"}, Phase: phase, Detail: "phase detail", StartedAt: started})
		for range 3 {
			app.syncAgentStatusInventory()
			status, _ := app.Status.Agent("root")
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
		app.syncAgentStatusEvent(tuitypes.AgentEventMsg{AgentID: "root", Type: "sub_agent_completed", Data: map[string]string{"status": status}})
		got, _ := app.Status.Agent("root")
		if got.Phase != ttypes.AgentFailed {
			t.Fatalf("%s rendered as %s", status, got.Phase)
		}
	}
}
