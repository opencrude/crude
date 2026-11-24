package workspace

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/types/insight"
	"crude/types/setting"
)

// workspacePullRuntime starts servers that offer workspace diagnostics: each
// request reports the closed files it was told about.
type workspacePullRuntime struct {
	*lifecycleRuntime
	mu       sync.Mutex
	reports  map[string]string // path -> message
	requests int
	active   int
}

type workspacePullClient struct {
	*lifecycleClient
	runtime *workspacePullRuntime
}

func (r *workspacePullRuntime) NewClient(spec setting.LSPServerSpec, root string, admit func() error) LSPClient {
	return &workspacePullClient{lifecycleClient: r.lifecycleRuntime.NewClient(spec, root, admit).(*lifecycleClient), runtime: r}
}

func (c *workspacePullClient) WorkspaceDiagnostics() bool { return true }

func (c *workspacePullClient) PullWorkspaceDiagnostics(ctx context.Context) error {
	c.runtime.mu.Lock()
	c.runtime.requests++
	c.runtime.active++
	reports := make(map[string]string, len(c.runtime.reports))
	for path, message := range c.runtime.reports {
		reports[path] = message
	}
	c.runtime.mu.Unlock()
	defer func() {
		c.runtime.mu.Lock()
		c.runtime.active--
		c.runtime.mu.Unlock()
	}()
	c.mu.Lock()
	unopened := c.unopened
	c.mu.Unlock()
	for path, message := range reports {
		var diagnostics []insight.LSPDiagnostic
		if message != "" {
			diagnostics = []insight.LSPDiagnostic{{Message: message, Severity: 1}}
		}
		unopened(path, insight.DiagnosticPublication{Sequence: uint64(time.Now().UnixNano()), WireVersion: 1, Diagnostics: diagnostics})
	}
	// A server may keep the request open; this one does until cancelled or
	// a moment passes.
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
	}
	return nil
}

func (r *workspacePullRuntime) counts() (requests, active int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests, r.active
}

// A server that offers workspace diagnostics covers the project: Crude keeps
// its workspace request, publishes what it reports about closed files, clears
// a file that it reports clean, never opens those files one at a time, and
// stops the request when the user turns Check closed files off.
func TestWorkspaceDiagnosticsReplaceTheClosedFileCheck(t *testing.T) {
	previous := workspacePullRest
	workspacePullRest = 200 * time.Millisecond
	t.Cleanup(func() { workspacePullRest = previous })
	m, base, root := probeManager(t)
	r := &workspacePullRuntime{lifecycleRuntime: base, reports: map[string]string{}}
	m.SetRuntime(r)

	closed := []string{filepath.Join(root, "a.py"), filepath.Join(root, "pkg", "b.py")}
	for _, path := range closed {
		mkdir(t, filepath.Dir(path))
		createFile(t, path, "x: int = 'text'\n")
	}
	r.mu.Lock()
	r.reports[closed[0]] = "a has a problem"
	r.reports[closed[1]] = "b has a problem"
	r.mu.Unlock()
	startProjectCheck(t, m, root, map[string][]string{"python": closed})

	for _, path := range closed {
		awaitLSP(t, func() bool {
			batch, ok := m.FileDiagnostics(path)
			return ok && len(batch.Diagnostics) == 1 && strings.HasSuffix(batch.Diagnostics[0].Message, "has a problem")
		})
	}
	var health []string
	for _, server := range m.ServerHealthSnapshots() {
		health = append(health, string(server.Scope)+": "+server.Coverage)
	}
	if !slices.Contains(health, string(insight.CoverageProject)+": Checks the whole project, including files that are not open.") {
		t.Fatalf("coverage does not say the server checks the project: %v", health)
	}
	for _, call := range base.snapshot()[0].snapshot() {
		if strings.HasPrefix(call, "open:a.py") || strings.HasPrefix(call, "open:b.py") {
			t.Fatalf("a closed file of a workspace-diagnostics server was opened: %v", base.snapshot()[0].snapshot())
		}
	}

	// The server's next report says a.py is clean: its row leaves Problems.
	r.mu.Lock()
	r.reports[closed[0]] = ""
	r.mu.Unlock()
	awaitLSP(t, func() bool {
		batch, ok := m.FileDiagnostics(closed[0])
		return !ok || len(batch.Diagnostics) == 0
	})
	if requests, _ := r.counts(); requests < 2 {
		t.Fatalf("the workspace request was not repeated: %d", requests)
	}

	settings := m.Settings()
	settings.ProjectCheck = setting.ProjectCheckOff
	m.SetSettings(settings)
	awaitLSP(t, func() bool { _, active := r.counts(); return active == 0 })
	stopped, _ := r.counts()
	time.Sleep(3 * workspacePullRest)
	if requests, _ := r.counts(); requests != stopped {
		t.Fatalf("workspace requests continued with Check closed files off: %d -> %d", stopped, requests)
	}
}

