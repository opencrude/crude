package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/insight/facts"
	"crude/sysload"
	model "crude/types"
)

func countCalls(calls []string, prefix string) int {
	n := 0
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

// Checking a closed file must not cost the files that are open: the server
// sees nothing new, so open files are not sent again and keep their results.
// The checked file keeps its result after the view closes, as a project
// result that Problems counts.
func TestDiskViewLeavesOpenFilesAloneAndKeepsItsResult(t *testing.T) {
	m, r, root := probeManager(t)
	open := filepath.Join(root, "open.go")
	source := editorSource(t, m, open, "package main\n")
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	client := r.snapshot()[0]
	waitDiagnosticSend(t, m, client, source)
	client.emit(client.publication(open, []LSPDiagnostic{{Message: "open problem", Severity: model.DiagError}}))
	if batch, ok := m.FileDiagnostics(open); !ok || !batch.HasExactSource() {
		t.Fatalf("open file result = %+v, present=%v", batch, ok)
	}
	before := client.snapshot()

	closed := filepath.Join(root, "closed.go")
	createFile(t, closed, "package main\n")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, settled, _, err := m.ProbeDiagnostics(ctx, closed); err != nil || !settled {
		t.Fatalf("probe settled=%v err=%v", settled, err)
	}
	awaitLSP(t, func() bool { return slices.Contains(client.snapshot(), "close:closed.go") })

	after := client.snapshot()[len(before):]
	if n := countCalls(after, "change:open.go"); n != 0 {
		t.Fatalf("checking a closed file sent the open file again %d times: %v", n, after)
	}
	if batch, _ := m.FileDiagnostics(open); !batch.HasExactSource() || batch.State == model.InsightStale {
		t.Fatalf("checking a closed file made the open file's result out of date: %+v", batch)
	}
	batch, ok := m.FileDiagnostics(closed)
	if !ok || !batch.FromProjectCheck() || len(batch.Diagnostics) != 1 {
		t.Fatalf("closed file result after the view closed = %+v, present=%v", batch, ok)
	}
	if summary := m.ProjectInsights(); summary.Errors != 2 || summary.FilesAffected != 2 {
		t.Fatalf("Problems does not count both files: %+v", summary)
	}
}

// The reported failure: a restart (here a settings change) leaves results
// for closed files from the stopped server. The new server reports only
// files that still have problems, so the old rows stayed forever. Once the
// new server has settled, they go away; until then they stay out of date.
func TestRestartedServerRetiresOldClosedFileResults(t *testing.T) {
	m, r, root := probeManager(t)
	open := filepath.Join(root, "open.go")
	editorSource(t, m, open, "package main\n")
	closed := filepath.Join(root, "closed.go")
	createFile(t, closed, "package main\n")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, settled, _, err := m.ProbeDiagnostics(ctx, closed); err != nil || !settled {
		t.Fatalf("probe settled=%v err=%v", settled, err)
	}
	if batch, _ := m.FileDiagnostics(closed); !batch.FromProjectCheck() {
		t.Fatalf("closed file result = %+v", batch)
	}

	settings := m.Settings()
	settings.UpdateDebounceMS++
	m.SetSettings(settings)
	awaitLSP(t, func() bool {
		h := m.ServerHealthSnapshots()
		return len(r.snapshot()) == 2 && len(h) == 1 && h[0].Connected
	})
	if batch, ok := m.FileDiagnostics(closed); !ok || batch.State != model.InsightStale {
		t.Fatalf("before the new server settled the old result must stay out of date: %+v, present=%v", batch, ok)
	}
	deadline := time.Now().Add(lspProjectSettleQuiet + 3*time.Second)
	for {
		if _, ok := m.FileDiagnostics(closed); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stopped server's result for a closed file stayed after the new server settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if summary := m.ProjectInsights(); summary.DiagnosticsStale != 0 {
		t.Fatalf("out-of-date rows remain: %+v", summary)
	}
}

// The project check opens each closed file of a server that does not report
// closed files, one at a time, skips vendored code, keeps every result and
// never sends the open file again. Turning it off stops it. A server that
// reports closed files itself (here the Go server, by catalog) is not asked.
func TestProjectCheckCoversClosedFilesOneAtATime(t *testing.T) {
	m, r, root := probeManager(t)
	open := filepath.Join(root, "open.go")
	source := editorSource(t, m, open, "package main\n")
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	goClient := r.snapshot()[0]
	waitDiagnosticSend(t, m, goClient, source)

	files := []string{filepath.Join(root, "a.py"), filepath.Join(root, "pkg", "b.py")}
	vendored := filepath.Join(root, "third_party", "lib", "c.py")
	project := filepath.Join(root, "closed.go")
	for _, path := range append(slices.Clone(files), vendored, project) {
		mkdir(t, filepath.Dir(path))
		createFile(t, path, "x = 1\n")
	}
	goCalls := len(goClient.snapshot())
	m.mu.Lock()
	m.lastEdit = time.Time{}
	m.discovery = &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{
		"python": {id: "python", name: "Python", paths: append(slices.Clone(files), vendored)},
		"go":     {id: "go", name: "Go", paths: []string{open, project}},
	}}
	m.checkPending = true
	m.signalLocked()
	m.mu.Unlock()

	for _, path := range files {
		awaitProjectFact(t, m, path)
	}
	if _, ok := m.FileDiagnostics(vendored); ok {
		t.Fatal("the project check spent time on vendored code")
	}
	if _, ok := m.FileDiagnostics(project); ok {
		t.Fatal("the project check asked a server that reports the project itself")
	}
	if calls := goClient.snapshot()[goCalls:]; len(calls) != 0 {
		t.Fatalf("the project check sent Go traffic, including the open file: %v", calls)
	}
	clients := r.snapshot()
	if len(clients) != 2 {
		t.Fatalf("servers started: %d", len(clients))
	}
	calls := clients[1].snapshot()
	open1, close1 := slices.Index(calls, "open:a.py:python:x = 1\n"), slices.Index(calls, "close:a.py")
	open2 := slices.Index(calls, "open:b.py:python:x = 1\n")
	if open1 < 0 || close1 < 0 || open2 < 0 || close1 > open2 {
		t.Fatalf("files were not checked one at a time: %v", calls)
	}
	awaitLSP(t, func() bool { return m.ProjectInsights().ProjectCheckTotal == 0 })

	// A changed file is checked again; with the check off it is not.
	settings := m.Settings()
	settings.ProjectCheck = model.ProjectCheckOff
	m.SetSettings(settings)
	m.mu.Lock()
	m.invalidateCheckedFactsLocked(files[0])
	m.mu.Unlock()
	time.Sleep(1500 * time.Millisecond)
	if batch, _ := m.FileDiagnostics(files[0]); batch.State != model.InsightStale {
		t.Fatalf("the check ran while it was off: %+v", batch)
	}
	settings.ProjectCheck = model.ProjectCheckIdle
	m.SetSettings(settings)
	awaitProjectFact(t, m, files[0])
}

