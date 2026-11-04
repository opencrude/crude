package viewmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// The workspace's Aeron is created once per attachment, never takes the user's
// tab, and stays in the Agents panel with a state the user can read.
func TestWorkspaceAeronIsEnsuredOnceAndListedIdle(t *testing.T) {
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
	row := findSidebarAgent(t, app, "aeron-agent")
	if row.Label != "Aeron" || row.Detail != "idle · minimized" {
		t.Fatalf("Aeron row does not report its state: %#v", row)
	}
	app.setActivity(activityAgents, true)
	app.paintShellFrame()
	paint := ansi.Strip(app.View().RenderContent())
	if !strings.Contains(paint, "Aeron") || !strings.Contains(paint, "idle") {
		t.Fatalf("the Agents panel does not paint the idle Aeron:\n%s", paint)
	}
	if again := app.ensureWorkspaceAeron(); again != nil {
		t.Fatal("a second ensure would create or resume the agent again")
	}
	if len(port.roles) != 1 {
		t.Fatalf("a second ensure created another agent: %v", port.roles)
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
	if app.aeronEnsureAttempts != 0 {
		t.Fatalf("a successful ensure kept its retry budget: %d", app.aeronEnsureAttempts)
	}
}

// An admission conflict retries a bounded number of times, then stops instead
// of looping against the core for the rest of the session.
func TestWorkspaceAeronRetryBudgetIsBounded(t *testing.T) {
	app, _ := projectToolsTestApp(t, 120, 36)
	app.aeronEnsureAttempts = aeronEnsureMaxAttempts
	if app.retryWorkspaceAeron() {
		t.Fatal("the retry budget kept retrying past its limit")
	}
	if !app.aeronEnsureFailed || app.aeronEnsureScheduled {
		t.Fatalf("bounded retry did not stop quietly: failed=%v scheduled=%v", app.aeronEnsureFailed, app.aeronEnsureScheduled)
	}
	app.aeronEnsureAttempts = 0
	if !app.retryWorkspaceAeron() || !app.aeronEnsureScheduled {
		t.Fatal("a fresh budget did not schedule a retry")
	}
}
