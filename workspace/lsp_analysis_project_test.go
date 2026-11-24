package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/internal/fileuri"
	"crude/types/insight"
	"crude/types/setting"
)

// Verify coverage through the workspace, not merely a capability response.
// A Go type error in a file never opened by the manager must appear and clear.
func TestGoplsProjectPushWithNoOpenDocument(t *testing.T) {
	testNativeFile(t, "gopls", "closed.go", "package coverage\nvar _ int = \"bad\"\n", "package coverage\nvar _ int = 1\n", map[string]string{"go.mod": "module example.org/coverage\n\ngo 1.22\n"})
}

func TestPyrightWorkspaceDiagnostics(t *testing.T) {
	testNativeFile(t, "pyright-langserver", "broken.py", "value: int = 'bad'\n", "value: int = 10\n", nil)
}

func TestMarksmanProjectDiagnostics(t *testing.T) {
	testNativeFile(t, "marksman", "broken.md", "# Broken\n\n[[missing-target]]\n", "# Fixed\n", map[string]string{".marksman.toml": ""})
}

func testNativeFile(t *testing.T, server, file, broken, fixed string, markers map[string]string) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts an installed language server")
	}
	if _, err := exec.LookPath(server); err != nil {
		t.Skip(server + " not installed")
	}
	root := t.TempDir()
	for name, data := range markers {
		createFile(t, filepath.Join(root, name), data)
	}
	path := filepath.Join(root, file)
	createFile(t, path, broken)
	m := NewLSPManager(nil)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	t.Cleanup(m.Close)
	s := m.Settings()
	s.ProjectCheck = setting.ProjectCheckAlways
	m.SetSettings(s)
	m.RefreshAssembly()
	wait := func(want int) {
		t.Helper()
		end := time.Now().Add(20 * time.Second)
		for time.Now().Before(end) {
			batch, ok := m.FileDiagnostics(path)
			if ok && batch.FromProjectCheck() && len(batch.Diagnostics) == want && strings.HasPrefix(batch.Boundary.Source.DocumentID, projectFactPrefix) {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("%s: want %d native problems, got %+v; servers %+v", server, want, m.ProjectInsights(), m.ServerHealthSnapshots())
	}
	wait(1)
	if err := os.WriteFile(path, []byte(fixed), 0600); err != nil {
		t.Fatal(err)
	}
	if server == "marksman" { // This release does not implement didChangeWatchedFiles. Refresh rereads the folder.
		m.Refresh(root)
		awaitLSP(t, func() bool { return m.ProjectInsights().Errors == 0 })
		return
	}
	m.NotifyWatchedFilesChanged(t.Context(), path, 2)
	wait(0)
	if len(m.ProjectInsights().Analysis) != 0 {
		t.Fatal("native server got a fallback counter")
	}
}

type analysisTestRuntime struct {
	*lifecycleRuntime
	entered chan string
	release chan struct{}
}
type analysisTestClient struct {
	*lifecycleClient
	r *analysisTestRuntime
}

func (r *analysisTestRuntime) NewClient(s setting.LSPServerSpec, root string, admit func() error) LSPClient {
	return &analysisTestClient{r.lifecycleRuntime.NewClient(s, root, admit).(*lifecycleClient), r}
}
func (c *analysisTestClient) AwaitDiagnostics(ctx context.Context, path string) (insight.DiagnosticPublication, bool) {
	select {
	case c.r.entered <- path:
	case <-ctx.Done():
		return insight.DiagnosticPublication{}, false
	}
	select {
	case <-ctx.Done():
		return insight.DiagnosticPublication{}, false
	case <-c.r.release:
	}
	if strings.Contains(filepath.Base(path), "silent") {
		return insight.DiagnosticPublication{}, false
	}
	p := c.publication(path, nil)
	c.emit(p)
	return p, true
}

func TestForcedFallbackCountsAnswersNotSubmissions(t *testing.T) {
	t.Setenv("CRUDE_TEST_LSP_FORCE_FALLBACK", "1")
	m, base, root := probeManager(t)
	r := &analysisTestRuntime{base, make(chan string, 32), make(chan struct{})}
	m.SetRuntime(r)
	open := filepath.Join(root, "open.go")
	source := editorSource(t, m, open, "package main\n")
	awaitLSP(t, func() bool { return len(base.snapshot()) == 1 })
	client := base.snapshot()[0]
	waitDiagnosticSend(t, m, client, source)
	client.emit(client.publication(open, nil))
	paths := []string{open}
	for _, name := range []string{"a-silent.go", "b-silent.go", "c.go", "d.go", "e.go", "f.go"} {
		p := filepath.Join(root, name)
		createFile(t, p, "package main\n")
		paths = append(paths, p)
	}
	other := filepath.Join(root, "last.py")
	createFile(t, other, "x=1\n")
	startProjectCheck(t, m, root, map[string][]string{"go": paths, "python": {other}})
	for range projectCheckBatch {
		select {
		case <-r.entered:
		case <-time.After(4 * time.Second):
			t.Fatal("fallback waited for the last file before submitting the next")
		}
	}
	rows := m.ProjectInsights().Analysis
	if len(rows) != 2 || rows[0].Checked != 1 || rows[0].Total != 7 || rows[1].Total != 1 {
		t.Fatalf("queued/submitted files counted as checked or open file excluded: %+v", rows)
	}
	close(r.release)
	awaitProjectCheckEnd(t, m)
	rows = m.ProjectInsights().Analysis
	if len(rows) != 1 || rows[0].Checked != 5 || rows[0].Unchecked != 2 || rows[0].Total != 7 {
		t.Fatalf("silence hid later files or counted as an answer: %+v", rows)
	}
	if got := m.ProjectInsights().DiagnosticsObserved; got != 6 {
		t.Fatalf("open and clean reports = %d want 6", got)
	}
}

// Identical commands in different roots do not confer coverage on one another.
// A server's unsolicited publication covers that path only. The other root's
// project protocol must not be replaced with background document traffic.
type mixedRootRuntime struct {
	*workspacePullRuntime
	native string
}

func (r mixedRootRuntime) NewClient(spec setting.LSPServerSpec, root string, admit func() error) LSPClient {
	if root == r.native {
		return r.workspacePullRuntime.NewClient(spec, root, admit)
	}
	return r.lifecycleRuntime.NewClient(spec, root, admit)
}
func TestProjectCoverageBelongsToExactRoot(t *testing.T) {
	m, base, root := probeManager(t)
	native, other := filepath.Join(root, "one"), filepath.Join(root, "two")
	for _, dir := range []string{native, other} {
		writeTree(t, filepath.Join(dir, "pyrightconfig.json"), "{}")
	}
	a, b, c := filepath.Join(native, "native.py"), filepath.Join(other, "reported.py"), filepath.Join(other, "fallback.py")
	for _, p := range []string{a, b, c} {
		createFile(t, p, "x=0\n")
	}
	w := &workspacePullRuntime{lifecycleRuntime: base, reports: map[string]string{a: "native report"}}
	m.SetRuntime(mixedRootRuntime{w, fileuri.FromPath(native)})
	editorSource(t, m, filepath.Join(other, "editor.py"), "x=1\n")
	awaitLSP(t, func() bool {
		if len(base.snapshot()) != 1 {
			return false
		}
		base.snapshot()[0].mu.Lock()
		defer base.snapshot()[0].mu.Unlock()
		return base.snapshot()[0].unopened != nil
	})
	base.snapshot()[0].emitUnopened(b, insight.LSPDiagnostic{Message: "individual report", Severity: 1})
	startProjectCheck(t, m, root, map[string][]string{"python": {a, b, c}})
	awaitProjectFact(t, m, a)
	awaitProjectFact(t, m, c)
	awaitProjectCheckEnd(t, m)
	for _, p := range []string{a, b} {
		for _, client := range base.snapshot() {
			if countCalls(client.snapshot(), "open:"+filepath.Base(p)+":") > 0 {
				t.Fatalf("opened already-covered %s", p)
			}
		}
	}
	batch, _ := m.FileDiagnostics(c)
	if batch.Boundary.Context.Root != other {
		t.Fatalf("fallback used wrong root: %+v", batch.Boundary)
	}
}

func TestProjectDeclarationRequiresMarkersOfTheRightKind(t *testing.T) {
	root := t.TempDir()
	uri := fileuri.FromPath(root)
	spec, ok := setting.DefaultLSP().ResolveServer(filepath.Join(root, "notes.md"))
	if !ok {
		t.Fatal("no Markdown server")
	}
	if declaresProject(spec, uri) {
		t.Fatal("unmarked folder claimed as a project")
	}
	marker := filepath.Join(root, ".git")
	createFile(t, marker, "gitdir: elsewhere\n")
	if declaresProject(spec, uri) {
		t.Fatal("a worktree pointer is not a .git directory")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	mkdir(t, marker)
	if !declaresProject(spec, uri) {
		t.Fatal("recognized project did not get native reporting")
	}
}