func awaitProjectFact(t *testing.T, m *LSPManager, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if batch, ok := m.FileDiagnostics(path); ok && batch.FromProjectCheck() {
			return
		}
		if time.Now().After(deadline) {
			batch, ok := m.FileDiagnostics(path)
			t.Fatalf("%s was not checked: %+v, present=%v", filepath.Base(path), batch, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The reported journey: a trusted project with no open file and no server
// that reports the project. The published assembly plan alone starts the
// check, and Problems gets the closed file's result.
func TestProjectCheckStartsFromAssemblyWithoutOpenFiles(t *testing.T) {
	m, _, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x: int = 'text'\n")
	m.mu.Lock()
	generation := m.assemblyRequested
	m.mu.Unlock()
	m.publishAssembly(t.Context(), &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{
		"python": {id: "python", name: "Python", files: 1, sample: path, paths: []string{path}},
	}}, model.AssemblyPlan{State: model.AssemblyReady, Generation: generation})
	awaitProjectFact(t, m, path)
	if summary := m.ProjectInsights(); summary.Errors != 1 || summary.FilesAffected != 1 {
		t.Fatalf("Problems does not count the closed file: %+v", summary)
	}
}

// Found live with pyright: a server that checks only open files clears the
// file's results when it closes. That empty report must neither erase the
// result Crude kept nor make Crude treat the server as one that reports the
// project, which would stop the project check for its language.
func TestCloseTimeClearingKeepsTheCheckedResult(t *testing.T) {
	m, r, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x: int = 'text'\n")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, settled, _, err := m.ProbeDiagnostics(ctx, path); err != nil || !settled {
		t.Fatalf("probe settled=%v err=%v", settled, err)
	}
	client := r.snapshot()[0]
	awaitLSP(t, func() bool { return slices.Contains(client.snapshot(), "close:main.py") })
	client.emitUnopened(path)
	if batch, ok := m.FileDiagnostics(path); !ok || !batch.FromProjectCheck() || len(batch.Diagnostics) != 1 {
		t.Fatalf("the close-time clearing erased the checked result: %+v, present=%v", batch, ok)
	}
	m.mu.Lock()
	reports := false
	for _, state := range m.states {
		reports = reports || state.reportsProject
	}
	m.mu.Unlock()
	if reports {
		t.Fatal("an empty close-time report marked the server as reporting the project")
	}
}

// The check yields to the computer, not only to typing: on battery power and
// under contention it waits and says why; once the computer is free it goes
// on. Always is the user's override and checks anyway.
func TestProjectCheckYieldsToBatteryAndContention(t *testing.T) {
	m, _, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x: int = 'text'\n")
	var mu sync.Mutex
	load := sysload.Sample{PowerKnown: true, OnBattery: true, CPUPressure: 0, MemoryPressure: 0, IOPressure: 0,
		IdleCores: -1, MemoryKnown: true, AvailableMemory: 8 << 30}
	m.mu.Lock()
	m.loadProbe = func() sysload.Sample { mu.Lock(); defer mu.Unlock(); return load }
	generation := m.assemblyRequested
	m.mu.Unlock()
	m.publishAssembly(t.Context(), &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{
		"python": {id: "python", name: "Python", files: 1, sample: path, paths: []string{path}},
	}}, model.AssemblyPlan{State: model.AssemblyReady, Generation: generation})

	waits := func(reason string) {
		t.Helper()
		awaitLSP(t, func() bool { return m.ProjectInsights().ProjectCheckWait == reason })
		if _, ok := m.FileDiagnostics(path); ok {
			t.Fatalf("the check ran while it should wait %s", reason)
		}
	}
	waits("on battery power")
	mu.Lock()
	load.OnBattery, load.IOPressure = false, 0.5
	mu.Unlock()
	waits("while the computer is busy")
	mu.Lock()
	load.IOPressure, load.AvailableMemory = 0, 64<<20
	mu.Unlock()
	waits("for free memory")

	// The user decides: Always checks on battery, under load and without free
	// memory, as Windows Search does with backoff turned off.
	mu.Lock()
	load.OnBattery, load.IOPressure = true, 0.5
	mu.Unlock()
	settings := m.Settings()
	settings.ProjectCheck = model.ProjectCheckAlways
	m.SetSettings(settings)
	awaitProjectFact(t, m, path)
}

