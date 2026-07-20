package filetree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	model "crude/types"
	"crude/workspace"
)

type treeLSPTestLogger struct{}

func (treeLSPTestLogger) Info(tag, msg string, pairs ...any)  {}
func (treeLSPTestLogger) Warn(tag, msg string, pairs ...any)  {}
func (treeLSPTestLogger) Error(tag, msg string, pairs ...any) {}
func (treeLSPTestLogger) Debug(tag, msg string, pairs ...any) {}

type treeLSPTestClient struct {
	onDiagnostics func(string, []workspace.LSPDiagnostic)
}

func (c *treeLSPTestClient) OnDiagnostics(fn func(string, []workspace.LSPDiagnostic)) {
	c.onDiagnostics = fn
}

func (c *treeLSPTestClient) Start(context.Context) error { return nil }
func (c *treeLSPTestClient) Stop()                       {}
func (c *treeLSPTestClient) OpenFile(context.Context, string, string, string) {
}
func (c *treeLSPTestClient) UpdateFile(context.Context, string, string) {}
func (c *treeLSPTestClient) CloseFile(context.Context, string)          {}
func (c *treeLSPTestClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (c *treeLSPTestClient) SyncFileFromDisk(context.Context, string, int) {}
func (c *treeLSPTestClient) RequestDefinition(context.Context, string, int, int) []workspace.LSPLocation {
	return nil
}
func (c *treeLSPTestClient) FindReferences(context.Context, string, int, int, bool) []workspace.LSPLocation {
	return nil
}
func (c *treeLSPTestClient) RequestDocumentSymbols(context.Context, string) []workspace.LSPSymbol {
	return nil
}
func (c *treeLSPTestClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (c *treeLSPTestClient) RequestRename(context.Context, string, int, int, string) (interface{}, error) {
	return nil, nil
}
func (c *treeLSPTestClient) RequestCompletions(context.Context, string, int, int) []workspace.CompletionItem {
	return nil
}

func (c *treeLSPTestClient) EmitDiagnostics(filePath string, diags []workspace.LSPDiagnostic) {
	if c.onDiagnostics != nil {
		c.onDiagnostics(filePath, diags)
	}
}

type treeLSPTestRuntime struct {
	command string
	client  *treeLSPTestClient
}

func (r *treeLSPTestRuntime) NewClient(serverCmd string, serverArgs []string, rootURI string) workspace.LSPClient {
	r.client = &treeLSPTestClient{}
	return r.client
}

func (r *treeLSPTestRuntime) ServerConfigForExt(ext string) (string, []string, bool) {
	if ext != ".go" {
		return "", nil, false
	}
	return r.command, nil, true
}

func (r *treeLSPTestRuntime) RootURIForFile(filePath string) string {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}
	return "file://" + filepath.Dir(absPath)
}

func (r *treeLSPTestRuntime) LanguageID(ext string) string {
	if ext == ".go" {
		return "go"
	}
	return ""
}

func (r *treeLSPTestRuntime) AllServerConfigs() map[string]workspace.LSPServerConfig {
	return map[string]workspace.LSPServerConfig{
		".go": {Cmd: r.command},
	}
}

func newTreeDiagnosticsManager(t *testing.T, tree *FileTreeModel) (*workspace.LSPManager, *treeLSPTestRuntime, *bool) {
	t.Helper()
	runtime := &treeLSPTestRuntime{command: os.Args[0]}
	mgr := workspace.NewLSPManager(treeLSPTestLogger{}, runtime)
	treeUpdated := false
	mgr.SetOnDiagnostics(func(filePath string, diags []workspace.LSPDiagnostic) {
		treeUpdated = true
		errs, warns, hints := 0, 0, 0
		for _, diag := range diags {
			switch diag.Severity {
			case model.DiagError:
				errs++
			case model.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		tree.SetDiagnostics(filePath, errs, warns, hints)
	})
	t.Cleanup(mgr.Stop)
	return mgr, runtime, &treeUpdated
}

func openTreeDiagnosticsFile(t *testing.T, mgr *workspace.LSPManager, runtime *treeLSPTestRuntime, filePath string) {
	t.Helper()
	mgr.OpenFile(context.Background(), filePath, "")
	if runtime.client == nil {
		t.Fatal("LSP runtime did not create a client")
	}
}

func TestLSPManager_TreeDiagnosticsIntegration(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFileForTest(t, mainGo, "package main")

	tree := NewFileTreeModel(dir)
	mgr, runtime, treeUpdated := newTreeDiagnosticsManager(t, tree)
	openTreeDiagnosticsFile(t, mgr, runtime, mainGo)
	runtime.client.EmitDiagnostics(mainGo, []workspace.LSPDiagnostic{
		{Severity: model.DiagError, Message: "test1"},
	})

	if !*treeUpdated {
		t.Fatal("tree update callback was not invoked")
	}

	node := tree.NodeByPath(mainGo)
	if node == nil {
		t.Fatalf("node not found at %q", mainGo)
	}
	if node.DiagErrors != 1 {
		t.Errorf("node DiagErrors = %d, want 1", node.DiagErrors)
	}
	if node.DiagnosticCount != 1 {
		t.Errorf("node DiagnosticCount = %d, want 1", node.DiagnosticCount)
	}
	if tree.Root.DiagErrors != 1 {
		t.Errorf("root DiagErrors = %d, want 1", tree.Root.DiagErrors)
	}
}

func TestLSPManager_TreeDiagnostics_UncleanedPath(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFileForTest(t, mainGo, "package main")

	tree := NewFileTreeModel(dir)
	mgr, runtime, _ := newTreeDiagnosticsManager(t, tree)
	openTreeDiagnosticsFile(t, mgr, runtime, mainGo)

	// Simulate an LSP server sending a path with trailing components.
	runtime.client.EmitDiagnostics(mainGo+"/./.", []workspace.LSPDiagnostic{
		{Severity: model.DiagError, Message: "uncleaned"},
	})

	node := tree.NodeByPath(mainGo)
	if node == nil {
		t.Fatalf("node not found at %q", mainGo)
	}
	if node.DiagErrors != 1 {
		t.Errorf("node DiagErrors = %d, want 1 (uncleaned path)", node.DiagErrors)
	}
}

func TestLSPManager_TreeDiagnostics_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	createFileForTest(t, a, "package a")
	createFileForTest(t, b, "package b")

	tree := NewFileTreeModel(dir)
	mgr, runtime, _ := newTreeDiagnosticsManager(t, tree)
	openTreeDiagnosticsFile(t, mgr, runtime, a)
	runtime.client.EmitDiagnostics(a, []workspace.LSPDiagnostic{{Severity: model.DiagError}})
	runtime.client.EmitDiagnostics(b, []workspace.LSPDiagnostic{{Severity: model.DiagWarning}, {Severity: model.DiagHint}})

	na := tree.NodeByPath(a)
	nb := tree.NodeByPath(b)
	if na == nil || nb == nil {
		t.Fatal("nodes not found")
	}
	if na.DiagErrors != 1 {
		t.Errorf("a.go DiagErrors = %d, want 1", na.DiagErrors)
	}
	if nb.DiagWarnings != 1 {
		t.Errorf("b.go DiagWarnings = %d, want 1", nb.DiagWarnings)
	}
	if nb.DiagHints != 1 {
		t.Errorf("b.go DiagHints = %d, want 1", nb.DiagHints)
	}
	if tree.Root.DiagnosticCount != 3 {
		t.Errorf("root DiagnosticCount = %d, want 3", tree.Root.DiagnosticCount)
	}
}

func TestLSPManager_TreeDiagnostics_AfterExpand(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	mkdirForTest(t, sub)
	deepGo := filepath.Join(sub, "deep.go")
	createFileForTest(t, deepGo, "package deep")

	tree := NewFileTreeModel(dir)
	mgr, runtime, _ := newTreeDiagnosticsManager(t, tree)
	openTreeDiagnosticsFile(t, mgr, runtime, deepGo)

	// Send diagnostics for deep.go before expanding sub/.
	runtime.client.EmitDiagnostics(deepGo, []workspace.LSPDiagnostic{{Severity: model.DiagError}})
	if node := tree.NodeByPath(deepGo); node != nil {
		t.Log("deep.go node exists before expand (unexpected but OK)")
	}

	tree.Expand(sub)

	node := tree.NodeByPath(deepGo)
	if node == nil {
		t.Fatal("deep.go node not found after expand")
	}
	if node.DiagErrors != 1 {
		t.Errorf("deep.go DiagErrors after expand = %d, want 1", node.DiagErrors)
	}
	subNode := tree.NodeByPath(sub)
	if subNode == nil {
		t.Fatal("sub node not found")
	}
	if subNode.DiagnosticCount != 1 {
		t.Errorf("sub DiagnosticCount after expand = %d, want 1", subNode.DiagnosticCount)
	}
}
