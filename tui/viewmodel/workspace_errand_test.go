package viewmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"crude/agent/provider"
	"crude/core/port"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// The workspace's Errand is created once per attachment, never takes the user's
// tab, and opens from the header instead of the ordinary Agents collection.
func TestWorkspaceErrandIsEnsuredOnceAndOpensFromHeader(t *testing.T) {
	app, port := projectToolsTestApp(t, 120, 36)
	port.onEnsure = func() {
		//nolint:staticcheck // The wrapper is not a *local; unwrap its embedded workspace.
		base := shellport.Unwrap(port.Workspace)
		if base.SessionsByAgent == nil {
			base.SessionsByAgent = make(map[string]*workspace.Session)
		}
		base.SessionsByAgent["errand-agent"] = &workspace.Session{
			SessionID: "errand-session",
			AgentID:   "errand-agent",
			Settings:  &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "errand"}},
		}
		base.AgentMgr.AddOrchestrator("errand-agent", workspace.NewAgentState("errand-agent", nil, nil))
	}
	cmd := app.ensureWorkspaceErrand()
	if cmd == nil || !app.errandEnsureInFlight {
		t.Fatal("the workspace Errand was not ensured")
	}
	runAppCommand(t, app, cmd)
	if len(port.roles) != 1 || port.roles[0] != "errand" {
		t.Fatalf("ensure did not ask for the Errand role: %v", port.roles)
	}
	if app.errandEnsureInFlight || app.errandEnsureFailed {
		t.Fatalf("ensure did not settle: inFlight=%v failed=%v", app.errandEnsureInFlight, app.errandEnsureFailed)
	}
	if app.workspaceErrandAgent() != "errand-agent" {
		t.Fatal("the ensured Errand is not the workspace's agent")
	}
	if !app.AgentView.IsHidden("errand-agent") {
		t.Fatal("the workspace Errand took the user's tab")
	}
	requireErrandAbsentFromAgentList(t, app)
	app.setActivity(activityAgents, true)
	app.toggleWorkspacePanel(termdock.PanelWorkspace)
	app.paintShellFrame()
	paint := ansi.Strip(app.View().RenderContent())
	if !strings.Contains(paint, "Open workspace Errand") {
		t.Fatalf("the panel header does not expose workspace Errand:\n%s", paint)
	}
	app.hideWorkspacePanel()
	openErrandHeader(t, app)
	if app.AgentView.ActiveID() != "errand-agent" || app.AgentView.IsHidden("errand-agent") {
		t.Fatal("the header did not restore the existing workspace Errand")
	}
	if again := app.ensureWorkspaceErrand(); again != nil {
		t.Fatal("a second ensure would create or resume the agent again")
	}
	if len(port.roles) != 1 {
		t.Fatalf("a second ensure created another agent: %v", port.roles)
	}
}

func requireErrandAbsentFromAgentList(t *testing.T, app *App) {
	t.Helper()
	for _, row := range app.agentSidebarItems() {
		if row.Label == "Errand" || row.ID == "errand:workspace" ||
			strings.HasPrefix(row.ID, "agent:") && app.isErrandAgent(strings.TrimPrefix(row.ID, "agent:")) {
			t.Fatalf("workspace Errand remains in the ordinary agent list: %+v", row)
		}
	}
}

func findSidebarAgent(t *testing.T, app *App, id string) ui.ListItem {
	t.Helper()
	for _, item := range app.agentSidebarItems() {
		if item.ID == "agent:"+id {
			return item
		}
	}
	t.Fatalf("Agents panel has no row for %q: %#v", id, app.agentSidebarItems())
	return ui.ListItem{}
}

