package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/screens/perfdash"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
)

func TestPerformanceSlashCommandIsRemoved(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.AddOrchestrator("root")
	if cmd := app.executeSlashCommand("/perf"); cmd != nil {
		t.Fatal("/perf still schedules the application dashboard")
	}
	if app.perf.screen != nil || app.panelShown(termdock.PanelPerformance) {
		t.Fatal("/perf still opens Performance from the session")
	}
	if got := testStatusText(app); !strings.Contains(got, "Unknown command: /perf") {
		t.Fatalf("removed command was not rejected: %q", got)
	}
}

func TestPerformanceFooterKeepsIdleAndAttentionEntry(t *testing.T) {
	app := newFullTestApp(t)
	for _, test := range []struct {
		severity perfdash.Severity
		text     string
		role     ui.SemanticColor
	}{
		{perfdash.SeverityOK, "Performance", ui.ColorMuted},
		{perfdash.SeverityWarn, "⚠ Performance", ui.ColorWarning},
		{perfdash.SeverityCrit, "✕ Performance", ui.ColorError},
		{perfdash.SeverityOK, "Performance", ui.ColorMuted},
	} {
		app.perf.attention = test.severity
		count := 0
		for _, segment := range app.footerStatusSegments() {
			if segment.Action != "performance" {
				continue
			}
			count++
			if segment.Text != test.text || segment.FillRole != test.role || segment.Attention != (test.severity != perfdash.SeverityOK) {
				t.Fatalf("Performance footer severity = %+v, want %+v", segment, test)
			}
		}
		if count != 1 || app.perf.screen != nil {
			t.Fatal("Performance must have one permanent footer entry without eagerly creating its screen")
		}
	}
}

func TestPerformanceFooterToggleWithoutSession(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.width, app.height = size[0], size[1]
			app.ActiveSession = nil
			app.Input.SetInput("keep this draft")
			app.paintShellFrame()
			if app.perf.screen != nil {
				t.Fatal("painting an idle footer eagerly created the dashboard")
			}
			footer := rowText(app, app.height-1)
			index := strings.Index(footer, "Performance")
			if index < 0 {
				t.Fatalf("idle app has no Performance button: %q", rowText(app, app.height-1))
			}
			x := rendering.VisibleWidth(footer[:index])
			_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: app.height - 1, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			app.paintShellFrame()
			screen := app.perf.screen
			if screen == nil || app.termDock.Panel(termdock.PanelPerformance) != screen || !app.panelShown(termdock.PanelPerformance) || !app.termDockFocused {
				t.Fatal("footer did not open the app-owned Performance dock")
			}
			if r, mounted := app.rootHost.ScreenRect(perfdash.BodyID); !mounted || r.Empty() {
				t.Fatal("Performance body is not rendered in the app shell")
			}
			if app.perf.inFlight {
				t.Fatal("footer snapshot did not settle through App.Update")
			}
			footer = rowText(app, app.height-1)
			index = strings.Index(footer, "Performance")
			if index < 0 {
				t.Fatal("open Performance lost its footer control")
			}
			x = rendering.VisibleWidth(footer[:index])
			_, cmd = app.Update(tea.MouseClickMsg{X: x, Y: app.height - 1, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			app.paintShellFrame()
			if app.panelShown(termdock.PanelPerformance) || app.perf.screen != screen || app.requestPerfSnapshot(false) != nil {
				t.Fatal("second footer click did not hide and retain the dashboard without refresh work")
			}
			if !strings.Contains(rowText(app, app.height-1), "Performance") {
				t.Fatal("hidden dashboard lost its permanent bottom-bar entry")
			}
			if app.ActiveSession != nil || app.AgentView.ActiveID() != "" || app.Input.Value() != "keep this draft" {
				t.Fatal("application utility created a session or consumed the composer draft")
			}
		})
	}
}

func TestFooterMoreOpensPerformanceWithoutSession(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 48, 24
	app.ActiveSession = nil
	app.paintShellFrame()
	footer := rowText(app, app.height-1)
	if strings.Contains(footer, "Performance") {
		t.Fatalf("fixture must overflow the Performance control: %q", footer)
	}
	x := strings.Index(footer, "More")
	if x < 0 {
		t.Fatalf("narrow footer lost More: %q", footer)
	}
	x = rendering.VisibleWidth(footer[:x])
	_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: app.height - 1, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	for _, label := range []string{"Problems · pending", "Notices", "Terminals", "Git", "Board", "Memory", "Performance", "Workspace"} {
		if item, found := contextMenuItem(app.LastContextMenu, label); !found || item.Disabled || item.Action == nil {
			t.Fatalf("footer More lost %s", label)
		}
	}
	results := make(chan tea.Msg, 1)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { results <- msg })
	defer restore()
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	for i := 0; i < 6; i++ {
		_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	select {
	case msg := <-results:
		_, cmd = app.Update(msg)
		runAppCommand(t, app, cmd)
	case <-time.After(3 * time.Second):
		t.Fatal("More did not deliver its footer action")
	}
	app.paintShellFrame()
	if !app.panelShown(termdock.PanelPerformance) || app.perf.inFlight || app.ActiveSession != nil {
		t.Fatal("More did not open and refresh the app-owned Performance surface")
	}
	if !strings.Contains(rowText(app, app.height-1), "Performance") {
		t.Fatal("active Performance control did not survive narrow footer overflow")
	}
}
