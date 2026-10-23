package viewmodel

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
	"crude/workspace"
)

func TestRecoveryJourneyPreservesDraftAndCanonicalHydration(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("recovery-owner")
	app.maybeHydrateComposerDraft()
	const draft = "Keep this exact draft.\nIncluding the next instruction."
	app.Input.SetInput(draft)
	status := recovery.Status{
		ID: "incident", AgentID: "recovery-owner", Generation: 1, Attempt: 1,
		StartedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(1001, 0),
		Summary: "Connection needs attention", Detail: "HTTP 401: configured account was rejected",
		Action: "Update Accounts, then /retry.",
	}
	for _, state := range []recovery.State{recovery.Recovering, recovery.NeedsAttention, recovery.Recovered, recovery.Interrupted, recovery.Cancelled} {
		status.State = state
		status.UpdatedAt = status.UpdatedAt.Add(time.Second)
		_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: status.AgentID, Data: status})
		if app.Input.Value() != draft {
			t.Fatalf("state %s consumed the composer draft", state)
		}
		node := app.ws.AgentMgr().Get(status.AgentID)
		messages := node.State.Messages.Messages()
		if len(messages) != 1 || messages[0].Recovery == nil || messages[0].Recovery.State != state {
			t.Fatalf("state %s did not retain one incident: %+v", state, messages)
		}
		projected := ChatMessagesFromViews(messages)
		hydrated := ChatMessagesFromViews([]workspace.ChatMessageView{workspace.RecoveryMessage(status)})
		if !reflect.DeepEqual(projected, hydrated) {
			t.Fatalf("live/hydrated recovery changed at %s", state)
		}
	}
	if app.canRetryAgentRecovery(status.AgentID) {
		t.Fatal("settled explicit cancellation still offers Continue")
	}
}

func TestSendingWhilePassivelyInterruptedKeepsExactComposerDraft(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	app.maybeHydrateComposerDraft()
	const draft = "My exact new instruction.\nKeep it until the pending run is resolved."
	app.Input.SetInput(draft)
	app.Input.SetCursorFlat(7)
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: recovery.Status{
		ID: "pending", AgentID: "owner", Generation: 1, State: recovery.Interrupted,
	}})
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.Input.Value() != draft || app.Input.CursorFlat() != 7 {
		t.Fatal("rejected pending-run submission consumed or reset the draft")
	}
	if got := app.ws.AgentMgr().Get("owner").State.Messages.Messages(); len(got) != 1 || got[0].Recovery == nil {
		t.Fatalf("rejected input fabricated a sent human message: %+v", got)
	}
}

func TestRecoveryCancelAcknowledgementPassesShieldButLateRetryCannotReactivate(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	status := recovery.Status{ID: "incident", AgentID: "owner", Generation: 1, State: recovery.NeedsAttention}
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: status})
	node := app.ws.AgentMgr().Get("owner")
	node.State.BeginCancellation()
	status.State = recovery.Recovering
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: status})
	if !node.State.Cancelling || node.State.RecoveryStatus().State != recovery.NeedsAttention {
		t.Fatal("late recovering status defeated the explicit stop")
	}
	status.State = recovery.Cancelled
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: status})
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "done", AgentID: "owner", Data: map[string]any{"reason": "cancelled"}})
	app.syncAgentStatusInventory()
	snapshot, _ := app.ws.Status().Agent("owner")
	if node.State.IsActive() || app.canRetryAgentRecovery("owner") || snapshot.NeedsAction || snapshot.Phase != types.AgentCancelled {
		t.Fatalf("stopped recovery is still working/actionable: %+v", snapshot)
	}
}

func TestNetworkPaletteDeepLinkAndRecoveryActionsAreDiscoverable(t *testing.T) {
	app := newFullTestApp(t)
	app.openNetworkSettings()
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(window)
	if body == nil {
		t.Fatal("Network Settings did not open the retained frame")
	}
	selected := body.Model().SelectedCategory()
	found := false
	for _, item := range body.Model().CategoryItems() {
		if index, ok := item.Payload.(int); ok && index == selected {
			found = item.Label == "Connection"
		}
	}
	if !found {
		t.Fatal("Network Settings did not deep-link to Connection")
	}
	app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	app.AddOrchestrator("owner")
	_ = app.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: "owner", Data: recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 1, State: recovery.NeedsAttention,
		Summary: "Account needs attention", Detail: "HTTP 401", Action: "Update Accounts, then Continue",
	}})
	menu := app.buildChatContextMenu(tea.Mouse{})
	var labels []string
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			labels = append(labels, item.Label)
		}
	}
	if !strings.Contains(strings.Join(labels, "\n"), "Continue run") {
		t.Fatal("pending recovery has no discoverable chat action")
	}
}
