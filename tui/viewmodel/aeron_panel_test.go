package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func seedNamedAeronPanel(t *testing.T, app *App) {
	t.Helper()
	ws := wsOf(app)
	ws.Logger = stubLogger{}
	ws.AgentMgr.AddOrchestrator("workspace-agent", workspace.NewAgentState("workspace-agent", nil, nil)).Label = "Naveen"
	sess := &workspace.Session{SessionID: "workspace-session", AgentID: "workspace-agent",
		Settings: &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "aeron"}}}
	if ws.SessionsByAgent == nil {
		ws.SessionsByAgent = make(map[string]*workspace.Session)
	}
	ws.SessionsByAgent["workspace-agent"] = sess
	app.AgentView.SetHidden("workspace-agent", true)
}

func TestWorkspaceAeronPanelPlacementAndNamedAgentAbsence(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		for _, placement := range []string{"dock", "tab", "window"} {
			t.Run(fmt.Sprint(size)+placement, func(t *testing.T) {
				app := newFullTestApp(t)
				app.width, app.height = size[0], size[1]
				app.AddOrchestrator("main")
				app.ActiveSession = workspace.NewSession("main", t.TempDir(), nil, stubLogger{})
				wsOf(app).SessionsByAgent = map[string]*workspace.Session{"main": app.ActiveSession}
				app.maybeHydrateComposerDraft()
				seedNamedAeronPanel(t, app)
				app.Input.SetInput("private draft")
				app.setActivity(activityAgents, true)
				app.toggleWorkspacePanel(termdock.PanelWorkspace)
				if placement != "dock" {
					action := termdock.ActionOpenAsTab
					if placement == "window" {
						action = termdock.ActionOpenAsWindow
					}
					app.handleHostAction(&ui.Action{ID: action, Payload: termdock.PanelWorkspace})
				}
				app.paintShellFrame()
				if strings.Contains(rowText(app, app.frameTopRow()), "Aeron") {
					t.Fatal("rejected Aeron application-header link is still visible")
				}
				for _, item := range app.statusTabItems() {
					if item.ID == "agent:workspace-agent" || item.Label == "Naveen" || item.Label == "Aeron" {
						t.Fatalf("workspace agent remains in the normal panel list: %+v", item)
					}
					if item.ID == "group-agents" && item.Label != "Open Agents (1)" {
						t.Fatal("workspace agent still affects panel count", item.Label)
					}
				}
				requireAeronAbsentFromAgentList(t, app)
				rect, ok := app.screenRectOf(statusoverview.HeaderLinkID)
				if !ok || rect.Empty() || rect.Y <= app.headerScreen.Bounds().Y {
					t.Fatalf("link is not mounted in the panel: %+v", rect)
				}
				_, cmd := app.Update(tea.MouseClickMsg{X: rect.X + 1, Y: rect.Y, Button: tea.MouseLeft})
				runAppCommand(t, app, cmd)
				if app.AgentView.ActiveID() != "workspace-agent" {
					t.Fatal("panel link did not open the existing named Aeron")
				}
				app.restoreAgent("main")
				if app.Input.Value() != "private draft" {
					t.Fatal("opening Aeron lost the original session draft", app.Input.Value())
				}
			})
		}
	}
}
