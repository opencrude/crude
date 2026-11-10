package viewmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/shellport"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// The workspace's Aeron is created once per attachment, never takes the user's
// tab, and opens from the header instead of the ordinary Agents collection.
func TestWorkspaceAeronIsEnsuredOnceAndOpensFromHeader(t *testing.T) {
	app, port := projectToolsTestApp(t, 120, 36)
	port.onEnsure = func() {
		//nolint:staticcheck // The wrapper is not a *local; unwrap its embedded workspace.
		base := shellport.Unwrap(port.Workspace)
		if base.SessionsByAgent == nil {
			base.SessionsByAgent = make(map[string]*workspace.Session)
		}
		base.SessionsByAgent["aeron-agent"] = &workspace.Session{
			SessionID: "aeron-session",
			AgentID:   "aeron-agent",
			Settings:  &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "aeron"}},
		}
		base.AgentMgr.AddOrchestrator("aeron-agent", workspace.NewAgentState("aeron-agent", nil, nil))
	}
	cmd := app.ensureWorkspaceAeron()
	if cmd == nil || !app.aeronEnsureInFlight {
		t.Fatal("the workspace Aeron was not ensured")
	}
	runAppCommand(t, app, cmd)
	if len(port.roles) != 1 || port.roles[0] != "aeron" {
		t.Fatalf("ensure did not ask for the Aeron role: %v", port.roles)
	}
	if app.aeronEnsureInFlight || app.aeronEnsureFailed {
		t.Fatalf("ensure did not settle: inFlight=%v failed=%v", app.aeronEnsureInFlight, app.aeronEnsureFailed)
	}
	if app.workspaceAeronAgent() != "aeron-agent" {
		t.Fatal("the ensured Aeron is not the workspace's agent")
	}
	if !app.AgentView.IsHidden("aeron-agent") {
		t.Fatal("the workspace Aeron took the user's tab")
	}
	requireAeronAbsentFromAgentList(t, app)
	app.setActivity(activityAgents, true)
	app.toggleWorkspacePanel(termdock.PanelWorkspace)
	app.paintShellFrame()
	paint := ansi.Strip(app.View().RenderContent())
	if !strings.Contains(paint, "Open workspace Aeron") {
		t.Fatalf("the panel header does not expose workspace Aeron:\n%s", paint)
	}
	app.hideWorkspacePanel()
	openAeronHeader(t, app)
	if app.AgentView.ActiveID() != "aeron-agent" || app.AgentView.IsHidden("aeron-agent") {
		t.Fatal("the header did not restore the existing workspace Aeron")
	}
	if again := app.ensureWorkspaceAeron(); again != nil {
		t.Fatal("a second ensure would create or resume the agent again")
	}
	if len(port.roles) != 1 {
		t.Fatalf("a second ensure created another agent: %v", port.roles)
	}
}

