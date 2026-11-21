package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/launcher"
)

// The automatic open of the workspace Errand is background work. On a slow
// link its reply can take seconds; a New Session click in that time must
// still open the user's session when the open settles, never be lost.
func TestRemoteNewSessionClickWaitsForAutomaticErrandOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	app := shell.app
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return len(shell.fifo) == 0 })

	app.requestWorkspaceErrandEnsure()
	app.errandEnsureEarliest = time.Now().Add(-time.Millisecond)
	app.LastInteractionTime = time.Now().Add(-errandEnsureQuietPeriod - time.Second)
	errandOpen := app.workspaceErrandTick(time.Now())
	if errandOpen == nil || !app.errandEnsureInFlight {
		t.Fatal("the automatic Errand open did not start")
	}

	// The open's reply has not reached Update yet: the user clicks the
	// painted New Session action now.
	app.paintShellFrame()
	action, ok := app.rootHost.ScreenRect(launcher.NewSessionID)
	if !ok || action.Empty() {
		t.Fatal("the launcher does not paint New Session")
	}
	x, y := action.X+action.W/2, action.Y
	sequence := app.sessionLifecycleSequence
	_, press := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, press)
	_, release := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, release)
	if app.sessionLifecycleSequence != sequence || !app.errandEnsureInFlight {
		t.Fatal("the click started a second session request beside the Errand open")
	}

	runAppCommand(t, app, errandOpen)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		active := app.AgentView.ActiveID()
		return active != "" && !app.isErrandAgent(active)
	})
	assertAgentIsPaintedView(t, app, app.AgentView.ActiveID())
	errand := app.workspaceErrandAgent()
	if errand == "" || !app.AgentView.IsHidden(errand) {
		t.Fatalf("the workspace Errand did not open in the background: id=%q", errand)
	}
	if app.sessionLifecyclePending || app.sessionLifecycleAfterErrand != nil {
		t.Fatal("the session request did not settle")
	}
}

// Opening another workspace while the automatic Errand open is in flight waits
// for that reply and then switches; it does not refuse the user's choice.
func TestRemoteWorkspaceSwitchWaitsForAutomaticErrandOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root, target := t.TempDir(), t.TempDir()
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return len(shell.fifo) == 0 })

	app.requestWorkspaceErrandEnsure()
	app.errandEnsureEarliest = time.Now().Add(-time.Millisecond)
	app.LastInteractionTime = time.Now().Add(-errandEnsureQuietPeriod - time.Second)
	errandOpen := app.workspaceErrandTick(time.Now())
	if errandOpen == nil || !app.errandEnsureInFlight {
		t.Fatal("the automatic Errand open did not start")
	}
	runAppCommand(t, app, confirmWorkspaceSwitchTest(t, app, target))
	if len(app.pendingOperationIssues) != 0 || app.workspaceSwitchIntent == nil {
		t.Fatalf("the switch was refused while Errand opened: issues=%+v", app.pendingOperationIssues)
	}
	if !sameWorkspaceRoot(app.ws.RootPath(), root) {
		t.Fatal("the switch did not wait for the Errand reply")
	}

	runAppCommand(t, app, errandOpen)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return sameWorkspaceRoot(app.ws.RootPath(), target) })
	if len(app.pendingOperationIssues) != 0 || app.sessionLifecyclePending {
		t.Fatalf("the switch did not settle cleanly: issues=%+v", app.pendingOperationIssues)
	}
}
