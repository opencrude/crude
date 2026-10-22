package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
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
}
type lifecycleClient struct {
	reapableClient
	mu                  sync.Mutex
	runtime             *lifecycleRuntime
	diagnostics         func(string, []LSPDiagnostic)
	calls               []string
	startGate, stopGate <-chan struct{}
	started, stopped    bool
	startErr            error
}

func (r *lifecycleRuntime) NewClient(spec model.LSPServerSpec, root string) LSPClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := &lifecycleClient{runtime: r, startGate: r.startGate, stopGate: r.stopGate, reapableClient: reapableClient{done: make(chan struct{})}}
	if len(r.startErrs) > 0 {
		c.startErr, r.startErrs = r.startErrs[0], r.startErrs[1:]
	}
	r.clients = append(r.clients, c)
	r.roots = append(r.roots, root)
	r.specs = append(r.specs, spec)
	return c
}
func (c *lifecycleClient) OnDiagnostics(fn func(string, []LSPDiagnostic)) {
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
func (c *lifecycleClient) Stop() {
	if c.stopGate != nil {
		<-c.stopGate
	}
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	started := c.started
	c.mu.Unlock()
	if started {
		c.runtime.mu.Lock()
		c.runtime.active--
		c.runtime.mu.Unlock()
	}
}
func (c *lifecycleClient) record(value string) {
	c.mu.Lock()
	c.calls = append(c.calls, value)
	c.mu.Unlock()
}
func (c *lifecycleClient) OpenFile(_ context.Context, path, language, text string) {
	c.record("open:" + filepath.Base(path) + ":" + language + ":" + text)
}
func (c *lifecycleClient) UpdateFile(_ context.Context, path, text string) {
	c.record("change:" + filepath.Base(path) + ":" + text)
}
func (c *lifecycleClient) CloseFile(_ context.Context, path string) {
	c.record("close:" + filepath.Base(path))
}
func (c *lifecycleClient) NotifyWatchedFilesChanged(_ context.Context, path string, kind int) {
	c.record(fmt.Sprintf("watch:%s:%d", filepath.Base(path), kind))
}
func (c *lifecycleClient) RequestCompletions(_ context.Context, _ string, _, _ int) []CompletionItem {
	c.record("complete")
	return []CompletionItem{{Label: "ok"}}
}
func (c *lifecycleClient) emit(path string, ds []LSPDiagnostic) {
	c.mu.Lock()
	fn := c.diagnostics
	c.mu.Unlock()
	if fn != nil {
		fn(path, ds)
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
	m.OpenFile(t.Context(), path, "initial")
	m.OpenFile(t.Context(), closed, "never open")
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	m.QueueFileUpdate(path, "latest unsaved")
	m.CloseFile(t.Context(), closed)
	close(gate)
	c := r.snapshot()[0]
	awaitLSP(t, func() bool { return len(c.snapshot()) == 1 })
	if got := c.snapshot()[0]; got != "open:main.go:go:latest unsaved" {
		t.Fatalf("startup sync=%s", got)
	}
	m.OpenFile(t.Context(), path, "latest unsaved")
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
	m.OpenFile(t.Context(), path, "unsaved")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	old := r.snapshot()[0]
	old.emit(path, []LSPDiagnostic{{Message: "error", Severity: 1}})
	id := m.ServerHealthSnapshots()[0].ID
	if !m.StopServer(id) {
		t.Fatal("stop rejected")
	}
	awaitLSP(t, func() bool { old.mu.Lock(); defer old.mu.Unlock(); return old.stopped })
	m.QueueFileUpdate(path, "kept while stopped")
	old.emit(path, []LSPDiagnostic{{Message: "stale", Severity: 1}})
	if len(m.FileDiagnostics(path)) != 0 {
		t.Fatal("stale diagnostics revived stopped state")
	}
	if !m.RestartServer(id) {
		t.Fatal("stopped server lost restart identity")
	}
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 2 && len(cs[1].snapshot()) == 1 })
	if got := r.snapshot()[1].snapshot()[0]; got != "open:main.go:go:kept while stopped" {
		t.Fatal(got)
	}
	old.emit(path, []LSPDiagnostic{{Message: "old owner", Severity: 1}})
	if len(m.FileDiagnostics(path)) != 0 {
		t.Fatal("old process wrote new process cache")
	}
	changes := m.DrainDiagnosticChanges()
	if _, ok := changes[path]; !ok {
		t.Fatal("clear omitted UI delta")
	}
}

// A missing server is reported as such, and Retry after installing it starts
// the server for the documents still open, without reopening them.
func TestLSPRetryAfterInstallingMissingServer(t *testing.T) {
	m, r, root := lifecycleManager(t)
	r.startErrs = []error{fmt.Errorf("failed to start process: %w", &exec.Error{Name: "gopls", Err: exec.ErrNotFound})}
	path := filepath.Join(root, "main.go")
	m.OpenFile(t.Context(), path, "unsaved")
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
	m.OpenFile(t.Context(), a, "go")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	m.OpenFile(t.Context(), b, "python")
	awaitLSP(t, func() bool {
		for _, h := range m.ServerHealthSnapshots() {
			if h.Status == "Waiting for slot" {
				return true
			}
		}
		return false
	})
	m.CloseFile(t.Context(), a)
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

func TestLSPDisableCancelsPendingStartAndDoesNotResurrect(t *testing.T) {
	m, r, root := lifecycleManager(t)
	gate := make(chan struct{})
	r.startGate = gate
	m.OpenFile(t.Context(), filepath.Join(root, "main.go"), "data")
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
	m.OpenFile(t.Context(), path, "initial")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	m.QueueFileUpdate(path, "changed")
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
	m.OpenFile(t.Context(), filepath.Join(root, "large.go"), strings.Repeat("x", 65))
	m.OpenFile(t.Context(), filepath.Join(root, "small.go"), "small")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	if got := r.snapshot()[0].snapshot()[0]; got != "open:small.go:go:small" {
		t.Fatal(got)
	}
}

func TestLSPIdleProcessEviction(t *testing.T) {
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	m.OpenFile(t.Context(), path, "data")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	m.CloseFile(t.Context(), path)
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

func (c *lifecycleClient) AwaitDiagnostics(_ context.Context, path string) ([]LSPDiagnostic, bool) {
	c.record("await:" + filepath.Base(path))
	return []LSPDiagnostic{{Message: "probe diagnostic", Severity: 1}}, true
}
