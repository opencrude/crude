package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core/shellport"
	"crude/core/shellport/remote"
	"crude/internal/buildinfo"
	"crude/internal/metrics"
	"crude/internal/perfdata"
	"crude/tui/perf"
	"crude/tui/rendering"
	"crude/tui/screens/perfdash"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/termdock"
)

func TestPerformanceSaveReportThroughDashboard(t *testing.T) {
	home := t.TempDir()
	t.Setenv(buildinfo.HomeEnv, home)
	app, _ := newRealShellApp(t)
	paneJourneySize(app, 160, 46)
	sink := make(markdownSink, 64)
	app.eventBus.SetSink(sink)
	copied := make(chan string, 1)
	t.Cleanup(installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		writeText: func(text string) error { copied <- text; return nil },
	}))
	panel := app.perfDashboard()
	app.toggleWorkspacePanel(termdock.PanelPerformance)
	panel.SetData(perfdash.Data{Shell: metrics.Take(metrics.Query{Seconds: 60})})
	app.forceFrame()
	_ = app.View()
	r, ok := app.rootHost.ScreenRect(perfdash.SaveID)
	if !ok || r.Empty() {
		t.Fatal("Save report did not mount on the dashboard")
	}
	_, press := app.Update(tea.MouseClickMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	_, release := app.Update(tea.MouseReleaseMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	runAppCommand(t, app, tea.Batch(press, release))
	matches, err := filepath.Glob(filepath.Join(perfdata.ReportsDir(home), "*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("Save report did not produce exactly one file: %v, %v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil || !strings.Contains(string(data), "## Current window") || !strings.Contains(string(data), "## Stalls") {
		t.Fatalf("saved report is missing the live window or stall timeline: %v", err)
	}
	select {
	case got := <-copied:
		if got != matches[0] {
			t.Fatalf("copied %q, want %q", got, matches[0])
		}
	default:
		t.Fatal("the report path was not copied")
	}
	_ = app.View()
	md := app.activeMarkdown()
	if md == nil {
		t.Fatal("the saved report did not open as a Markdown tab")
	}
	pumpMarkdown(t, app, sink, func() bool { return md.Document() != nil })
	if text := paneJourneyText(app, app.mainScreenRect(md.PreviewRect())); !strings.Contains(text, "Crude performance report") {
		t.Fatalf("the opened report did not render its title: %q", text)
	}
}

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
	complete := func(r *metrics.Resources) bool {
		if r == nil || !r.TreeComplete || r.ProcessesOmitted != 0 || len(r.Processes) == 0 {
			return false
		}
		for _, p := range r.Processes {
			if p.CreatedMS <= 0 {
				return false
			}
		}
		return true
	}
	merged := first.data.Shell.HostIdentity == "boot_id" && core.HostIdentity == "boot_id" &&
		first.data.Shell.HostBootID != "" && first.data.Shell.HostBootID == core.HostBootID &&
		complete(first.data.Shell.Resources) && complete(core.Resources)
	views := perfdata.ResourceViews(perfdata.Data(first.data))
	if merged {
		if len(views) != 1 || views[0].Label != "Crude" {
			t.Fatalf("qualified same-host resources not merged: %+v", views)
		}
		seen := map[[2]int64]bool{}
		var memory int64
		for _, p := range views[0].Processes {
			key := [2]int64{int64(p.PID), p.CreatedMS}
			if seen[key] {
				t.Fatalf("merged resources double-count process %v", key)
			}
			seen[key] = true
			if p.Memory >= 0 {
				memory += p.Memory
			}
		}
		for _, resources := range []*metrics.Resources{first.data.Shell.Resources, core.Resources} {
			for _, p := range resources.Processes {
				if !seen[[2]int64{int64(p.PID), p.CreatedMS}] {
					t.Fatalf("merged resources omitted process pid=%d creation=%d", p.PID, p.CreatedMS)
				}
			}
		}
		if views[0].Memory != memory {
			t.Fatalf("merged memory %d differs from deduplicated process total %d", views[0].Memory, memory)
		}
		if text := paint("Crude tree memory", "Processes"); strings.Contains(text, "Shell tree memory") || strings.Contains(text, "Core tree memory") {
			t.Fatal("merged resource frame also displays split owners")
		}
	} else {
		if len(views) != 2 || views[0].Label != "Shell" || views[1].Label != "Core" {
			t.Fatalf("unqualified resource trees must stay split: %+v", views)
		}
		if text := paint("Shell tree memory", "Core tree memory", "Processes"); strings.Contains(text, "Crude tree memory") {
			t.Fatal("unqualified resources displayed a combined total")
		}
	}

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
	if !strings.Contains(app.perf.screen.Report(), "Core metrics unavailable") || strings.Contains(app.perf.screen.Report(), "- Core process (pid ") {
		t.Fatal("failed core phase retained a successful core snapshot or hid its failure")
	}
	if app.ActiveSession != nil {
		t.Fatal("application Performance journey created a session")
	}
}

// TestPerformanceSaveReportNativeCapture drives the production App and loop
// observer in the dedicated tuishot window, using an isolated Crude home.
// F6 supplies a known slow Update, F7 opens Performance, F8 clicks Save
// report at its actual mounted bounds, and F9 closes the test shell.
func TestPerformanceSaveReportNativeCapture(t *testing.T) {
	proof := os.Getenv("CRUDE_VISUAL_PERFORMANCE")
	if proof == "" {
		t.Skip("requires the native tuishot workflow")
	}
	home := t.TempDir()
	t.Setenv(buildinfo.HomeEnv, home)
	app, _ := newRealShellApp(t)
	app.AddOrchestrator("performance-capture")
	metrics.Start("shell")
	v := &performanceCapture{App: app}
	p := tea.NewProgram(v, tea.WithObserver(perf.NewLoopObserver()), tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	defer app.SetProgram(nil)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
	if v.saveErr != nil || v.saved == "" {
		t.Fatalf("the dashboard did not save a report: %v", v.saveErr)
	}
	data, err := os.ReadFile(v.saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Build: " + buildinfo.ExecutableID(), "## Stall evidence", "Watchdog wake-up delay", "Nearby sample:", "Process CPU", "Host CPU", "Longest Go scheduling wait", "GC assist CPU time", "holdPerformanceCapture"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("report is missing %q", want)
		}
	}
	if strings.Contains(string(data), "PRIVATE-CAPTURE-CONTENT") {
		t.Fatal("report contains private text")
	}
	if err := os.WriteFile(proof, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type performanceCapture struct {
	*App
	saved   string
	saveErr error
}

func (v *performanceCapture) Init() tea.Cmd { return tickCmd() }

//go:noinline
func holdPerformanceCapture() { time.Sleep(150 * time.Millisecond) }

func (v *performanceCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if saved, ok := msg.(perfReportSavedMsg); ok {
		v.saved, v.saveErr = saved.path, saved.err
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF6:
			private := metrics.NewLatency(metrics.Meta{Name: "capture.fixture.files", Group: metrics.GroupFiles, Title: "File read", Stall: 10})
			private.ObserveLabeled(time.Millisecond, "PRIVATE-CAPTURE-CONTENT")
			holdPerformanceCapture()
			return v, nil
		case tea.KeyF7:
			_, cmd := v.App.Update(shellfooter.ActionMsg{Action: "performance"})
			return v, cmd
		case tea.KeyF8:
			r, mounted := v.rootHost.ScreenRect(perfdash.SaveID)
			if !mounted || r.Empty() {
				v.saveErr = fmt.Errorf("the Save report control did not mount")
				return v, nil
			}
			_, press := v.App.Update(tea.MouseClickMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
			_, release := v.App.Update(tea.MouseReleaseMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
			return v, tea.Batch(press, release)
		case tea.KeyF9:
			return v, tea.Quit
		}
	}
	_, cmd := v.App.Update(msg)
	return v, cmd
}