// Found live under a CPU load: the first sample of the computer has no
// interval, so it said nothing and the first file ran at once. An unmeasured
// computer is not an idle one.
func TestProjectCheckWaitsForAFirstMeasurement(t *testing.T) {
	m, _, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x: int = 'text'\n")
	var mu sync.Mutex
	load := sysload.Sample{PowerKnown: true, Warming: true, CPUPressure: -1, MemoryPressure: -1, IOPressure: -1,
		IdleCores: -1, MemoryKnown: true, AvailableMemory: 8 << 30}
	m.mu.Lock()
	m.loadProbe = func() sysload.Sample { mu.Lock(); defer mu.Unlock(); return load }
	generation := m.assemblyRequested
	m.mu.Unlock()
	m.publishAssembly(t.Context(), &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{
		"python": {id: "python", name: "Python", files: 1, sample: path, paths: []string{path}},
	}}, model.AssemblyPlan{State: model.AssemblyReady, Generation: generation})
	awaitLSP(t, func() bool { return m.ProjectInsights().ProjectCheckTotal == 1 })
	time.Sleep(1200 * time.Millisecond)
	if _, ok := m.FileDiagnostics(path); ok {
		t.Fatal("the check ran before it measured the computer")
	}
	mu.Lock()
	load.Warming, load.CPUPressure, load.MemoryPressure, load.IOPressure = false, 0, 0, 0
	mu.Unlock()
	awaitProjectFact(t, m, path)
}

