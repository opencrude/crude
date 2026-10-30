package viewmodel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/document"
	model "crude/types"
	"crude/workspace"
)

// These acceptance tests never install servers or touch application settings.
// Run explicitly with CRUDE_LSP_LIVE=1 GOMAXPROCS=2 go test -p 2
// ./tui/viewmodel -run '^TestLiveLSP' -count=1 -v.
type liveLSPRuntime struct {
	workspace.LSPRuntime
	mu      sync.Mutex
	created int
	opened  []string
}

type liveLSPClient struct {
	workspace.LSPClient
	owner *liveLSPRuntime
}

func (r *liveLSPRuntime) NewClient(spec model.LSPServerSpec, root string, startAdmission func() error) workspace.LSPClient {
	r.mu.Lock()
	r.created++
	r.mu.Unlock()
	return &liveLSPClient{LSPClient: r.LSPRuntime.NewClient(spec, root, startAdmission), owner: r}
}

func (c *liveLSPClient) OpenFile(ctx context.Context, path, language, text string, boundary model.InsightBoundary) (int, error) {
	c.owner.mu.Lock()
	c.owner.opened = append(c.owner.opened, path)
	c.owner.mu.Unlock()
	return c.LSPClient.OpenFile(ctx, path, language, text, boundary)
}

func liveLSPContext(t *testing.T) context.Context {
	t.Helper()
	if os.Getenv("CRUDE_LSP_LIVE") != "1" {
		t.Skip("opt-in real-server acceptance: set CRUDE_LSP_LIVE=1")
	}
	t.Setenv("GOMAXPROCS", "2")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func liveLSPExecutable(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("installed %s unavailable: %v", name, err)
	}
	arg := "--version"
	if name == "gopls" {
		arg = "version"
	}
	versionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(versionCtx, path, arg).CombinedOutput()
	t.Logf("server executable=%s version=%s (error=%v)", path, strings.TrimSpace(string(out)), err)
	return path
}

func liveLSPManager(t *testing.T, root string) (*workspace.LSPManager, *liveLSPRuntime, *workspace.DocumentService) {
	t.Helper()
	r := &liveLSPRuntime{LSPRuntime: workspace.NewLSPRuntime()}
	m := workspace.NewLSPManager(nil, r)
	t.Cleanup(m.Close) // Only this manager's exact owned processes are stopped.
	admitSemanticManager(t, m, root)
	s := model.DefaultLSPSettings()
	s.Enabled, s.MaxServers, s.ServerThreads = true, 1, 2
	if address := os.Getenv("CRUDE_LSP_PROFILE_ADDR"); address != "" {
		cfg := s.Languages["go"]
		cfg.Args = []string{"-debug=" + address}
		s.Languages["go"] = cfg
	}
	if os.Getenv("CRUDE_LSP_NO_STATICCHECK") == "1" {
		cfg := s.Languages["go"]
		cfg.Settings = map[string]any{"gopls": map[string]any{"staticcheck": false}}
		s.Languages["go"] = cfg
	}
	m.SetSettings(s)
	m.StartForWorkspace(root)
	documents := workspace.NewDocumentService(root, nil, m)
	t.Cleanup(documents.Close)
	t.Logf("limits: overall=45s max_servers=1 server_threads=2 server_memory_mb=%d (soft hint), GOMAXPROCS=2", s.ServerMemoryMB)
	return m, r, documents
}

var liveLSPOwner = document.Owner{ClientID: "live-lsp-editor", Epoch: 1}

