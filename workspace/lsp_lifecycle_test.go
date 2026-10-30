package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	model "crude/types"
)

type lifecycleRuntime struct {
	mu                  sync.Mutex
	clients             []*lifecycleClient
	roots               []string
	specs               []model.LSPServerSpec
	startGate, stopGate <-chan struct{}
	active, peak        int
	// startErrs fail the next starts in order, like a missing executable.
	startErrs []error
	stopErr   error
}
type lifecycleClient struct {
	reapableClient
	mu                  sync.Mutex
	runtime             *lifecycleRuntime
	diagnostics         func(model.InsightDiagnosticPublication)
	calls               []string
	wireVersion         int
	boundaries          map[string]model.InsightBoundary
	versions            map[string]int
	publications        map[string]model.InsightDiagnosticPublication
	sequence            uint64
	startGate, stopGate <-chan struct{}
	started, stopped    bool
	startErr, stopErr   error
}

func (r *lifecycleRuntime) NewClient(spec model.LSPServerSpec, root string) LSPClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := &lifecycleClient{runtime: r, startGate: r.startGate, stopGate: r.stopGate, stopErr: r.stopErr, reapableClient: reapableClient{done: make(chan struct{})}}
	if len(r.startErrs) > 0 {
		c.startErr, r.startErrs = r.startErrs[0], r.startErrs[1:]
	}
	r.clients = append(r.clients, c)
	r.roots = append(r.roots, root)
	r.specs = append(r.specs, spec)
	return c
}
func (c *lifecycleClient) OnDiagnostics(fn func(model.InsightDiagnosticPublication)) {
	c.mu.Lock()
	c.diagnostics = fn
	c.mu.Unlock()
}
func (c *lifecycleClient) Start(ctx context.Context) error {
	if c.startGate != nil {
		select {
		case <-c.startGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.startErr != nil {
		return c.startErr
	}
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	c.runtime.mu.Lock()
	c.runtime.active++
	c.runtime.peak = max(c.runtime.peak, c.runtime.active)
	c.runtime.mu.Unlock()
	return nil
}
func (c *lifecycleClient) Stop() error {
	if c.stopGate != nil {
		<-c.stopGate
	}
	c.mu.Lock()
	if c.stopErr != nil {
		err := c.stopErr
		c.mu.Unlock()
		return err
	}
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	started := c.started
	c.mu.Unlock()
	if started {
		c.runtime.mu.Lock()
		c.runtime.active--
		c.runtime.mu.Unlock()
	}
	return nil
}
func (c *lifecycleClient) record(value string) {
	c.mu.Lock()
	c.calls = append(c.calls, value)
	c.mu.Unlock()
}
func (c *lifecycleClient) send(path, call string, boundary model.InsightBoundary) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
	c.wireVersion++
	if c.boundaries == nil {
		c.boundaries = make(map[string]model.InsightBoundary)
		c.versions = make(map[string]int)
		c.publications = make(map[string]model.InsightDiagnosticPublication)
	}
	c.boundaries[path], c.versions[path] = boundary, c.wireVersion
	return c.wireVersion, nil
}
func (c *lifecycleClient) OpenFile(_ context.Context, path, language, text string, boundary model.InsightBoundary) (int, error) {
	return c.send(path, "open:"+filepath.Base(path)+":"+language+":"+text, boundary)
}
func (c *lifecycleClient) UpdateFile(_ context.Context, path, text string, boundary model.InsightBoundary) (int, error) {
	return c.send(path, "change:"+filepath.Base(path)+":"+text, boundary)
}
func (c *lifecycleClient) CloseFile(_ context.Context, path string) {
	c.record("close:" + filepath.Base(path))
}
func (c *lifecycleClient) NotifyWatchedFilesChanged(_ context.Context, path string, kind int) {
	c.record(fmt.Sprintf("watch:%s:%d", filepath.Base(path), kind))
}
func (c *lifecycleClient) NotifySave(_ context.Context, _ string, text string, _ int, before bool) {
	phase := "didSave:"
	if before {
		phase = "willSave:"
	}
	c.record(phase + text)
}
func (c *lifecycleClient) RequestCompletions(_ context.Context, _ string, _, _ int) []CompletionItem {
	c.record("complete")
	return []CompletionItem{{Label: "ok"}}
}
func (c *lifecycleClient) publication(path string, ds []LSPDiagnostic) model.InsightDiagnosticPublication {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	v := c.versions[path]
	return model.InsightDiagnosticPublication{Boundary: c.boundaries[path], Sequence: c.sequence,
		WireVersion: v, ReportedVersion: &v, Diagnostics: ds}
}
func (c *lifecycleClient) emit(p model.InsightDiagnosticPublication) {
	c.mu.Lock()
	fn := c.diagnostics
	if c.publications == nil {
		c.publications = make(map[string]model.InsightDiagnosticPublication)
	}
	c.publications[p.Boundary.Source.Path] = p
	c.mu.Unlock()
	if fn != nil {
		fn(p)
	}
}
func (c *lifecycleClient) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}
func (r *lifecycleRuntime) snapshot() []*lifecycleClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*lifecycleClient(nil), r.clients...)
}
func awaitLSP(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("LSP lifecycle did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}
func lifecycleManager(t *testing.T) (*LSPManager, *lifecycleRuntime, string) {
	t.Helper()
	r := &lifecycleRuntime{}
	m := NewLSPManager(nil, r)
	root := t.TempDir()
	m.SetWorkspaceRoot(root)
	t.Cleanup(m.Close)
	return m, r, root
}

func TestLSPDocumentLifetimesAreLazyAndRetainLatestDuringStart(t *testing.T) {
	m, r, root := lifecycleManager(t)
	gate := make(chan struct{})
	r.startGate = gate
	for _, name := range []string{"main.go", "platform_windows.go", "unopened.go"} {
		createFile(t, filepath.Join(root, name), "package main")
	}
	m.StartForWorkspace(root)
	if len(r.snapshot()) != 0 {
		t.Fatal("workspace start spawned unused server")
	}
	path := filepath.Join(root, "main.go")
	closed := filepath.Join(root, "closed.go")
	editorSource(t, m, path, "initial")
	editorSource(t, m, closed, "never open")
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	editorSource(t, m, path, "latest unsaved")
	closeEditor(t, m, closed)
	close(gate)
	c := r.snapshot()[0]
	awaitLSP(t, func() bool { return len(c.snapshot()) == 1 })
	if got := c.snapshot()[0]; got != "open:main.go:go:latest unsaved" {
		t.Fatalf("startup sync=%s", got)
	}
	editorSource(t, m, path, "latest unsaved")
	m.mu.Lock()
	m.signalLocked()
	m.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	if len(c.snapshot()) != 1 {
		t.Fatalf("duplicate didOpen: %v", c.snapshot())
	}
}

func TestLSPStopRestartRetainsDocumentAndRejectsStaleDiagnostics(t *testing.T) {
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	initial := editorSource(t, m, path, "unsaved")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	old := r.snapshot()[0]
	waitDiagnosticSend(t, m, old, initial)
	old.emit(old.publication(path, []LSPDiagnostic{{Message: "error", Severity: 1}}))
	requireDiagnostic(t, m, path, "error")
	id := m.ServerHealthSnapshots()[0].ID
	if !m.StopServer(id) {
		t.Fatal("stop rejected")
	}
	awaitLSP(t, func() bool { old.mu.Lock(); defer old.mu.Unlock(); return old.stopped })
	editorSource(t, m, path, "kept while stopped")
	old.emit(old.publication(path, []LSPDiagnostic{{Message: "stale", Severity: 1}}))
	if batch, _ := m.FileDiagnostics(path); batch.HasExactSource() {
		t.Fatal("stale diagnostics revived stopped state")
	}
	if !m.RestartServer(id) {
		t.Fatal("stopped server lost restart identity")
	}
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 2 && len(cs[1].snapshot()) == 1 })
	if got := r.snapshot()[1].snapshot()[0]; got != "open:main.go:go:kept while stopped" {
		t.Fatal(got)
	}
	old.emit(old.publication(path, []LSPDiagnostic{{Message: "old owner", Severity: 1}}))
	if batch, _ := m.FileDiagnostics(path); batch.HasExactSource() {
		t.Fatal("old process wrote new process cache")
	}
	if batch, ok := m.FileDiagnostics(path); !ok || batch.State != model.InsightStale {
		t.Fatal("retirement did not retain stale publication evidence")
	}
}

