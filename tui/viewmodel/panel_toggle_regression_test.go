package viewmodel

import (
	"testing"

	"crude/tui/screens/termdock"
)

func TestBottomPanelToggleDoesNotDependOnFocus(t *testing.T) {
	for _, id := range []string{
		termdock.PanelWorkspace, termdock.PanelProblems, termdock.PanelNotices,
		termdock.PanelGit, termdock.PanelMemory, termdock.PanelBoard, termdock.PanelPerformance,
	} {
		t.Run(id, func(t *testing.T) {
			app := newFullTestApp(t)
			toggle := func() {
				switch id {
				case termdock.PanelGit:
					app.toggleGitPanel()
				case termdock.PanelMemory:
					app.toggleMemoryBrowser()
				case termdock.PanelBoard:
					app.toggleBoardBrowser()
				case termdock.PanelPerformance:
					app.togglePerformancePanel()
				default:
					app.toggleWorkspacePanel(id)
				}
			}
			toggle()
			if !app.panelShown(id) {
				t.Fatal("first activation did not show the panel")
			}
			app.setTermDockFocused(false)
			toggle()
			if app.panelShown(id) {
				t.Fatal("repeat activation focused the panel instead of hiding it")
			}
			toggle()
			if !app.panelShown(id) {
				t.Fatal("third activation did not reopen the panel")
			}
		})
	}
}
