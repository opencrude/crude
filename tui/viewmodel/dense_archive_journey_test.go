package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/internal/metrics"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/workspacehub"
	"crude/workspace"
)

// frameSample is one message the UI goroutine handled: its Update and the
// View that follows it, as Bubble Tea's event loop runs them.
type frameSample struct {
	scenario, label string
	update, view    time.Duration
}

// denseJourney drives a real core and a remote shell. Commands run off the
// UI goroutine as Bubble Tea runs them; only Update and View are timed.
type denseJourney struct {
	t        *testing.T
	shell    remoteJourneyShell
	results  chan tea.Msg
	scenario string
	samples  []frameSample
}

func (j *denseJourney) launch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		select {
		case j.results <- msg:
		case <-j.t.Context().Done():
		}
	}()
}

func (j *denseJourney) deliver(msg tea.Msg) {
	switch m := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, cmd := range m {
			j.launch(cmd)
		}
		return
	}
	label := fmt.Sprintf("%T", msg)
	if key, ok := msg.(tea.KeyPressMsg); ok {
		label = "key " + key.String()
	}
	var cmd tea.Cmd
	var start, updated time.Time
	var view time.Duration
	// Profile labels separate each scenario's Update and View in a profile.
	pprof.Do(context.Background(), pprof.Labels("scenario", j.scenario, "stage", "update"), func(context.Context) {
		start = time.Now()
		_, cmd = j.shell.app.Update(msg)
		updated = time.Now()
	})
	pprof.Do(context.Background(), pprof.Labels("scenario", j.scenario, "stage", "view"), func(context.Context) {
		j.shell.app.View()
		view = time.Since(updated)
	})
	j.samples = append(j.samples, frameSample{j.scenario, label, updated.Sub(start), view})
	j.launch(cmd)
}

// pump delivers pushed publications and command results until done reports
// true and the shell has been quiet for settle, or the timeout passes.
func (j *denseJourney) pump(timeout, settle time.Duration, done func() bool) bool {
	deadline := time.After(timeout)
	quiet := time.NewTimer(settle)
	defer quiet.Stop()
	for {
		select {
		case msg := <-j.shell.fifo:
			j.deliver(msg)
		case msg := <-j.results:
			j.deliver(msg)
		case <-quiet.C:
			if done == nil || done() {
				return true
			}
			quiet.Reset(settle)
			continue
		case <-deadline:
			return false
		}
		if !quiet.Stop() {
			select {
			case <-quiet.C:
			default:
			}
		}
		quiet.Reset(settle)
	}
}

func (j *denseJourney) key(code rune, mod tea.KeyMod) {
	text := ""
	if code >= ' ' && code < 0x7f && mod == 0 {
		text = string(code)
	}
	j.deliver(tea.KeyPressMsg{Code: code, Text: text, Mod: mod})
	j.pump(2*time.Second, 5*time.Millisecond, nil)
}

func (j *denseJourney) resize(w, h int) {
	j.deliver(tea.WindowSizeMsg{Width: w, Height: h})
	j.pump(2*time.Second, 5*time.Millisecond, nil)
}

func (j *denseJourney) report() (worst frameSample) {
	j.t.Helper()
	byScenario := map[string][]frameSample{}
	var order []string
	for _, sample := range j.samples {
		if _, ok := byScenario[sample.scenario]; !ok {
			order = append(order, sample.scenario)
		}
		byScenario[sample.scenario] = append(byScenario[sample.scenario], sample)
	}
	for _, scenario := range order {
		samples := byScenario[scenario]
		frames := make([]time.Duration, len(samples))
		var total time.Duration
		var slowest frameSample
		over := 0
		for i, sample := range samples {
			frames[i] = sample.update + sample.view
			total += frames[i]
			if frames[i] > slowest.update+slowest.view {
				slowest = sample
			}
			if frames[i] > 16*time.Millisecond {
				over++
			}
		}
		slices.Sort(frames)
		p95 := frames[len(frames)*95/100]
		j.t.Logf("%-28s n=%4d total=%8s p95=%7s over16ms=%3d worst=%s (update %s, view %s)",
			scenario, len(samples), total.Round(time.Millisecond), p95.Round(100*time.Microsecond), over,
			slowest.label, slowest.update.Round(100*time.Microsecond), slowest.view.Round(100*time.Microsecond))
		if slowest.update+slowest.view > worst.update+worst.view {
			worst = slowest
		}
	}
	return worst
}