// A missing server is reported as such, and Retry after installing it starts
// the server for the documents still open, without reopening them.
func TestLSPRetryAfterInstallingMissingServer(t *testing.T) {
	m, r, root := lifecycleManager(t)
	r.startErrs = []error{fmt.Errorf("failed to start process: %w", &exec.Error{Name: "gopls", Err: exec.ErrNotFound})}
	path := filepath.Join(root, "main.go")
	editorSource(t, m, path, "unsaved")
	awaitLSP(t, func() bool { h := m.ServerHealthSnapshots(); return len(h) == 1 && h[0].Error != "" })
	server := m.ServerHealthSnapshots()[0]
	if server.Error != "gopls is not installed or not on PATH. Install it, then choose Retry." {
		t.Fatalf("missing server error = %q", server.Error)
	}
	if !m.RestartServer(server.ID) {
		t.Fatal("retry rejected for a failed server")
	}
	awaitLSP(t, func() bool { h := m.ServerHealthSnapshots(); return len(h) == 1 && h[0].Connected })
	if cs := r.snapshot(); len(cs) != 2 || len(cs[1].snapshot()) != 1 || cs[1].snapshot()[0] != "open:main.go:go:unsaved" {
		t.Fatalf("retry did not reopen the document on the new server")
	}
}

