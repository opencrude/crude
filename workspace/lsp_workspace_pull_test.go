package workspace

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	open := filepath.Join(root, "open.py")
	closed := []string{filepath.Join(root, "a.py"), filepath.Join(root, "pkg", "b.py")}
	for _, path := range closed {
		mkdir(t, filepath.Dir(path))
		createFile(t, path, "x: int = 'text'\n")
	}
	r.mu.Lock()
	r.reports[closed[0]] = "a has a problem"
	r.reports[closed[1]] = "b has a problem"
	r.mu.Unlock()
	editorSource(t, m, open, "x = 1\n")
	awaitLSP(t, func() bool { return len(base.snapshot()) == 1 })
	startProjectCheck(t, m, root, map[string][]string{"python": append([]string{open}, closed...)})

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