// TestDenseArchiveJourney opens copies of real session databases through a
// real core and remote shell, then walks the interactions that touch dense
// history: open, idle frames, scrolling the whole transcript, resizing, find,
// the Workspace panel pages, switching between sessions and reopening. It
// reports each scenario's frame costs and fails when one UI-goroutine frame
// exceeds CRUDE_TEST_FRAME_BUDGET (default 100ms).
//
// Opt-in: set CRUDE_TEST_ARCHIVES to one or more session databases (path
// list). CRUDE_TEST_PROFILE writes a CPU profile of the UI goroutine work.
func TestDenseArchiveJourney(t *testing.T) {
	archives := filepath.SplitList(os.Getenv("CRUDE_TEST_ARCHIVES"))
	if len(archives) == 0 {
		t.Skip("set CRUDE_TEST_ARCHIVES to session databases")
	}
	budget := 100 * time.Millisecond
	if value := os.Getenv("CRUDE_TEST_FRAME_BUDGET"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			t.Fatal(err)
		}
		budget = parsed
	}
	if path := os.Getenv("CRUDE_TEST_PROFILE"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			t.Fatal(err)
		}
		defer func() { pprof.StopCPUProfile(); _ = file.Close() }()
	}
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	var ids []string
	for _, archive := range archives {
		id := strings.TrimSuffix(filepath.Base(archive), ".db")
		target, err := workspace.SessionDBPath(root, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		copyArchiveFile(t, archive, target)
		ids = append(ids, id)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	j := &denseJourney{t: t, shell: shell, results: make(chan tea.Msg, 4096)}
	app := shell.app
	// The test goroutine runs Update and View, as Bubble Tea's loop does: a
	// core request or disk read made there is reported as a blocking call.
	metrics.ResetUIGoroutineForTest()
	metrics.MarkUIGoroutine()
	t.Cleanup(metrics.ResetUIGoroutineForTest)
	since := time.Now()
	j.scenario = "attach"
	j.resize(160, 45)

	agents := map[string]string{}
	open := func(id string) {
		j.scenario = "open " + id[len(id)-8:]
		start := time.Now()
		j.launch(app.launcherResumeSession(id, nil))
		ok := j.pump(60*time.Second, 300*time.Millisecond, func() bool {
			active := app.AgentView.ActiveID()
			return active != "" && app.ActiveSession != nil && app.ActiveSession.SessionID == id &&
				app.ws.AgentMgr().Get(active) != nil && len(app.ws.AgentMgr().Get(active).State.Messages.Messages()) > 0
		})
		if !ok {
			t.Fatalf("session %s did not open", id)
		}
		agents[id] = app.AgentView.ActiveID()
		t.Logf("open %s: %s until settled, %d rows", id, time.Since(start).Round(time.Millisecond), len(app.ws.AgentMgr().Get(agents[id]).State.Messages.Messages()))
	}
	open(ids[0])
	j.scenario = "idle"
	j.pump(time.Second, time.Second, nil)

	j.scenario = "scroll to top"
	for range 400 {
		j.key(tea.KeyPgUp, 0)
	}
	j.scenario = "scroll to bottom"
	for range 400 {
		j.key(tea.KeyPgDown, 0)
	}
	j.scenario = "wheel"
	for range 60 {
		j.deliver(tea.MouseWheelMsg{X: 60, Y: 20, Button: tea.MouseWheelUp})
	}
	j.pump(time.Second, 20*time.Millisecond, nil)

	j.scenario = "resize"
	for _, size := range [][2]int{{90, 28}, {60, 20}, {200, 60}, {120, 40}, {160, 45}} {
		j.resize(size[0], size[1])
	}

	j.scenario = "find"
	j.key('f', tea.ModCtrl)
	for _, r := range "the" {
		j.key(r, 0)
	}
	for range 5 {
		j.key(tea.KeyEnter, 0)
	}
	j.key(tea.KeyEscape, 0)

	for _, tab := range workspacehub.Tabs {
		j.scenario = "workspace " + string(tab)
		j.launch(app.openWorkspaceHub(tab))
		j.pump(5*time.Second, 200*time.Millisecond, nil)
		for range 20 {
			j.key(tea.KeyDown, 0)
		}
	}
	j.scenario = "workspace close"
	j.key(tea.KeyEscape, 0)
	j.key(tea.KeyEscape, 0)

	for _, id := range ids[1:] {
		open(id)
	}
	if len(ids) > 1 {
		j.scenario = "switch tabs"
		for range 6 {
			for _, id := range ids {
				start := time.Now()
				app.ActivateSessionByAgent(agents[id])
				app.View()
				j.samples = append(j.samples, frameSample{j.scenario, "activate " + id[len(id)-8:], time.Since(start), 0})
				j.pump(2*time.Second, 50*time.Millisecond, nil)
			}
		}
	}
	// The Workspace panel's lists keep their row while the open sessions
	// publish updates: the selection must not return to the first row.
	for _, tab := range []workspacehub.Tab{workspacehub.TabTabs, workspacehub.TabSessions} {
		j.scenario = "workspace " + string(tab) + " live"
		j.launch(app.openWorkspaceHub(tab))
		j.pump(5*time.Second, 200*time.Millisecond, nil)
		j.key(tea.KeyEnd, 0)
		page, _ := app.hub.Page(tab).(*statusoverview.Surface)
		before, _ := page.Selected()
		for range 4 {
			j.pump(1500*time.Millisecond, 1500*time.Millisecond, nil)
			if after, _ := page.Selected(); after.ID != before.ID {
				t.Errorf("Workspace %s moved its selection from %q to %q during updates", tab, before.ID, after.ID)
				break
			}
		}
	}
	worst := j.report()
	for _, event := range metrics.EventsSince(since) {
		if event.Series == "ui.blocking" {
			t.Errorf("%s %s held the UI goroutine for %dµs", event.Title, event.Label, event.Micros)
		}
	}
	if frame := worst.update + worst.view; frame > budget {
		t.Errorf("UI goroutine froze for %s in %q on %s (update %s, view %s); budget %s",
			frame.Round(time.Millisecond), worst.scenario, worst.label, worst.update.Round(time.Millisecond), worst.view.Round(time.Millisecond), budget)
	}
}

func copyArchiveFile(t *testing.T, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal"} {
		source, err := os.Open(from + suffix)
		if err != nil {
			if suffix != "" && os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		target, err := os.Create(to + suffix)
		if err != nil {
			_ = source.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(target, source)
		_ = source.Close()
		if closeErr := target.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
