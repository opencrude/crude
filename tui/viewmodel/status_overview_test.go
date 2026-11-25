package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/internal/providersim"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/types/statuscenter"
)

func TestConnectedCacheHeaderAndUsageDistinguishMissFromNoReport(t *testing.T) {
	server := simulatedChat(t,
		providersim.Text("Cache check 1").WithUsage(providersim.Usage{Input: 4096, Output: 1, Cached: 4000, CacheWrite: 50}),
		providersim.Text("Cache check 2").WithUsage(providersim.Usage{Input: 1024, Output: 1}),
		providersim.Text("Cache check 3").WithUsage(providersim.Usage{Input: 1024, Output: 1, OmitCacheRead: true}),
	)
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, shell)
	app := shell.app

	submit := func(call int) {
		t.Helper()
		app.Input.SetInput(fmt.Sprintf("Check cache %d", call))
		app.setFocusInput()
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, app, cmd)
		remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
			usage, _ := app.ws.UsageSummary(id, statuscenter.UsageThisAgent)
			return usage.Requests == call && !app.ws.AgentTurnActive(id)
		})
	}
	assertHeader := func(app *App, want string, absent ...string) {
		t.Helper()
		app.paintShellFrame()
		header := rowText(app, 0)
		if !strings.Contains(header, want) {
			t.Fatalf("header = %q; want %q", header, want)
		}
		for _, text := range absent {
			if strings.Contains(header, text) {
				t.Fatalf("header = %q; must not report %q", header, text)
			}
		}
	}
	assertDetail := func(app *App, label, want string) {
		t.Helper()
		for _, item := range app.statusTabItems() {
			if item.ID != "agent:"+id || len(item.Usage) == 0 {
				continue
			}
			for _, line := range item.Usage[0].Lines {
				if line.Label == label && line.Value == want {
					return
				}
			}
			t.Fatalf("agent usage lacks %q = %q: %+v", label, want, item.Usage)
		}
		t.Fatal("agent usage is absent")
	}

	submit(1)
	assertHeader(app, "cache 97.7%")
	submit(2)
	assertHeader(app, "last cache 0.0%", "78.1%")
	assertDetail(app, "Not from cache", "1.1k")
	assertDetail(app, "Cache read", "4.0k")
	assertDetail(app, "Cache write", "Not reported")
	submit(3)
	assertHeader(app, "last cache not available", "0.0%", "65.1%")
	assertDetail(app, "Cache read", "Not reported")
	assertDetail(app, "Not from cache", "Not reported")

	// A second shell receives the retained usage from the core, not the first
	// shell's stopped stream or private UI state. Open the actual disclosure.
	viewer := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{shell, viewer}, func() bool {
		return viewer.app.AgentView.ActiveID() == id
	})
	assertHeader(viewer.app, "last cache not available", "0.0%")
	viewer.app.toggleWorkspacePanel(termdock.PanelWorkspace)
	viewer.app.setTermDockHeight(29)
	viewer.app.paintShellFrame()
	screen := viewer.app.termDock.Panel(termdock.PanelWorkspace).(*statusoverview.Surface)
	list, dock := screen.ListBounds(), viewer.app.termDockRect
	for y := dock.Y + 1 + list.Y; y < dock.Y+1+list.Y+4 && !screen.IsExpanded("agent:"+id); y++ {
		viewer.app.rootHost.Handle(tea.MouseClickMsg{X: dock.X + list.X + 4, Y: y, Button: tea.MouseLeft})
	}
	if !screen.IsExpanded("agent:" + id) {
		t.Fatal("the agent usage disclosure did not open")
	}
	viewer.app.paintShellFrame()
	for y := viewer.app.termDockRect.Y; y < viewer.app.height; y++ {
		row := rowText(viewer.app, y)
		if strings.Contains(row, "Not from cache") && strings.Contains(row, "Not reported") {
			return
		}
	}
	t.Fatal("the expanded panel did not paint the unavailable cache count")
}