func liveLSPOpen(t *testing.T, documents *workspace.DocumentService, path string) document.Snapshot {
	t.Helper()
	snapshot, err := documents.Open(liveLSPOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func liveLSPReplace(t *testing.T, documents *workspace.DocumentService, snapshot document.Snapshot, text string) document.Snapshot {
	t.Helper()
	_, err := documents.Apply(document.Change{
		Owner: liveLSPOwner, DocumentID: snapshot.ID, BaseRevision: snapshot.Revision,
		RequestID: fmt.Sprintf("replace-%d", snapshot.Revision),
		Edits:     []document.Edit{{Start: 0, End: len(snapshot.Text), Text: text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := documents.Snapshot(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func liveLSPWait(t *testing.T, ctx context.Context, m *workspace.LSPManager, label string, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		_, status, reason := m.Status()
		if status == workspace.LSPFailed {
			t.Fatalf("%s: server failed: %s", label, reason)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: bounded wait expired: health=%+v diagnostics=%+v", label, m.ServerHealthSnapshots(), m.AllDiagnostics())
		case <-ticker.C:
		}
	}
}

func liveLSPWrite(t *testing.T, root, name, text string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func liveLSPOpenOnly(t *testing.T, r *liveLSPRuntime, path string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.created != 1 || len(r.opened) != 1 || r.opened[0] != path {
		t.Fatalf("expected one server and only requested editor document %s: created=%d opened=%v", path, r.created, r.opened)
	}
}

func liveLSPSemantics(t *testing.T, ctx context.Context, m *workspace.LSPManager, path, text string, line, col, completionCol int) {
	t.Helper()
	liveLSPWait(t, ctx, m, "semantic hover", func() bool {
		hover, err := m.RequestHover(ctx, path, line, col)
		return err == nil && strings.Contains(hover, "answer")
	})
	defs := m.RequestDefinition(ctx, path, line, col)
	if len(defs) == 0 || filepath.Clean(defs[0].Path) != path || defs[0].Line != 1 {
		t.Fatalf("definition should resolve answer declaration in opened file: %+v", defs)
	}
	items := m.RequestCompletions(ctx, path, line, completionCol)
	found := false
	for _, item := range items {
		found = found || strings.Contains(item.Label, "answer")
	}
	if !found {
		t.Fatalf("semantic completion missing answer: %+v (source=%q)", items, text)
	}
}

func TestLiveLSPGo(t *testing.T) {
	ctx := liveLSPContext(t)
	liveLSPExecutable(t, ctx, "gopls")
	root := t.TempDir()
	liveLSPWrite(t, root, "go.mod", "module example.test/livesemantics\n\ngo 1.22\n")
	good := "package main\nfunc answer() int { return 42 }\nfunc main() { _ = answer() }\n"
	path := liveLSPWrite(t, root, "main.go", good)
	// Excluded on every host: opening it accidentally would manufacture an error.
	sibling := liveLSPWrite(t, root, "alternate_windows.go", "//go:build ignore\n\npackage main\nvar _ = missingPlatformIdentifier\n")
	m, observer, documents := liveLSPManager(t, root)
	// Observe a settled unused workspace, not just its immediate constructor state.
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	observer.mu.Lock()
	created, opened := observer.created, len(observer.opened)
	observer.mu.Unlock()
	if created != 0 || opened != 0 || len(m.ServerHealthSnapshots()) != 0 {
		t.Fatalf("enabled unused workspace launched work: created=%d opened=%d", created, opened)
	}
	snapshot := liveLSPOpen(t, documents, path)
	liveLSPWait(t, ctx, m, "initialize", m.ServerReady)
	liveLSPSemantics(t, ctx, m, path, good, 2, 20, 21)
	if diags := diagnosticList(m, path); len(diags) != 0 {
		t.Fatalf("normal Go source has unexpected diagnostics: %+v", diags)
	}
	bad := strings.Replace(good, "_ = answer()", "_ = missingLiveIdentifier", 1)
	snapshot = liveLSPReplace(t, documents, snapshot, bad)
	liveLSPWait(t, ctx, m, "unknown identifier diagnostic", func() bool {
		for _, d := range diagnosticList(m, path) {
			if d.Severity == model.DiagError && strings.Contains(d.Message, "missingLiveIdentifier") {
				return true
			}
		}
		return false
	})
	snapshot = liveLSPReplace(t, documents, snapshot, good)
	liveLSPWait(t, ctx, m, "corrected buffer clears errors", func() bool {
		facts, ok := m.FileDiagnostics(path)
		return ok && facts.Boundary.Source.Revision == snapshot.Revision && len(facts.Diagnostics) == 0
	})
	liveLSPSemantics(t, ctx, m, path, good, 2, 20, 21)
	liveLSPOpenOnly(t, observer, path)
	if diags := diagnosticList(m, sibling); len(diags) != 0 {
		t.Fatalf("unopened alternate-platform source gained diagnostics: %+v", diags)
	}
	liveLSPProcessSample(t, ctx)
}

func TestLiveLSPClangd(t *testing.T) {
	ctx := liveLSPContext(t)
	liveLSPExecutable(t, ctx, "clangd")
	root := t.TempDir()
	good := "// no includes or external dependencies\nint answer() { return 42; }\nint main() { return answer(); }\n"
	path := liveLSPWrite(t, root, "main.cpp", good)
	m, observer, documents := liveLSPManager(t, root)
	snapshot := liveLSPOpen(t, documents, path)
	liveLSPWait(t, ctx, m, "initialize", m.ServerReady)
	liveLSPSemantics(t, ctx, m, path, good, 2, 21, 22)
	snapshot = liveLSPReplace(t, documents, snapshot, strings.Replace(good, "return answer();", "return missingLiveIdentifier;", 1))
	liveLSPWait(t, ctx, m, "unknown identifier diagnostic", func() bool {
		for _, d := range diagnosticList(m, path) {
			if d.Severity == model.DiagError && strings.Contains(d.Message, "missingLiveIdentifier") {
				return true
			}
		}
		return false
	})
	snapshot = liveLSPReplace(t, documents, snapshot, good)
	liveLSPWait(t, ctx, m, "corrected C++ buffer clears errors", func() bool {
		facts, ok := m.FileDiagnostics(path)
		return ok && facts.Boundary.Source.Revision == snapshot.Revision && len(facts.Diagnostics) == 0
	})
	liveLSPOpenOnly(t, observer, path)
	liveLSPProcessSample(t, ctx)
}

func TestLiveLSPRepository(t *testing.T) {
	ctx := liveLSPContext(t)
	root := os.Getenv("CRUDE_LSP_REPO_ROOT")
	if root == "" {
		t.Skip("read-only repository smoke requires explicit CRUDE_LSP_REPO_ROOT")
	}
	liveLSPExecutable(t, ctx, "gopls")
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Locate main's declaration in the actual file without assuming repo layout.
	line, col := -1, -1
	for i, text := range strings.Split(string(data), "\n") {
		if at := strings.Index(text, "func main("); at >= 0 {
			line, col = i, at+5
			break
		}
	}
	if line < 0 {
		t.Fatal("root main.go has no main declaration")
	}
	m, observer, documents := liveLSPManager(t, root)
	liveLSPOpen(t, documents, path)
	liveLSPWait(t, ctx, m, "repository initialize", m.ServerReady)
	liveLSPWait(t, ctx, m, "repository main hover", func() bool {
		hover, err := m.RequestHover(ctx, path, line, col)
		return err == nil && strings.Contains(hover, "main")
	})
	liveLSPOpenOnly(t, observer, path)
	t.Logf("read-only repository errors summary: %+v; opened file diagnostics: %+v", m.ProjectInsights(), diagnosticList(m, path))
	if diags := diagnosticList(m, path); len(diags) != 0 {
		t.Fatalf("compiling repository main file has diagnostics: %+v", diags)
	}
	liveLSPProcessSample(t, ctx)
	if runtime.GOOS == "linux" {
		// First semantic response may precede indexing completion. Require two
		// quiet measurement windows rather than labelling startup CPU as idle.
		quiet := 0
		for sample := 0; sample < 8 && quiet < 2; sample++ {
			before := liveLSPUsage(t)
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			after := liveLSPUsage(t)
			var ticks, rss int64
			for pid, usage := range after {
				rss += usage[1]
				if old, ok := before[pid]; ok {
					ticks += max(0, usage[0]-old[0])
				}
			}
			t.Logf("idle sample %d (3s): owned CPU ticks=%d aggregate RSS KiB=%d", sample+1, ticks, rss)
			if ticks < 30 {
				quiet++
			} else {
				quiet = 0
			}
		}
		if quiet < 2 {
			t.Fatal("language server did not settle to bounded idle work after indexing")
		}
		if diags := diagnosticList(m, path); len(diags) != 0 {
			t.Fatalf("settled compiling main file diagnostics: %+v", diags)
		}
	}
}

func liveLSPUsage(t *testing.T) map[int][2]int64 {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid=,ppid=").Output()
	if err != nil {
		t.Fatal(err)
	}
	owned := map[int]bool{os.Getpid(): true}
	rows := strings.Split(string(out), "\n")
	for changed := true; changed; {
		changed = false
		for _, row := range rows {
			f := strings.Fields(row)
			if len(f) != 2 {
				continue
			}
			pid, _ := strconv.Atoi(f[0])
			parent, _ := strconv.Atoi(f[1])
			if owned[parent] && !owned[pid] {
				owned[pid] = true
				changed = true
			}
		}
	}
	usage := make(map[int][2]int64)
	for pid := range owned {
		if pid == os.Getpid() {
			continue
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		at := strings.LastIndex(string(data), ")")
		f := strings.Fields(string(data[at+1:]))
		if len(f) < 22 {
			continue
		}
		u, _ := strconv.ParseInt(f[11], 10, 64)
		s, _ := strconv.ParseInt(f[12], 10, 64)
		rss, _ := strconv.ParseInt(f[21], 10, 64)
		usage[pid] = [2]int64{u + s, rss * int64(os.Getpagesize()) / 1024}
	}
	return usage
}

// Informational snapshot, not a guessed RSS cap. Only descendants of this test
// process are reported; no process-name lookup or signals touch user servers.
func liveLSPProcessSample(t *testing.T, ctx context.Context) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	sampleCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(sampleCtx, "ps", "-eo", "pid=,ppid=,rss=,pcpu=,comm=").Output()
	if err != nil {
		t.Logf("owned child resource sample unavailable: %v", err)
		return
	}
	rows := strings.Split(strings.TrimSpace(string(out)), "\n")
	owned := map[int]bool{os.Getpid(): true}
	for changed := true; changed; {
		changed = false
		for _, row := range rows {
			fields := strings.Fields(row)
			if len(fields) < 5 {
				continue
			}
			pid, _ := strconv.Atoi(fields[0])
			parent, _ := strconv.Atoi(fields[1])
			if owned[parent] && !owned[pid] {
				owned[pid], changed = true, true
			}
		}
	}
	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) < 5 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		if owned[pid] && pid != os.Getpid() && fields[4] != "ps" {
			t.Logf("owned descendant pid=%s ppid=%s rss_kib=%s cpu_percent=%s command=%s", fields[0], fields[1], fields[2], fields[3], fields[4])
		}
	}
}
