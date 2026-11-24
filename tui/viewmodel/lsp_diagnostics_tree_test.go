package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/core/shellport"
	"crude/types/insight"
	"crude/types/setting"
	"crude/workspace"
)

type immediateTreeDiagRuntime struct {
	mu       sync.Mutex
	filePath string
	diags    []insight.LSPDiagnostic
	rootURIs []string
	specs    []setting.LSPServerSpec
	client   *immediateTreeDiagClient
}

type immediateTreeDiagClient struct {
	filePath    string
	diags       []insight.LSPDiagnostic
	mu          sync.Mutex
	publish     func(insight.DiagnosticPublication)
	boundary    insight.Boundary
	wireVersion int
	sequence    uint64
	admit       func() error
}

func (r *immediateTreeDiagRuntime) NewClient(spec setting.LSPServerSpec, rootURI string, startAdmission func() error) workspace.LSPClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rootURIs = append(r.rootURIs, rootURI)
	r.specs = append(r.specs, setting.CloneLSPServerSpec(spec))
	r.client = &immediateTreeDiagClient{filePath: r.filePath, diags: r.diags, admit: startAdmission}
	return r.client
}

func (c *immediateTreeDiagClient) OnDiagnostics(fn func(insight.DiagnosticPublication)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publish = fn
}

func (c *immediateTreeDiagClient) publishDiagnostics(diags []insight.LSPDiagnostic) {
	c.mu.Lock()
	fn := c.publish
	c.sequence++
	version := c.wireVersion
	publication := insight.DiagnosticPublication{Boundary: c.boundary, Sequence: c.sequence, WireVersion: version, ReportedVersion: &version, Diagnostics: append([]insight.LSPDiagnostic(nil), diags...)}
	c.mu.Unlock()
	if fn != nil {
		fn(publication)
	}
}

func (c *immediateTreeDiagClient) syncBoundary(boundary insight.Boundary) (int, error) {
	c.mu.Lock()
	c.boundary = boundary
	c.wireVersion++
	version := c.wireVersion
	diags := append([]insight.LSPDiagnostic(nil), c.diags...)
	c.mu.Unlock()
	c.publishDiagnostics(diags)
	return version, nil
}

