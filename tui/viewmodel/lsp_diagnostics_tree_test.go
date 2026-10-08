package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	ttypes "crude/tui/types"
	model "crude/types"
	"crude/workspace"
)

type immediateTreeDiagRuntime struct {
	mu       sync.Mutex
	filePath string
	diags    []workspace.LSPDiagnostic
	rootURIs []string
	client   *immediateTreeDiagClient
}

type immediateTreeDiagClient struct {
	filePath string
	diags    []workspace.LSPDiagnostic
}

func (r *immediateTreeDiagRuntime) NewClient(_ model.LSPServerSpec, rootURI string) workspace.LSPClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rootURIs = append(r.rootURIs, rootURI)
	r.client = &immediateTreeDiagClient{filePath: r.filePath, diags: r.diags}
	return r.client
}

func (c *immediateTreeDiagClient) OnDiagnostics(fn func(string, []workspace.LSPDiagnostic)) {
	fn(c.filePath, c.diags)
}

func (c *immediateTreeDiagClient) Start(context.Context) error { return nil }
func (c *immediateTreeDiagClient) Stop()                       {}
func (c *immediateTreeDiagClient) Done() <-chan struct{}       { return nil }
func (c *immediateTreeDiagClient) OpenFile(context.Context, string, string, string) {
}
func (c *immediateTreeDiagClient) UpdateFile(context.Context, string, string)            {}
func (c *immediateTreeDiagClient) CloseFile(context.Context, string)                     {}
func (c *immediateTreeDiagClient) NotifySave(context.Context, string, string, int, bool) {}
func (c *immediateTreeDiagClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (c *immediateTreeDiagClient) SyncFileFromDisk(context.Context, string, int) {}
func (c *immediateTreeDiagClient) RequestDefinition(context.Context, string, int, int) []workspace.LSPLocation {
	return nil
}
func (c *immediateTreeDiagClient) FindReferences(context.Context, string, int, int, bool) []workspace.LSPLocation {
	return nil
}
func (c *immediateTreeDiagClient) RequestDocumentSymbols(context.Context, string) []workspace.LSPSymbol {
	return nil
}
func (c *immediateTreeDiagClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (c *immediateTreeDiagClient) RequestRename(context.Context, string, int, int, string) (interface{}, error) {
	return nil, nil
}
func (c *immediateTreeDiagClient) RequestCompletions(context.Context, string, int, int) []workspace.CompletionItem {
	return nil
}

func TestInitWiresDiagnosticsBeforeWorkspaceLSPStart(t *testing.T) {
	dir, mainGo := createDiagnosticsProject(t)
	app := newDiagnosticsTreeApp(t, dir)
	runtime := &immediateTreeDiagRuntime{
		filePath: mainGo,
		diags:    []workspace.LSPDiagnostic{{Severity: workspace.DiagError, Message: "boom"}},
	}
	app.LSPMgr.SetRuntime(runtime)

	_ = app.Init()
	if len(runtime.rootURIs) != 0 {
		t.Fatal("Init eagerly launched LSP")
	}
	_, err := app.openEditorTab(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	awaitTreeLSP(t, app, mainGo)

	assertLSPStartedForRoot(t, runtime, dir)
	node := app.Tree.Model.NodeByPath(mainGo)
	if node == nil {
		t.Fatalf("node not found at %q", mainGo)
	}
	if node.DiagErrors != 1 || node.DiagnosticCount != 1 {
		t.Fatalf("tree diagnostics not replayed: total=%d errors=%d", node.DiagnosticCount, node.DiagErrors)
	}
}

func TestSwitchProjectRewiresDiagnosticsForNewLSPManager(t *testing.T) {
	oldDir, _ := createDiagnosticsProject(t)
	newDir, mainGo := createDiagnosticsProject(t)
	app := newDiagnosticsTreeApp(t, oldDir)

	if err := app.SwitchProject(newDir); err != nil {
		t.Fatal(err)
	}
	runtime := &immediateTreeDiagRuntime{
		filePath: mainGo,
		diags:    []workspace.LSPDiagnostic{{Severity: workspace.DiagWarning, Message: "warn"}},
	}
	app.LSPMgr.SetRuntime(runtime)

	app.startLSPForWorkspace()
	_, err := app.openEditorTab(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	awaitTreeLSP(t, app, mainGo)

	assertLSPStartedForRoot(t, runtime, newDir)
	node := app.Tree.Model.NodeByPath(mainGo)
	if node == nil {
		t.Fatalf("node not found at %q", mainGo)
	}
	if node.DiagWarnings != 1 || node.DiagnosticCount != 1 {
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
	ws := workspace.NewWorkspace(dir, "test", "test", ttypes.Settings{}, stubLogger{})
	app := NewApp(ws, nil)
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
		t.Fatalf("started roots = %v, want exactly one", runtime.rootURIs)
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

func awaitTreeLSP(t *testing.T, app *App, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !app.LSPMgr.HasFileDiagnostics(path) {
		if time.Now().After(deadline) {
			t.Fatal("diagnostics never arrived")
		}
		time.Sleep(time.Millisecond)
	}
	app.drainLSPDiagnostics()
}