// The automatic ensure is background work: it waits for the startup delay, a
// quiet shell and no history operation, so a restore never loses its slot.
func TestWorkspaceErrandWaitsForQuietAndRestore(t *testing.T) {
	app, port := projectToolsTestApp(t, 120, 36)
	port.onEnsure = func() {
		//nolint:staticcheck // The wrapper is not a *local; unwrap its embedded workspace.
		base := shellport.Unwrap(port.Workspace)
		if base.SessionsByAgent == nil {
			base.SessionsByAgent = make(map[string]*workspace.Session)
		}
		base.SessionsByAgent["errand-agent"] = &workspace.Session{
			SessionID: "errand-session",
			AgentID:   "errand-agent",
			Settings:  &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "errand"}},
		}
		base.AgentMgr.AddOrchestrator("errand-agent", workspace.NewAgentState("errand-agent", nil, nil))
	}
	app.requestWorkspaceErrandEnsure()
	now := time.Now()
	due := now.Add(errandEnsureStartupDelay + time.Millisecond)

	app.LastInteractionTime = now
	if cmd := app.workspaceErrandTick(due); cmd != nil {
		t.Fatal("the workspace Errand started while the user was working")
	}
	app.historyOperation = &historyOperation{}
	app.LastInteractionTime = now.Add(-errandEnsureQuietPeriod - time.Second)
	if cmd := app.workspaceErrandTick(due); cmd != nil {
		t.Fatal("the workspace Errand started during a history restore")
	}
	app.historyOperation = nil
	cmd := app.workspaceErrandTick(due)
	if cmd == nil {
		t.Fatal("the workspace Errand did not start after the shell became quiet")
	}
	runAppCommand(t, app, cmd)
	if app.workspaceErrandAgent() != "errand-agent" {
		t.Fatal("the quiet ensure did not install the workspace Errand")
	}
}

// Work for Errand is never dropped because other session work held the core:
// it waits, Errand opens when admission is free, and the order is delivered.
// The user is not asked to send it again and is not told about the wait.
func TestWorkspaceErrandWorkWaitsThroughBusyAdmission(t *testing.T) {
	app, port := projectToolsTestApp(t, 120, 36)
	port.busy = 1
	cmd := app.taskWorkspaceErrand("<order/>", "Set up 1 code intelligence tool: marksman.")
	if cmd == nil {
		t.Fatal("work for Errand did not start Errand")
	}
	runAppCommand(t, app, cmd)
	if len(app.errandOrders) == 0 || app.errandEnsureFailed || !app.errandEnsureScheduled {
		t.Fatalf("busy admission dropped the work: orders=%v failed=%v scheduled=%v", app.errandOrders, app.errandEnsureFailed, app.errandEnsureScheduled)
	}
	if port.roles != nil {
		t.Fatal("a busy answer opened an agent")
	}
	// The order does not wait for a quiet shell, only for the retry time.
	app.LastInteractionTime = time.Now()
	retry := app.workspaceErrandTick(time.Now().Add(errandEnsureStartupDelay + time.Millisecond))
	if retry == nil {
		t.Fatal("waiting work did not retry Errand")
	}
	runAppCommand(t, app, retry)
	sent := app.pendingFirstInputs["errand-agent"][0]
	if sent.Text != "<order/>" || sent.DisplayText != "Set up 1 code intelligence tool: marksman." || len(app.errandOrders) != 0 {
		t.Fatalf("the waiting work was not delivered: sent=%+v orders=%v", sent, app.errandOrders)
	}
}

// A background open that fails is silent and stops; work that cannot reach
// Errand is reported once with the next step and is not retried in a loop.
func TestWorkspaceErrandFailureStopsAndReportsOnlyWork(t *testing.T) {
	app, _ := projectToolsTestApp(t, 120, 36)
	app.errandEnsureInFlight = true
	if cmd := app.applyErrandEnsure(&workspace.NewSessionResult{Error: "no model", NeedsModel: true}); cmd != nil {
		t.Fatal("a failed background open started more work")
	}
	if !app.errandEnsureFailed || app.errandEnsureInFlight {
		t.Fatal("a failed background open did not stop")
	}
	if cmd := app.workspaceErrandTick(time.Now().Add(time.Hour)); cmd != nil {
		t.Fatal("a failed background open retried in a loop")
	}
	app.errandOrders = []provider.UserInput{{Text: "<order/>"}}
	app.errandEnsureInFlight = true
	app.applyErrandEnsure(&workspace.NewSessionResult{Error: "no model", NeedsModel: true})
	if len(app.errandOrders) != 1 || app.errandEnsureScheduled {
		t.Fatal("failed work was lost or started an automatic retry loop")
	}
	reported := false
	for _, notice := range app.ws.Status().Notifications(false) {
		reported = reported || strings.Contains(notice.Summary, "Errand needs a model") &&
			strings.Contains(notice.Summary, "Choose a model in Settings")
	}
	if !reported {
		t.Fatal("failed work was not reported with its next step")
	}
}