func (c *immediateTreeDiagClient) Start(context.Context) error { return c.admit() }
func (c *immediateTreeDiagClient) Stop() error                 { return nil }
func (c *immediateTreeDiagClient) Done() <-chan struct{}       { return nil }
func (c *immediateTreeDiagClient) OpenFile(_ context.Context, _, _, _ string, boundary insight.Boundary) (int, error) {
	return c.syncBoundary(boundary)
}
func (c *immediateTreeDiagClient) UpdateFile(_ context.Context, _, _ string, boundary insight.Boundary) (int, error) {
	return c.syncBoundary(boundary)
}
func (c *immediateTreeDiagClient) CloseFile(context.Context, string)                     {}
func (c *immediateTreeDiagClient) NotifySave(context.Context, string, string, int, bool) {}
func (c *immediateTreeDiagClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (c *immediateTreeDiagClient) SyncFileFromDisk(context.Context, string, int) {}
func (c *immediateTreeDiagClient) RequestNavigation(context.Context, string, string, int, int, bool) (insight.LSPNavigationResponse, error) {
	return insight.LSPNavigationResponse{}, context.Canceled
}
func (c *immediateTreeDiagClient) RequestDocumentSymbols(context.Context, string) []insight.LSPSymbol {
	return nil
}
func (c *immediateTreeDiagClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (c *immediateTreeDiagClient) RequestSignatureHelp(context.Context, string, int, int, insight.LSPSignatureContext) (insight.LSPSignatureHelp, error) {
	return insight.LSPSignatureHelp{}, nil
}
func (c *immediateTreeDiagClient) RequestRename(context.Context, string, int, int, string) (insight.LSPRenameEdit, error) {
	return insight.LSPRenameEdit{}, nil
}
func (c *immediateTreeDiagClient) RequestCodeActions(context.Context, string, int, int, int, int) (insight.LSPCodeActionResponse, error) {
	return insight.LSPCodeActionResponse{}, context.Canceled
}
func (c *immediateTreeDiagClient) ResolveCodeAction(context.Context, string, int, []byte) (insight.LSPCodeActionResponse, error) {
	return insight.LSPCodeActionResponse{}, context.Canceled
}
func (c *immediateTreeDiagClient) RequestInlayHints(context.Context, string, insight.LSPInlayRange) (insight.LSPInlayResponse, error) {
	return insight.LSPInlayResponse{}, context.Canceled
}
func (c *immediateTreeDiagClient) ResolveInlayHint(context.Context, string, int, []byte) (insight.LSPInlayResponse, error) {
	return insight.LSPInlayResponse{}, context.Canceled
}
func (c *immediateTreeDiagClient) RequestCompletions(context.Context, string, int, int, insight.LSPCompletionContext) (insight.LSPCompletionResponse, error) {
	return insight.LSPCompletionResponse{}, context.Canceled
}
func (c *immediateTreeDiagClient) ResolveCompletion(context.Context, string, int, []byte) (insight.LSPCompletionResponse, error) {
	return insight.LSPCompletionResponse{}, context.Canceled
}

func TestLocalEditorCannotInventCanonicalDiagnosticAuthority(t *testing.T) {
	dir, mainGo := createDiagnosticsProject(t)
	app := newDiagnosticsTreeApp(t, dir)
	runtime := &immediateTreeDiagRuntime{
		filePath: mainGo,
		diags:    []insight.LSPDiagnostic{{Severity: workspace.DiagError, Message: "boom"}},
	}
	wsOf(app).LSPMgr.SetRuntime(runtime)

	_ = app.Init()
	if len(runtime.rootURIs) != 0 {
		t.Fatal("Init eagerly launched LSP")
	}
	_, err := app.openEditorTab(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.rootURIs) != 0 {
		t.Fatal("standalone buffer fabricated canonical LSP authority")
	}
	node := app.Tree.Model.NodeByPath(mainGo)
	if node == nil {
		t.Fatalf("node not found at %q", mainGo)
	}
	if node.DiagErrors != 0 || node.DiagnosticCount != 0 {
		t.Fatalf("tree diagnostics not replayed: total=%d errors=%d", node.DiagnosticCount, node.DiagErrors)
	}
}

func TestSwitchProjectLocalEditorCannotStartRawDiagnosticSource(t *testing.T) {
	oldDir, _ := createDiagnosticsProject(t)
	newDir, mainGo := createDiagnosticsProject(t)
	app := newDiagnosticsTreeApp(t, oldDir)
	detached := make(chan tea.Msg, 16)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { detached <- msg })
	defer restore()

	if err := app.SwitchProject(newDir); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for app.Tree.Model.NodeByPath(mainGo) == nil {
		select {
		case msg := <-detached:
			_, cmd := app.Update(msg)
			runAppCommand(t, app, cmd)
		case <-deadline:
			t.Fatal("replacement workspace tree never loaded")
		}
	}
	runtime := &immediateTreeDiagRuntime{
		filePath: mainGo,
		diags:    []insight.LSPDiagnostic{{Severity: workspace.DiagWarning, Message: "warn"}},
	}
	wsOf(app).LSPMgr.SetRuntime(runtime)

	app.startLSPForWorkspace()
	_, err := app.openEditorTab(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.rootURIs) != 0 {
		t.Fatal("workspace switch fabricated canonical LSP authority")
	}
	node := app.Tree.Model.NodeByPath(mainGo)
	if node == nil {
		t.Fatalf("node not found at %q", mainGo)
	}
	if node.DiagWarnings != 0 || node.DiagnosticCount != 0 {
		t.Fatalf("new workspace tree diagnostics not updated: total=%d warnings=%d", node.DiagnosticCount, node.DiagWarnings)
	}
}

func createDiagnosticsProject(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainGo, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, mainGo
}

func newDiagnosticsTreeApp(t *testing.T, dir string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	ws := workspace.NewWorkspace(dir, "test", "test", setting.Settings{}, stubLogger{})
	app := NewApp(shellport.Local(ws), nil)
	t.Cleanup(func() {
		app.StopAsyncIO()
		app.StopLSP()
		ws.Close()
	})
	return app
}

func assertLSPStartedForRoot(t *testing.T, runtime *immediateTreeDiagRuntime, dir string) {
	t.Helper()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.rootURIs) != 1 {
		t.Fatalf("started roots = %v, specs = %+v; want exactly one", runtime.rootURIs, runtime.specs)
	}
	got, err := protocol.DocumentURI(runtime.rootURIs[0]).Path()
	if err != nil {
		t.Fatalf("invalid LSP root URI %q: %v", runtime.rootURIs[0], err)
	}
	got = filepath.Clean(got)
	want := filepath.Clean(dir)
	if got != want {
		t.Fatalf("LSP started for %q, want workspace root %q", got, want)
	}
}

func (*immediateTreeDiagClient) AwaitDiagnostics(context.Context, string) (insight.DiagnosticPublication, bool) {
	return insight.DiagnosticPublication{}, true
}

func (*immediateTreeDiagClient) AssistanceCapabilities(string) insight.LSPAssistanceCapabilities {
	return insight.LSPAssistanceCapabilities{Completion: true, CompletionTriggers: []string{"."}, SignatureHelp: true, SignatureTriggers: []string{"(", ","}}
}