// A server may announce its project provider only after initialize and the
// first fallback didOpen. Its first workspace request can stay open, so an
// error discarded during that request is not guaranteed to be sent again.
func TestLateWorkspaceDiagnosticsDrainFallbackBeforeFirstRequest(t *testing.T) {
	m, base, root := probeManager(t)
	r := &lateProjectPullRuntime{
		lifecycleRuntime: base,
		waiting:          make(chan string, 1),
		firstRequest:     make(chan bool, 1),
	}
	m.SetRuntime(r)
	path := filepath.Join(root, "late.py")
	createFile(t, path, "value: int = 'bad'\n")
	startProjectCheck(t, m, root, map[string][]string{"python": {path}})
	select {
	case diskView := <-r.waiting:
		if diskView != path {
			t.Fatalf("fallback opened %s, want %s", diskView, path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fallback did not open a disk view before the registration")
	}
	r.announced.Store(true)
	m.mu.Lock()
	m.signalLocked()
	m.mu.Unlock()
	select {
	case closed := <-r.firstRequest:
		if !closed {
			t.Fatal("the native request started before the fallback's didClose")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("native project diagnostics waited for a silent fallback's timeout")
	}
	awaitLSP(t, func() bool {
		batch, ok := m.FileDiagnostics(path)
		return ok && batch.FromProjectCheck() && len(batch.Diagnostics) == 1 && batch.Diagnostics[0].Message == "first workspace report"
	})
	if summary := m.ProjectInsights(); summary.ProjectCheckUnchecked != 0 {
		t.Fatalf("cancelled fallback became an unchecked file: %+v", summary)
	}
}

type lateProjectPullRuntime struct {
	*lifecycleRuntime
	announced    atomic.Bool
	waiting      chan string
	firstRequest chan bool
}

type lateProjectPullClient struct {
	*lifecycleClient
	runtime *lateProjectPullRuntime
	path    string
}

func (r *lateProjectPullRuntime) NewClient(spec setting.LSPServerSpec, root string, admit func() error) LSPClient {
	return &lateProjectPullClient{lifecycleClient: r.lifecycleRuntime.NewClient(spec, root, admit).(*lifecycleClient), runtime: r}
}

func (c *lateProjectPullClient) AwaitDiagnostics(ctx context.Context, path string) (insight.DiagnosticPublication, bool) {
	c.mu.Lock()
	c.path = path
	c.mu.Unlock()
	select {
	case c.runtime.waiting <- path:
	default:
	}
	<-ctx.Done()
	return insight.DiagnosticPublication{}, false
}

func (c *lateProjectPullClient) WorkspaceDiagnostics() bool { return c.runtime.announced.Load() }

func (c *lateProjectPullClient) PullWorkspaceDiagnostics(ctx context.Context) error {
	c.mu.Lock()
	path, report := c.path, c.unopened
	closed := slices.Contains(c.calls, "close:"+filepath.Base(path))
	c.mu.Unlock()
	select {
	case c.runtime.firstRequest <- closed:
	case <-ctx.Done():
		return ctx.Err()
	}
	report(path, insight.DiagnosticPublication{Sequence: 1, WireVersion: 1,
		Diagnostics: []insight.LSPDiagnostic{{Message: "first workspace report", Severity: 1}}})
	<-ctx.Done()
	return nil
}
