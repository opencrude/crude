package viewmodel

import (
	"crude/workspace"
	"fmt"
	"strings"
	"testing"
)

func TestYoloHeaderFollowsDisplayedAgentNotLastSession(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = width, 40
			first := addBackendTestOrchestrator(t, app)
			second := addBackendTestOrchestrator(t, app)
			t.Cleanup(wsOf(app).CloseAllSessions)
			app.AddOrchestrator(first)
			app.setAgentYolo(first, true)
			check := func(want bool) {
				t.Helper()
				app.paintShellFrame()
				text := rowText(app, 0)
				if strings.Contains(text, "YOLO") != want {
					t.Fatalf("YOLO=%t header=%q", want, text)
				}
			}
			check(true)
			openEditorTabs(t, app, "main.go")
			check(false)
			app.activateAgentWindowTab(first)
			check(true)
			app.AddOrchestrator(second)
			check(false)
			// A cached overlay bit from a previously viewed session is not
			// authority for either a newly focused root or a background update.
			app.OverlayState.SetYolo(true)
			check(false)
			app.setAgentYolo(second, true)
			check(true)
			app.setAgentYolo(second, false)
			check(false)
			app.activateAgentWindowTab(first)
			check(true)
			// Descendants inherit the owning session's policy, not the shell's
			// last root. Exercise the ordinary child-tab selection path.
			pane := app.ws.PaneMgr().GetOrCreate("header-child", workspace.PaneAgent, "child")
			state := workspace.NewAgentState("header-child", pane.Messages, app.ws.Logger())
			app.ws.AgentMgr().AddSubAgent("header-child", first, "Child", "model", "researcher", state)
			app.ensureAgentProjection("header-child")
			app.RestoreAgent("header-child")
			app.syncAgentWindowTabs()
			app.activateAgentWindowTab("header-child")
			check(true)
			app.setAgentYolo(first, false)
			check(false)
		})
	}
}

func TestYoloHeaderDoesNotFollowAgentIntoTerminalPlacements(t *testing.T) {
	app, _ := newRealShellApp(t)
	id := addBackendTestOrchestrator(t, app)
	app.setAgentYolo(id, true)
	created, ok := app.openNewTerminal()().(terminalCreatedMsg)
	if !ok || created.err != nil {
		t.Fatalf("terminal creation: %+v", created)
	}
	t.Cleanup(func() { app.closeTerminal(created.info.ID) })
	_, next := app.Update(created)
	runTerminalCommand(t, app, next)
	check := func() {
		t.Helper()
		app.paintShellFrame()
		if text := rowText(app, 0); strings.Contains(text, "YOLO") {
			t.Fatalf("terminal borrowed agent policy: %q", text)
		}
	}
	check()
	app.openTerminalAsTab(created.info.ID)
	check()
	app.promoteTerminal(created.info.ID)
	check()
}