// The reported cycle: with more servers than the limit, the project check
// started one server, which evicted another, and the eviction marked the
// evicted server's results for closed files out of date. The next pass
// checked those files again and evicted the first server, without end.
// Crude's results for closed files describe the files on disk, so a stopped
// server leaves them current and the check ends after one pass.
func TestProjectCheckEndsWhenServersTakeTurns(t *testing.T) {
	m, r, root := probeManager(t)
	settings := m.Settings()
	settings.MaxServers = 1
	m.SetSettings(settings)
	python := []string{filepath.Join(root, "a.py"), filepath.Join(root, "b.py")}
	c := []string{filepath.Join(root, "a.c"), filepath.Join(root, "b.c")}
	for _, path := range append(slices.Clone(python), c...) {
		createFile(t, path, "x\n")
	}
	m.mu.Lock()
	m.lastEdit = time.Time{}
	m.discovery = &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{
		"python": {id: "python", name: "Python", paths: python},
		"c":      {id: "c", name: "C", paths: c},
	}}
	m.checkPending = true
	m.signalLocked()
	m.mu.Unlock()

	all := append(slices.Clone(python), c...)
	for _, path := range all {
		awaitProjectFact(t, m, path)
	}
	awaitLSP(t, func() bool { return m.ProjectInsights().ProjectCheckTotal == 0 })
	started := len(r.snapshot())
	// Retire the final process and wait on its actual removal, not elapsed time.
	m.mu.Lock()
	for _, state := range m.states {
		m.settleProjectReportLocked(state, time.Now().Add(lspProjectSettleQuiet))
		m.retireLocked(state)
	}
	m.signalLocked()
	m.mu.Unlock()
	awaitLSP(t, func() bool { return len(m.ServerHealthSnapshots()) == 0 })
	m.mu.Lock()
	queue := m.projectCheckQueueLocked()
	m.mu.Unlock()
	if len(queue) != 0 {
		t.Fatalf("retiring servers queued already checked files: %+v", queue)
	}
	if now := len(r.snapshot()); now != started {
		t.Fatalf("the project check kept starting servers after it had checked every file: %d, then %d", started, now)
	}
	for _, path := range all {
		if batch, ok := m.FileDiagnostics(path); !ok || batch.State == model.InsightStale || !batch.FromProjectCheck() {
			t.Fatalf("%s lost its result when its server stopped: %+v, present=%v", filepath.Base(path), batch, ok)
		}
	}
	if summary := m.ProjectInsights(); summary.FilesAffected != len(all) || summary.DiagnosticsStale != 0 {
		t.Fatalf("Problems does not show every closed file as current: %+v", summary)
	}
}