func TestLSPBudgetWaitsForOwnedShutdownBeforeReplacement(t *testing.T) {
	m, r, root := lifecycleManager(t)
	s := m.Settings()
	s.MaxServers = 1
	m.SetSettings(s)
	stop := make(chan struct{})
	r.stopGate = stop
	a, b := filepath.Join(root, "main.go"), filepath.Join(root, "app.py")
	editorSource(t, m, a, "go")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	editorSource(t, m, b, "python")
	awaitLSP(t, func() bool {
		for _, h := range m.ServerHealthSnapshots() {
			if h.Status == "Waiting for slot" {
				return true
			}
		}
		return false
	})
	closeEditor(t, m, a)
	time.Sleep(10 * time.Millisecond)
	if len(r.snapshot()) != 1 {
		t.Fatal("started replacement while old process still alive")
	}
	close(stop)
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 2 && len(cs[1].snapshot()) == 1 })
	r.mu.Lock()
	peak := r.peak
	r.mu.Unlock()
	if peak != 1 {
		t.Fatalf("process budget peak=%d", peak)
	}
}

func TestLSPCloseJoinsServersAcrossWorkerWake(t *testing.T) {
	// Close races ordinary worker wakeups, as detaching the final editor and
	// publishing server health do during real core teardown. A returned Close
	// must mean the transport was stopped, not just that its worker disappeared.
	root := t.TempDir()
	for attempt := 0; attempt < 250; attempt++ {
		r := &lifecycleRuntime{}
		m := NewLSPManager(nil, r)
		t.Cleanup(m.Close)
		m.SetWorkspaceRoot(root)
		editorSource(t, m, filepath.Join(root, "main.go"), "package main")
		awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
		started, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			close(started)
			for {
				m.mu.Lock()
				if m.closed {
					m.mu.Unlock()
					return
				}
				m.signalLocked()
				m.mu.Unlock()
				runtime.Gosched()
			}
		}()
		<-started
		m.Close()
		<-stopped
		m.sources.Close()
		r.mu.Lock()
		active := r.active
		r.mu.Unlock()
		if active != 0 {
			t.Fatalf("Close returned with %d owned language servers on wake %d", active, attempt)
		}
		m.Close() // The terminal join is also idempotent.
	}
}

