package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/shellport/remote"
	"crude/tui/rendering"
	"crude/tui/screens/perfdash"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/termdock"
)

func TestConnectedPerformanceSnapshotLifecycle(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	app := shell.app
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return len(shell.fifo) == 0 })
	app.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	if _, ok := app.ws.(*remote.Workspace); !ok {
		t.Fatal("fixture must use the framed remote workspace, not a fake MetricsSource")
	}
	if _, ok := app.ws.(shellport.MetricsSource); !ok {
		t.Fatal("connected workspace has no core metrics source")
	}

	paint := func(want ...string) string {
		t.Helper()
		text := ansi.Strip(app.View().RenderContent())
		for _, marker := range want {
			if !strings.Contains(text, marker) {
				t.Fatalf("Performance frame missing %q:\n%s", marker, text)
			}
		}
		return text
	}
	footer := func() tea.Cmd {
		t.Helper()
		_, cmd := app.Update(shellfooter.ActionMsg{Action: "performance"})
		return cmd
	}
	snapshot := func(cmd tea.Cmd) perfSnapshotMsg {
		t.Helper()
		if cmd == nil {
			t.Fatal("visible dashboard did not schedule its snapshot")
		}
		// Execute the actual remote CoreMetrics command, withholding only its
		// Update delivery when exercising late completion boundaries below.
		msg, ok := cmd().(perfSnapshotMsg)
		if !ok {
			t.Fatal("Performance command did not return its owned snapshot")
		}
		return msg
	}
	assertIdle := func(reason string) {
		t.Helper()
		generation, requestedAt := app.perf.generation, app.perf.requestedAt
		for _, force := range []bool{false, true} {
			if cmd := app.requestPerfSnapshot(force); cmd != nil {
				t.Fatalf("%s dashboard scheduled an RPC command (force=%v)", reason, force)
			}
		}
		if app.perf.generation != generation || app.perf.requestedAt != requestedAt || app.perf.inFlight {
			t.Fatalf("%s dashboard changed refresh ownership", reason)
		}
	}
	clickPause := func() tea.Cmd {
		t.Helper()
		paint()
		r, ok := app.rootHost.ScreenRect(perfdash.PauseID)
		if !ok || r.Empty() {
			t.Fatal("Performance Pause/Resume button is not mounted")
		}
		_, cmd := app.Update(tea.MouseClickMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
		return cmd
	}
	selectSection := func(label, section string) {
		t.Helper()
		paint()
		r, ok := app.rootHost.ScreenRect(perfdash.TabsID)
		if !ok || r.Empty() {
			t.Fatal("Performance tabs are not mounted")
		}
		row := rowText(app, r.Y)
		index := strings.Index(row, label)
		if index < 0 {
			t.Fatalf("Performance tab %q is missing: %s", label, row)
		}
		_, cmd := app.Update(tea.MouseClickMsg{X: rendering.VisibleWidth(row[:index]), Y: r.Y, Button: tea.MouseLeft})
		runAppCommand(t, app, cmd)
		if app.perf.screen.Section() != section {
			t.Fatalf("clicking %s did not select the Performance section", label)
		}
	}

	paint("Performance")
	if app.perf.screen != nil {
		t.Fatal("idle footer eagerly sampled Performance")
	}
	first := snapshot(footer())
	screen := app.perf.screen
	if screen == nil || !app.panelShown(termdock.PanelPerformance) || !app.perf.inFlight {
		t.Fatal("footer action did not open and start Performance")
	}
	if screen.Report() != "Crude performance: no measurements yet." {
		t.Fatal("background command installed data before Update")
	}
	if first.data.CoreErr != "" || first.data.Core == nil {
		t.Fatalf("framed core.metrics failed: %q", first.data.CoreErr)
	}
	core := first.data.Core
	if core.PID != os.Getpid() || core.Taken.IsZero() || core.Step < 1 || len(core.Latencies) == 0 {
		t.Fatalf("core.metrics did not return real process history: pid=%d step=%d", core.PID, core.Step)
	}
	foundActor := false
	for _, series := range core.Latencies {
		if series.Meta.Name == "core.actor" && len(series.Points) > 0 {
			foundActor = true
		}
	}
	if !foundActor || core.Resources == nil || len(core.Resources.Processes) == 0 {
		t.Fatal("core snapshot lost actor history or OS resource data on the wire")
	}
	app.Update(first)
	if app.perf.inFlight || !strings.Contains(screen.Report(), fmt.Sprintf("Core process (pid %d)", os.Getpid())) {
		t.Fatal("Update did not install the separate core snapshot")
	}
	paint("Overview", "Memory", "CPU")
	selectSection("Core", perfdash.SectionCore)
	paint("Workspace actor step", "Actor time by work")
	selectSection("Resources", perfdash.SectionResources)
	paint("Crude memory", "Processes", "Core")

	if cmd := footer(); cmd != nil {
		t.Fatal("hiding Performance scheduled a snapshot")
	}
	assertIdle("hidden")
	paint("Performance")
	if app.panelShown(termdock.PanelPerformance) {
		t.Fatal("footer action did not hide Performance")
	}
	if r, mounted := app.rootHost.ScreenRect(perfdash.BodyID); mounted && !r.Empty() {
		t.Fatal("hidden dashboard still paints its body")
	}
	runAppCommand(t, app, footer())
	if cmd := clickPause(); cmd != nil {
		t.Fatal("pausing scheduled a refresh")
	}
	if !screen.Paused() {
		t.Fatal("rendered Pause button did not freeze Performance")
	}
	assertIdle("paused")
	frozen := screen.Report()
	paint("Resume")
	if screen.Report() != frozen {
		t.Fatal("painting paused dashboard changed its frozen data")
	}
	runAppCommand(t, app, clickPause())
	if screen.Paused() || app.perf.inFlight {
		t.Fatal("Resume did not refresh and settle through Update")
	}

	old := snapshot(app.requestPerfSnapshot(true))
	// Distinct diagnostic ink makes a forbidden stale install observable even
	// when the two real snapshots finish within the same displayed second.
	old.data.CoreErr = "stale metrics result must not be displayed"
	oldOwner, oldWorkspaceGen := app.ws, app.workspaceSwitchGen.Load()
	next := t.TempDir()
	runAppCommand(t, app, confirmWorkspaceSwitchTest(t, app, next))
	if app.ws == oldOwner || app.ws.RootPath() != next || app.workspaceSwitchGen.Load() == oldWorkspaceGen {
		t.Fatal("real workspace switch did not replace the metrics owner and generation")
	}
	if !app.panelShown(termdock.PanelPerformance) {
		runAppCommand(t, app, footer())
	}
	replacement := snapshot(app.requestPerfSnapshot(true))
	if replacement.data.Core == nil || replacement.data.CoreErr != "" {
		t.Fatalf("replacement connection failed core.metrics: %q", replacement.data.CoreErr)
	}
	before := app.perf.screen.Report()
	app.Update(old)
	if !app.perf.inFlight || app.perf.screen.Report() != before {
		t.Fatal("old generation completed the replacement's in-flight slot or overwrote its data")
	}
	app.Update(replacement)
	if app.perf.inFlight {
		t.Fatal("replacement snapshot did not settle")
	}
	before = app.perf.screen.Report()
	// Isolate each fence so a generation check cannot mask a broken owner or
	// workspace-generation check. The payload remains the real framed result.
	for _, stale := range []perfSnapshotMsg{
		{owner: oldOwner, generation: replacement.generation, workspaceGen: replacement.workspaceGen, data: old.data},
		{owner: replacement.owner, generation: replacement.generation, workspaceGen: oldWorkspaceGen, data: old.data},
	} {
		app.Update(stale)
		if app.perf.screen.Report() != before {
			t.Fatal("stale owner/workspace generation overwrote replacement metrics")
		}
	}
	selectSection("Core", perfdash.SectionCore)
	paint("Workspace actor step", "Actor time by work")

	// Close the actual connection/carrier, not a stub MetricsSource. A later
	// refresh must settle visibly as unavailable rather than retain success.
	if err := app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	failed := snapshot(app.requestPerfSnapshot(true))
	if failed.data.Core != nil || failed.data.CoreErr == "" {
		t.Fatal("closed carrier did not fail the actual core.metrics query")
	}
	app.Update(failed)
	if app.perf.inFlight {
		t.Fatal("failed snapshot left Performance refreshing forever")
	}
	selectSection("Overview", perfdash.SectionOverview)
	paint("Core metrics unavailable")
	if !strings.Contains(app.perf.screen.Report(), "Core metrics unavailable") || strings.Contains(app.perf.screen.Report(), "\nCore process (pid ") {
		t.Fatal("failed core phase retained a successful core snapshot or hid its failure")
	}
	if app.ActiveSession != nil {
		t.Fatal("application Performance journey created a session")
	}
}