// Restart is an explicit request to refresh this server's closed files, unlike
// an idle stop or eviction. Exercise the same entry point as Settings and agents.
func TestRestartServerRechecksClosedFiles(t *testing.T) {
	m, r, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x = 1\n")
	startProjectCheck(t, m, root, map[string][]string{"python": {path}})
	awaitProjectFact(t, m, path)
	awaitProjectCheckEnd(t, m)
	before, _ := m.FileDiagnostics(path)
	health := m.ServerHealthSnapshots()
	if len(health) != 1 || !m.RestartServer(health[0].ID) {
		t.Fatal("could not restart checked server")
	}
	awaitLSP(t, func() bool {
		after, ok := m.FileDiagnostics(path)
		return ok && after.ID != before.ID && after.State != model.InsightStale && len(r.snapshot()) == 2
	})
}

func startProjectCheck(t *testing.T, m *LSPManager, root string, files map[string][]string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discovery = &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{}}
	for id, paths := range files {
		m.discovery.languages[id] = &discoveredLanguage{id: id, paths: paths}
	}
	m.lastEdit = time.Time{}
	m.requestProjectCheckLocked()
}

func awaitProjectCheckEnd(t *testing.T, m *LSPManager) {
	t.Helper()
	awaitLSP(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return !m.checkRunning && !m.checkPending })
}

type silentCheckRuntime struct{ *lifecycleRuntime }
type silentCheckClient struct{ *lifecycleClient }

func (r silentCheckRuntime) NewClient(spec model.LSPServerSpec, root string, admit func() error) LSPClient {
	return &silentCheckClient{r.lifecycleRuntime.NewClient(spec, root, admit).(*lifecycleClient)}
}
func (c *silentCheckClient) AwaitDiagnostics(context.Context, string) (model.InsightDiagnosticPublication, bool) {
	return model.InsightDiagnosticPublication{}, false
}

func TestProjectCheckDoesNotRestartAServerThatGivesNoResult(t *testing.T) {
	m, r, root := probeManager(t)
	m.SetRuntime(silentCheckRuntime{r})
	settings := m.Settings()
	settings.MaxServers = 1
	m.SetSettings(settings)
	paths := []string{filepath.Join(root, "main.py"), filepath.Join(root, "main.c")}
	for _, path := range paths {
		createFile(t, path, "x\n")
	}
	startProjectCheck(t, m, root, map[string][]string{"python": {paths[0]}, "c": {paths[1]}})
	awaitLSP(t, func() bool { return len(r.snapshot()) >= 2 })
	awaitProjectCheckEnd(t, m)
	// Unrelated watch events must not retry silent files or start another pass.
	m.NotifyWatchedFilesChanged(t.Context(), filepath.Join(root, "notes.txt"), 2)
	m.mu.Lock()
	for _, state := range m.states {
		m.settleProjectReportLocked(state, time.Now().Add(lspProjectSettleQuiet))
		m.retireLocked(state)
	}
	queue := m.projectCheckQueueLocked()
	pending := m.checkPending
	m.signalLocked()
	m.mu.Unlock()
	if len(queue) != 0 || pending {
		t.Fatalf("silent files queued again: queue=%+v pending=%v", queue, pending)
	}
	awaitLSP(t, func() bool { return len(m.ServerHealthSnapshots()) == 0 })
	if len(r.snapshot()) != 2 {
		t.Fatalf("silent check kept starting servers: %d", len(r.snapshot()))
	}
}

func TestWatchEventWithoutCheckedResultStartsNoPass(t *testing.T) {
	m, _, root := probeManager(t)
	m.mu.Lock()
	m.checkPending = false
	m.mu.Unlock()
	m.NotifyWatchedFilesChanged(t.Context(), filepath.Join(root, "notes.txt"), 2)
	m.mu.Lock()
	pending := m.checkPending
	m.mu.Unlock()
	if pending {
		t.Fatal("unrelated watch event scheduled a closed-file check")
	}
}