func TestLSPDisableCancelsPendingStartAndDoesNotResurrect(t *testing.T) {
	m, r, root := lifecycleManager(t)
	gate := make(chan struct{})
	r.startGate = gate
	editorSource(t, m, filepath.Join(root, "main.go"), "data")
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	s := m.Settings()
	s.Enabled = false
	m.SetSettings(s)
	awaitLSP(t, func() bool { return len(m.ServerHealthSnapshots()) == 0 })
	close(gate)
	if connected, _, _ := m.Status(); connected {
		t.Fatal("disabled manager reports connected")
	}
	if len(r.snapshot()[0].snapshot()) != 0 {
		t.Fatal("late didOpen after disable")
	}
}

func TestLSPRequestsFlushDebounceAndWatchersDoNotOpenFiles(t *testing.T) {
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	editorSource(t, m, path, "initial")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	editorSource(t, m, path, "changed")
	m.RequestCompletions(t.Context(), path, 0, 1)
	m.NotifyWatchedFilesChanged(t.Context(), filepath.Join(root, "go.mod"), 2)
	m.NotifyWatchedFilesChanged(t.Context(), filepath.Join(root, "external.go"), 2)
	calls := r.snapshot()[0].snapshot()
	if len(calls) < 3 || calls[1] != "change:main.go:changed" || calls[2] != "complete" {
		t.Fatalf("stale request context: %v", calls)
	}
	for _, call := range calls[1:] {
		if strings.HasPrefix(call, "open:") {
			t.Fatalf("watcher fabricated overlay: %v", calls)
		}
	}
}

func TestLSPLargeDocumentDoesNotPoisonOtherDocuments(t *testing.T) {
	m, r, root := lifecycleManager(t)
	s := m.Settings()
	s.MaxDocumentBytes = 64
	m.SetSettings(s)
	editorSource(t, m, filepath.Join(root, "large.go"), strings.Repeat("x", 65))
	editorSource(t, m, filepath.Join(root, "small.go"), "small")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	if got := r.snapshot()[0].snapshot()[0]; got != "open:small.go:go:small" {
		t.Fatal(got)
	}
}

func TestLSPIdleProcessEviction(t *testing.T) {
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	editorSource(t, m, path, "data")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	closeEditor(t, m, path)
	m.mu.Lock()
	for _, state := range m.states {
		state.lastUsed = time.Now().Add(-time.Hour)
	}
	m.signalLocked()
	m.mu.Unlock()
	awaitLSP(t, func() bool { return len(m.ServerHealthSnapshots()) == 0 })
}

func TestLSPRootUsesLanguageMarkersAndWorkspaceBoundary(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "src", "nested")
	mkdir(t, nested)
	createFile(t, filepath.Join(root, "go.work"), "go 1.25\n")
	createFile(t, filepath.Join(root, "src", "go.mod"), "module nested")
	if got := resolveLSPRoot(filepath.Join(nested, "main.go"), root, []string{"go.work", "go.mod"}); got != fileURI(root) {
		t.Fatal(got)
	}
	createFile(t, filepath.Join(root, "Solution.sln"), "")
	createFile(t, filepath.Join(nested, "Project.csproj"), "")
	if got := resolveLSPRoot(filepath.Join(nested, "Main.cs"), root, []string{"*.sln", "*.csproj"}); got != fileURI(root) {
		t.Fatal(got)
	}
	if got := resolveLSPRoot(filepath.Join(nested, "test.xyz"), root, nil); got != fileURI(root) {
		t.Fatal("fallback fragmented workspace root", got)
	}
	if got := resolveLSPRoot(filepath.Join(nested, "main.go"), nested, []string{"go.work", "go.mod"}); got != fileURI(nested) {
		t.Fatal("escaped workspace boundary", got)
	}
}

func (c *lifecycleClient) AwaitDiagnostics(_ context.Context, path string) (model.InsightDiagnosticPublication, bool) {
	c.record("await:" + filepath.Base(path))
	c.mu.Lock()
	p, ok := c.publications[path]
	current := ok && sameInsightBoundary(p.Boundary, c.boundaries[path]) && p.WireVersion == c.versions[path]
	c.mu.Unlock()
	if !current {
		p = c.publication(path, []LSPDiagnostic{{Message: "probe diagnostic", Severity: 1}})
		c.emit(p)
	}
	return p, true
}