func requireAeronAbsentFromAgentList(t *testing.T, app *App) {
	t.Helper()
	for _, row := range app.agentSidebarItems() {
		if row.Label == "Aeron" || row.ID == "aeron:workspace" ||
			strings.HasPrefix(row.ID, "agent:") && app.isAeronAgent(strings.TrimPrefix(row.ID, "agent:")) {
			t.Fatalf("workspace Aeron remains in the ordinary agent list: %+v", row)
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
func TestWorkspaceAeronWaitsForQuietAndRestore(t *testing.T) {
	app, port := projectToolsTestApp(t, 120, 36)
	port.onEnsure = func() {
		//nolint:staticcheck // The wrapper is not a *local; unwrap its embedded workspace.
		base := shellport.Unwrap(port.Workspace)
		if base.SessionsByAgent == nil {
			base.SessionsByAgent = make(map[string]*workspace.Session)
		}
		base.SessionsByAgent["aeron-agent"] = &workspace.Session{
			SessionID: "aeron-session",
			AgentID:   "aeron-agent",
			Settings:  &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "aeron"}},
		}
		base.AgentMgr.AddOrchestrator("aeron-agent", workspace.NewAgentState("aeron-agent", nil, nil))
	}
	app.requestWorkspaceAeronEnsure()
	now := time.Now()
	due := now.Add(aeronEnsureStartupDelay + time.Millisecond)

	app.LastInteractionTime = now
	if cmd := app.workspaceAeronTick(due); cmd != nil {
		t.Fatal("the workspace Aeron started while the user was working")
	}
	app.historyOperation = &historyOperation{}
	app.LastInteractionTime = now.Add(-aeronEnsureQuietPeriod - time.Second)
	if cmd := app.workspaceAeronTick(due); cmd != nil {
		t.Fatal("the workspace Aeron started during a history restore")
	}
	app.historyOperation = nil
	cmd := app.workspaceAeronTick(due)
	if cmd == nil {
		t.Fatal("the workspace Aeron did not start after the shell became quiet")
	}
	runAppCommand(t, app, cmd)
	if app.workspaceAeronAgent() != "aeron-agent" {
		t.Fatal("the quiet ensure did not install the workspace Aeron")
	}
}

// Work for Aeron is never dropped because other session work held the core:
// it waits, Aeron opens when admission is free, and the order is delivered.
// The user is not asked to send it again and is not told about the wait.
func TestWorkspaceAeronWorkWaitsThroughBusyAdmission(t *testing.T) {
	app, port := projectToolsTestApp(t, 120, 36)
	port.busy = 1
	cmd := app.taskWorkspaceAeron("<order/>", "Set up 1 code intelligence tool: marksman.")
	if cmd == nil {
		t.Fatal("work for Aeron did not start Aeron")
	}
	runAppCommand(t, app, cmd)
	if len(app.aeronOrders) == 0 || app.aeronEnsureFailed || !app.aeronEnsureScheduled {
		t.Fatalf("busy admission dropped the work: orders=%v failed=%v scheduled=%v", app.aeronOrders, app.aeronEnsureFailed, app.aeronEnsureScheduled)
	}
	if port.roles != nil {
		t.Fatal("a busy answer opened an agent")
	}
	// The order does not wait for a quiet shell, only for the retry time.
	app.LastInteractionTime = time.Now()
	retry := app.workspaceAeronTick(time.Now().Add(aeronEnsureStartupDelay + time.Millisecond))
	if retry == nil {
		t.Fatal("waiting work did not retry Aeron")
	}
	runAppCommand(t, app, retry)
	sent := app.pendingFirstInputs["aeron-agent"][0]
	if sent.Text != "<order/>" || sent.DisplayText != "Set up 1 code intelligence tool: marksman." || len(app.aeronOrders) != 0 {
		t.Fatalf("the waiting work was not delivered: sent=%+v orders=%v", sent, app.aeronOrders)
	}
}

// A background open that fails is silent and stops; work that cannot reach
// Aeron is reported once with the next step and is not retried in a loop.
func TestWorkspaceAeronFailureStopsAndReportsOnlyWork(t *testing.T) {
	app, _ := projectToolsTestApp(t, 120, 36)
	app.aeronEnsureInFlight = true
	if cmd := app.applyAeronEnsure(&workspace.NewSessionResult{Error: "no model", NeedsModel: true}); cmd != nil {
		t.Fatal("a failed background open started more work")
	}
	if !app.aeronEnsureFailed || app.aeronEnsureInFlight {
		t.Fatal("a failed background open did not stop")
	}
	if cmd := app.workspaceAeronTick(time.Now().Add(time.Hour)); cmd != nil {
		t.Fatal("a failed background open retried in a loop")
	}
	app.aeronOrders = []provider.UserInput{{Text: "<order/>"}}
	app.aeronEnsureInFlight = true
	app.applyAeronEnsure(&workspace.NewSessionResult{Error: "no model", NeedsModel: true})
	if len(app.aeronOrders) != 1 || app.aeronEnsureScheduled {
		t.Fatal("failed work was lost or started an automatic retry loop")
	}
	reported := false
	for _, notice := range app.ws.Status().Notifications(false) {
		reported = reported || strings.Contains(notice.Summary, "Aeron needs a model") &&
			strings.Contains(notice.Summary, "Choose a model in Settings")
	}
	if !reported {
		t.Fatal("failed work was not reported with its next step")
	}
}