func TestFullFactStoreDoesNotFailTheServer(t *testing.T) {
	m, r, root := probeManager(t)
	store, err := facts.New(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	b := model.InsightBoundary{
		Source:  model.InsightSource{WorkspaceGeneration: "w", DocumentID: "occupied", Path: filepath.Join(root, "old.py"), Lifetime: "s", Revision: 1, Kind: model.InsightSourceDisk},
		Context: model.InsightContext{ID: "k", Root: root, Generation: 1}, Producer: model.InsightProducer{InstanceID: "s", Provider: "checker", ToolIdentity: "tool"},
	}
	if err := store.Observe(b); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sources.Insight.diagnostics = store
	m.mu.Unlock()
	path := filepath.Join(root, "main.py")
	source := editorSource(t, m, path, "x\n")
	awaitLSP(t, func() bool {
		h := m.ServerHealthSnapshots()
		if len(h) == 1 && h[0].Status == "Failed" {
			t.Fatalf("capacity exhaustion failed a healthy server: %+v", h[0])
		}
		return len(r.snapshot()) == 1 && len(r.snapshot()[0].snapshot()) != 0
	})
	waitDiagnosticSend(t, m, r.snapshot()[0], source)
	if got := m.ProjectInsights(); got.DiagnosticsLimited == 0 {
		t.Fatalf("capacity limit was not reported: %+v", got)
	}
}

func TestProjectCheckUsesResolvedProviderIdentity(t *testing.T) {
	m, _, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x\n")
	if _, settled, _, err := m.ProbeDiagnostics(t.Context(), path); err != nil || !settled {
		t.Fatalf("probe: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	spec, _ := m.resolveServerLocked(path)
	m.toolPaths = map[assemblyToolKey]string{{spec.ServerID, spec.Command}: filepath.Join(root, "new-checker")}
	command, needed := m.needsProjectCheckLocked(path)
	if !needed || command != filepath.Join(root, "new-checker") {
		t.Fatalf("old launch result suppressed new provider: %q %v", command, needed)
	}
}

func TestProjectWatchRecoveryRechecksCheckedFiles(t *testing.T) {
	m, _, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x\n")
	if _, settled, _, err := m.ProbeDiagnostics(t.Context(), path); err != nil || !settled {
		t.Fatalf("probe: %v", err)
	}
	m.mu.Lock()
	m.checkPending = false
	m.rememberProjectCheckLocked(filepath.Join(root, "silent.py"), "pyright-langserver", "", "No result")
	m.mu.Unlock()
	m.SetProjectWatchReliable(false)
	m.SetProjectWatchReliable(true)
	m.mu.Lock()
	pending, attempts := m.checkPending, len(m.checkResults)
	m.mu.Unlock()
	batch, _ := m.FileDiagnostics(path)
	if !pending || attempts != 0 || batch.State != model.InsightStale {
		t.Fatalf("watch recovery retained trusted checks: pending=%v attempts=%d fact=%+v", pending, attempts, batch)
	}
}

func TestProjectCheckReservesFactCapacityForOpenDocuments(t *testing.T) {
	m, r, root := probeManager(t)
	store, err := facts.New(3, 20)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sources.Insight.diagnostics = store
	m.mu.Unlock()
	paths := []string{filepath.Join(root, "a.py"), filepath.Join(root, "b.py"), filepath.Join(root, "c.py")}
	for _, path := range paths {
		createFile(t, path, "x\n")
	}
	startProjectCheck(t, m, root, map[string][]string{"python": paths})
	awaitProjectFact(t, m, paths[0])
	awaitProjectFact(t, m, paths[1])
	awaitProjectCheckEnd(t, m)
	if summary := m.ProjectInsights(); summary.ProjectCheckUnchecked != 1 {
		t.Fatalf("limited closed-file coverage not reported: %+v", summary)
	}
	if usage := store.Usage(); usage.Documents != 2 {
		t.Fatalf("background check spent editor reserve: %+v", usage)
	}
	open := filepath.Join(root, "open.py")
	source := editorSource(t, m, open, "x\n")
	waitDiagnosticSend(t, m, r.snapshot()[0], source)
	r.snapshot()[0].emit(r.snapshot()[0].publication(open, []LSPDiagnostic{{Severity: model.DiagError, Message: "editor problem"}}))
	if batch, ok := m.FileDiagnostics(open); !ok || !batch.HasExactSource() || len(batch.Diagnostics) != 1 {
		t.Fatalf("editor could not use reserve: %+v", batch)
	}
}

// The user's concern: a file that gets no answer while the computer, or the
// language server itself, is busy says nothing about the file. Crude must not
// remember it as unanswerable and give up on the server's other files; it
// tries again later. Without contention, the same silence is remembered, so
// the check still ends instead of asking again and again.
func TestProjectCheckDoesNotBlameAFileForABusyComputer(t *testing.T) {
	m, r, root := probeManager(t)
	m.SetRuntime(silentCheckRuntime{r})
	var mu sync.Mutex
	contended := true
	m.mu.Lock()
	m.contention = func() bool { mu.Lock(); defer mu.Unlock(); return contended }
	m.mu.Unlock()
	paths := []string{filepath.Join(root, "a.py"), filepath.Join(root, "b.py"), filepath.Join(root, "c.py")}
	for _, path := range paths {
		createFile(t, path, "x\n")
	}
	startProjectCheck(t, m, root, map[string][]string{"python": paths})
	awaitLSP(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return !m.checkRunning && m.checkPending && !m.checkRetryAt.IsZero()
	})
	m.mu.Lock()
	remembered, queued := len(m.checkResults), len(m.projectCheckQueueLocked())
	m.mu.Unlock()
	if remembered != 0 || queued != len(paths) || m.ProjectInsights().ProjectCheckUnchecked != 0 {
		t.Fatalf("a busy computer was blamed on the files: remembered=%d queued=%d", remembered, queued)
	}
	if attempts := len(r.snapshot()); attempts != 1 {
		t.Fatalf("the busy pass started %d servers", attempts)
	}

	// The computer is free again and the retry time has come: now silence
	// is the server's answer, and the pass ends with the files remembered.
	mu.Lock()
	contended = false
	mu.Unlock()
	m.mu.Lock()
	m.checkRetryAt = time.Now()
	m.mu.Unlock()
	awaitProjectCheckEnd(t, m)
	if unchecked := m.ProjectInsights().ProjectCheckUnchecked; unchecked != len(paths) {
		t.Fatalf("unchecked after a quiet pass = %d", unchecked)
	}
}

type busyCheckRuntime struct {
	*lifecycleRuntime
	busy *atomic.Bool
}
type busyCheckClient struct {
	*lifecycleClient
	busy *atomic.Bool
}

func (r busyCheckRuntime) NewClient(spec model.LSPServerSpec, root string, admit func() error) LSPClient {
	return &busyCheckClient{r.lifecycleRuntime.NewClient(spec, root, admit).(*lifecycleClient), r.busy}
}
func (c *busyCheckClient) Busy() bool { return c.busy.Load() }

// A server that reports its own work in progress (for example indexing)
// loads the computer because Crude started it. That is not the user's load,
// and the check says so: it waits for the server, not "while the computer is
// busy", and goes on when the server is done.
func TestProjectCheckWaitsForItsOwnServerNotForTheComputer(t *testing.T) {
	m, r, root := probeManager(t)
	busy := &atomic.Bool{}
	m.SetRuntime(busyCheckRuntime{r, busy})
	first, second := filepath.Join(root, "a.py"), filepath.Join(root, "b.py")
	createFile(t, first, "x: int = 'text'\n")
	createFile(t, second, "x: int = 'text'\n")
	startProjectCheck(t, m, root, map[string][]string{"python": {first}})
	awaitProjectFact(t, m, first)
	awaitProjectCheckEnd(t, m)
	busy.Store(true)
	startProjectCheck(t, m, root, map[string][]string{"python": {first, second}})
	awaitLSP(t, func() bool {
		return m.ProjectInsights().ProjectCheckWait == "while the language server prepares the project"
	})
	if _, ok := m.FileDiagnostics(second); ok {
		t.Fatal("the check sent a file while its server was busy")
	}
	busy.Store(false)
	awaitProjectFact(t, m, second)
}

// Found live: a Dockerfile with problems was deleted (rm -rf of its folder),
// and its problems stayed in Problems as out of date for good, because
// nothing checks a file that no longer exists. A deleted file, or every file
// below a deleted directory, leaves Problems; a file elsewhere keeps its
// result.
func TestDeletedFilesLeaveProblems(t *testing.T) {
	m, _, root := probeManager(t)
	gone := filepath.Join(root, "docker", "Dockerfile")
	kept := filepath.Join(root, "main.py")
	if err := os.MkdirAll(filepath.Dir(gone), 0o755); err != nil {
		t.Fatal(err)
	}
	createFile(t, gone, "x: int = 'text'\n")
	createFile(t, kept, "x: int = 'text'\n")
	startProjectCheck(t, m, root, map[string][]string{"python": {gone, kept}})
	awaitProjectFact(t, m, gone)
	awaitProjectFact(t, m, kept)
	awaitProjectCheckEnd(t, m)
	if err := os.RemoveAll(filepath.Dir(gone)); err != nil {
		t.Fatal(err)
	}
	m.NotifyWatchedFilesChanged(t.Context(), filepath.Dir(gone), lspFileDeleted)
	if _, ok := m.FileDiagnostics(gone); ok {
		t.Fatal("a file below a deleted directory kept its problems")
	}
	if batch, ok := m.FileDiagnostics(kept); !ok || batch.State == model.InsightStale {
		t.Fatalf("a file elsewhere lost its current result: %+v %v", batch, ok)
	}
}

// Found after a real core restart: pyright stopped when it was idle, and its
// issue resolved within seconds although its errors stayed in Problems, so
// Aeron never heard about them. Results that Crude keeps for a stopped server
// keep raising the issue until the files change.
func TestLSPIssueOutlivesAnIdleServerStop(t *testing.T) {
	m, _, root := probeManager(t)
	path := filepath.Join(root, "main.py")
	createFile(t, path, "x: int = 'text'\n")
	startProjectCheck(t, m, root, map[string][]string{"python": {path}})
	awaitProjectFact(t, m, path)
	awaitProjectCheckEnd(t, m)
	center := NewStatusCenter(nil, nil)
	SyncLSPIssues(center, m)
	open := func() []model.IssueSnapshot {
		var out []model.IssueSnapshot
		for _, issue := range center.Issues() {
			if issue.ResolvedAt.IsZero() && strings.HasPrefix(issue.ID, lspResultsIssuePrefix) {
				out = append(out, issue)
			}
		}
		return out
	}
	before := open()
	if len(before) != 1 {
		t.Fatalf("errors in the only checked file raised %+v", center.Issues())
	}
	// An idle stop or an eviction retires the process the same way.
	m.mu.Lock()
	for _, state := range m.states {
		m.retireLocked(state)
	}
	m.mu.Unlock()
	awaitLSP(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.states) == 0 })
	SyncLSPIssues(center, m)
	if after := open(); len(after) != 1 || after[0].ID != before[0].ID || !after[0].UpdatedAt.Equal(before[0].UpdatedAt) {
		t.Fatalf("a stopped server dropped its issue: %+v", center.Issues())
	}
	if batch, ok := m.FileDiagnostics(path); !ok || batch.State == model.InsightStale {
		t.Fatalf("the stop changed the checked result: %+v %v", batch, ok)
	}
}
