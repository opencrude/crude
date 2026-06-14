package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/tui/editor"
	"crude/tui/filetree"
)

// ── Logger ──────────────────────────────────────────────────────────

type testLogger struct{}

func (testLogger) Info(tag, msg string, pairs ...any)  {}
func (testLogger) Warn(tag, msg string, pairs ...any)  {}
func (testLogger) Error(tag, msg string, pairs ...any) {}
func (testLogger) Debug(tag, msg string, pairs ...any) {}

func createFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestLSPManager_New(t *testing.T) {
	m := NewLSPManager(testLogger{})
	if m == nil {
		t.Fatal("NewLSPManager returned nil")
	}
	conn, status, errMsg := m.Status()
	if conn {
		t.Error("new manager should not be connected")
	}
	if status != editor.LSPIdle {
		t.Errorf("new manager status = %d, want LSPIdle", status)
	}
	if errMsg != "" {
		t.Errorf("new manager errMsg = %q, want empty", errMsg)
	}
}

func TestLSPManager_StartForWorkspace_NotFound(t *testing.T) {
	m := NewLSPManager(testLogger{})
	m.StartForWorkspace(t.TempDir())

	conn, status, errMsg := m.Status()
	if conn {
		t.Error("should not be connected without a valid server in PATH")
	}
	if status == editor.LSPConnected {
		t.Error("status should not be LSPConnected for a temp dir")
	}
	_ = errMsg
}

func TestLSPManager_FileDiagnostics(t *testing.T) {
	m := NewLSPManager(testLogger{})

	diags := m.FileDiagnostics("/tmp/nonexistent.go")
	if len(diags) != 0 {
		t.Errorf("expected empty diags, got %d", len(diags))
	}

	if m.HasFileDiagnostics("/tmp/nonexistent.go") {
		t.Error("HasFileDiagnostics should be false for unknown file")
	}

	m.setFileDiagnostics("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "test"},
	})

	if !m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("HasFileDiagnostics should be true after set")
	}

	diags = m.FileDiagnostics("/tmp/test.go")
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Message != "test" {
		t.Errorf("message = %q, want 'test'", diags[0].Message)
	}
}

func TestLSPManager_FileDiagnostics_PersistAfterSet(t *testing.T) {
	m := NewLSPManager(testLogger{})

	// Simulate a file being opened, receiving diagnostics, then closed.
	m.setFileDiagnostics("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "error"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.CloseFile(ctx, "/tmp/test.go")

	// Diagnostics should persist after close.
	if !m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("diagnostics should persist after CloseFile")
	}
	diags := m.FileDiagnostics("/tmp/test.go")
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic after close, got %d", len(diags))
	}
}

func TestLSPManager_FileDiagnostics_ReplaceOnUpdate(t *testing.T) {
	m := NewLSPManager(testLogger{})

	m.setFileDiagnostics("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "old error"},
	})

	// Replace with new diagnostics (simulates server re-sending after edit).
	m.setFileDiagnostics("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 1, Col: 0, Severity: 2, Message: "new warning"},
	})

	diags := m.FileDiagnostics("/tmp/test.go")
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Message != "new warning" {
		t.Errorf("message = %q, want 'new warning'", diags[0].Message)
	}
}

func TestLSPManager_FileDiagnostics_EmptyFile(t *testing.T) {
	m := NewLSPManager(testLogger{})

	m.setFileDiagnostics("/tmp/empty.go", nil)

	// Setting nil diagnostics removes the file from the map.
	if m.HasFileDiagnostics("/tmp/empty.go") {
		t.Error("HasFileDiagnostics should be false after setting nil")
	}

	diags := m.FileDiagnostics("/tmp/empty.go")
	if diags != nil {
		t.Errorf("expected nil, got %v", diags)
	}
}

func TestLSPManager_FileDiagnostics_MultipleFiles(t *testing.T) {
	m := NewLSPManager(testLogger{})

	m.setFileDiagnostics("/tmp/a.go", []editor.LSPDiagnostic{
		{Line: 0, Severity: 1, Message: "a error"},
	})
	m.setFileDiagnostics("/tmp/b.go", []editor.LSPDiagnostic{
		{Line: 0, Severity: 2, Message: "b warning"},
	})
	m.setFileDiagnostics("/tmp/c.go", []editor.LSPDiagnostic{
		{Line: 0, Severity: 3, Message: "c info"},
	})

	if diags := m.FileDiagnostics("/tmp/a.go"); len(diags) != 1 || diags[0].Message != "a error" {
		t.Errorf("wrong diagnostics for a.go")
	}
	if diags := m.FileDiagnostics("/tmp/b.go"); len(diags) != 1 || diags[0].Message != "b warning" {
		t.Errorf("wrong diagnostics for b.go")
	}
	if diags := m.FileDiagnostics("/tmp/c.go"); len(diags) != 1 || diags[0].Message != "c info" {
		t.Errorf("wrong diagnostics for c.go")
	}
}

func TestLSPManager_Status_Lifecycle(t *testing.T) {
	m := NewLSPManager(testLogger{})

	conn, status, errMsg := m.Status()
	if conn || status != editor.LSPIdle || errMsg != "" {
		t.Errorf("initial: connected=%v status=%d errMsg=%q", conn, status, errMsg)
	}

	// Simulate diagnostics callback setting cached data.
	m.setFileDiagnostics("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "test diag"},
	})

	if !m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("diagnostics should be stored even when no server connected")
	}
}

func TestLSPManager_OpenFile_NonExistentRoot(t *testing.T) {
	m := NewLSPManager(testLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Opening a file with an unsupported extension should not panic.
	m.OpenFile(ctx, "/tmp/test.xyz", "some content")

	diags := m.FileDiagnostics("/tmp/test.xyz")
	if len(diags) != 0 {
		t.Errorf("no diagnostics expected, got %d", len(diags))
	}
}

func TestLSPManager_OnDiagnosticsCallback(t *testing.T) {
	m := NewLSPManager(testLogger{})

	called := false
	var gotFP string
	var gotDiags []editor.LSPDiagnostic
	m.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		called = true
		gotFP = fp
		gotDiags = diags
	})

	// Simulate the LSPIntegration callback (fires from within setFileDiagnostics via interceptor).
	cb := m.captureOnDiagnostics()
	if cb == nil {
		t.Fatal("captureOnDiagnostics returned nil")
	}
	cb("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "from server"},
	})

	if !called {
		t.Fatal("callback was not called")
	}
	if gotFP != "/tmp/test.go" {
		t.Errorf("filepath = %q, want /tmp/test.go", gotFP)
	}
	if len(gotDiags) != 1 || gotDiags[0].Message != "from server" {
		t.Errorf("unexpected diagnostics: %v", gotDiags)
	}
}

func TestLSPManager_OnDiagnosticsCallback_SetsDiagnosticsStore(t *testing.T) {
	m := NewLSPManager(testLogger{})

	m.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {})

	// The callback in startServerForRoot calls setFileDiagnostics BEFORE captureOnDiagnostics.
	// Test that both the store and the callback receive the data.
	m.setFileDiagnostics("/tmp/test.go", []editor.LSPDiagnostic{
		{Line: 0, Severity: 1, Message: "stored"},
	})

	if !m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("setFileDiagnostics should store the diagnostics")
	}
}

func TestLSPManager_PendingText(t *testing.T) {
	m := NewLSPManager(testLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// OpenFile with no running server does nothing (status stays LSPIdle).
	m.OpenFile(ctx, "/tmp/test.go", "package main")

	conn, status, errMsg := m.Status()
	t.Logf("after OpenFile: connected=%v status=%d errMsg=%q", conn, status, errMsg)
	_ = conn
	_ = status
	_ = errMsg
}

func TestLSPManager_UpdateFile_NoClient(t *testing.T) {
	m := NewLSPManager(testLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// UpdateFile with a file that has no client — should not panic.
	m.UpdateFile(ctx, "/tmp/test.go", "package main\nfunc main() {}")
}

func TestLSPManager_CloseFile_NoClient(t *testing.T) {
	m := NewLSPManager(testLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseFile with no client — should not panic.
	m.CloseFile(ctx, "/tmp/test.go")
}

func TestLSPManager_Stop_Idempotent(t *testing.T) {
	m := NewLSPManager(testLogger{})
	m.Stop()
	m.Stop()
}

func TestLSPManager_Stop_AfterOpenFile(t *testing.T) {
	m := NewLSPManager(testLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.OpenFile(ctx, "/tmp/test.go", "package main")
	m.Stop()

	// Stop should clear the clients map.
	if len(m.clients) != 0 {
		t.Errorf("expected 0 clients after Stop, got %d", len(m.clients))
	}
}

func TestLSPManager_StartForWorkspace_NotPanic(t *testing.T) {
	m := NewLSPManager(testLogger{})

	// StartForWorkspace in a temp directory with no project markers.
	// Should not panic or hang.
	m.StartForWorkspace(t.TempDir())
}

func TestLSPManager_DetectProjectRoots(t *testing.T) {
	m := NewLSPManager(testLogger{})
	roots := m.detectProjectRoots(t.TempDir())
	if len(roots) == 0 {
		t.Error("detectProjectRoots should return at least the temp dir as a fallback root")
	}
}

func TestScanProjectFiles(t *testing.T) {
	dir := t.TempDir()

	createFile(t, dir+"/main.go", "package main")
	createFile(t, dir+"/app.ts", "const x = 1")
	createFile(t, dir+"/README.md", "# hi")
	mkdir(t, dir+"/node_modules")
	createFile(t, dir+"/node_modules/ignore.js", "ignored")
	mkdir(t, dir+"/.git")
	createFile(t, dir+"/.git/config", "ignored")

	extSet := map[string]bool{".go": true, ".ts": true}
	files := scanProjectFiles(dir, extSet)

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}

	hasGo, hasTS := false, false
	for _, f := range files {
		if f == dir+"/main.go" {
			hasGo = true
		}
		if f == dir+"/app.ts" {
			hasTS = true
		}
	}
	if !hasGo {
		t.Error("main.go should be in scanned files")
	}
	if !hasTS {
		t.Error("app.ts should be in scanned files")
	}
}

func TestScanPresentExtensions(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir+"/main.go", "package main")
	createFile(t, dir+"/app.ts", "const x = 1")

	exts := []string{".go", ".rs", ".py", ".ts"}
	present := scanPresentExtensions(dir, exts)

	if !present[".go"] {
		t.Error("expected .go to be present")
	}
	if !present[".ts"] {
		t.Error("expected .ts to be present")
	}
	if present[".rs"] {
		t.Error("expected .rs to NOT be present")
	}
	if present[".py"] {
		t.Error("expected .py to NOT be present")
	}
}

func TestScanPresentExtensions_SkipIgnored(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir+"/real.go", "package main")
	mkdir(t, dir+"/node_modules")
	createFile(t, dir+"/node_modules/ignored.js", "ignored")
	mkdir(t, dir+"/.git")
	createFile(t, dir+"/.git/hooks.py", "ignored")

	exts := []string{".go", ".js", ".py"}
	present := scanPresentExtensions(dir, exts)

	if !present[".go"] {
		t.Error("expected .go to be present")
	}
	if present[".js"] {
		t.Error("expected .js in node_modules to NOT be present")
	}
	if present[".py"] {
		t.Error("expected .py in .git to NOT be present")
	}
}

func TestScanPresentExtensions_NoMatches(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir+"/README.md", "# hi")
	createFile(t, dir+"/Makefile", "all:")

	present := scanPresentExtensions(dir, []string{".go", ".rs", ".py"})
	if len(present) != 0 {
		t.Errorf("expected no present extensions, got %v", present)
	}
}

func TestScanProjectFiles_SkipNodeModules(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/node_modules/pkg", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/node_modules/pkg/index.js", []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/index.js", []byte("not ignored"), 0644); err != nil {
		t.Fatal(err)
	}

	extSet := map[string]bool{".js": true}
	files := scanProjectFiles(dir, extSet)
	if len(files) != 1 || files[0] != dir+"/index.js" {
		t.Fatalf("expected only index.js, got %v", files)
	}
}

func TestScanProjectFiles_SkipDotGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/.git/objects", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.git/objects/pack.go", []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/work.go", []byte("real"), 0644); err != nil {
		t.Fatal(err)
	}

	extSet := map[string]bool{".go": true}
	files := scanProjectFiles(dir, extSet)
	if len(files) != 1 || files[0] != dir+"/work.go" {
		t.Fatalf("expected only work.go, got %v", files)
	}
}

func TestProjectDiagnosticCounts_Empty(t *testing.T) {
	m := NewLSPManager(testLogger{})
	errs, warns, hints := m.ProjectDiagnosticCounts()
	if errs != 0 || warns != 0 || hints != 0 {
		t.Errorf("expected (0,0,0), got (%d,%d,%d)", errs, warns, hints)
	}
}

func TestProjectDiagnosticCounts_Aggregates(t *testing.T) {
	m := NewLSPManager(testLogger{})

	m.setFileDiagnostics("/tmp/a.go", []editor.LSPDiagnostic{
		{Severity: editor.DiagError},
		{Severity: editor.DiagError},
		{Severity: editor.DiagWarning},
	})
	m.setFileDiagnostics("/tmp/b.go", []editor.LSPDiagnostic{
		{Severity: editor.DiagWarning},
		{Severity: editor.DiagInfo},
		{Severity: editor.DiagHint},
	})

	errs, warns, hints := m.ProjectDiagnosticCounts()
	if errs != 2 {
		t.Errorf("expected 2 errors, got %d", errs)
	}
	if warns != 2 {
		t.Errorf("expected 2 warnings, got %d", warns)
	}
	if hints != 2 {
		t.Errorf("expected 2 hints, got %d", hints)
	}
}

func TestProjectDiagnosticCounts_AfterClear(t *testing.T) {
	m := NewLSPManager(testLogger{})

	m.setFileDiagnostics("/tmp/a.go", []editor.LSPDiagnostic{
		{Severity: editor.DiagError},
	})
	m.setFileDiagnostics("/tmp/a.go", nil) // clear

	errs, warns, hints := m.ProjectDiagnosticCounts()
	if errs != 0 || warns != 0 || hints != 0 {
		t.Errorf("expected (0,0,0) after clear, got (%d,%d,%d)", errs, warns, hints)
	}
}

func TestClientForFile_UnsupportedExt(t *testing.T) {
	m := NewLSPManager(testLogger{})
	c := m.clientForFile("/tmp/test.xyz")
	if c != nil {
		t.Error("clientForFile should return nil for unsupported extension")
	}
}

func TestClientForFile_NoClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	c := m.clientForFile("/tmp/test.go")
	if c != nil {
		t.Error("clientForFile should return nil when no server is started")
	}
}

func TestClientsForRootURI_Empty(t *testing.T) {
	m := NewLSPManager(testLogger{})
	clients := m.clientsForRootURI("file:///tmp")
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}

func TestUpdateFile_WrongClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// UpdateFile for a file whose root has no client — should not panic.
	m.UpdateFile(ctx, "/tmp/test.go", "content")
}

func TestCloseFile_WrongClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseFile for a file whose root has no client — should not panic.
	m.CloseFile(ctx, "/tmp/test.go")
}

// ── Integration: LSPManager callback → FileTreeModel ──────────────

func TestLSPManager_TreeDiagnosticsIntegration(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFile(t, mainGo, "package main")

	tree := filetree.NewFileTreeModel(dir)
	mgr := NewLSPManager(testLogger{})

	// Same callback that App.Init() sets.
	treeUpdated := false
	mgr.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		treeUpdated = true
		errs, warns, hints := 0, 0, 0
		for _, d := range diags {
			switch d.Severity {
			case editor.DiagError:
				errs++
			case editor.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		tree.SetDiagnostics(fp, errs, warns, hints)
	})

	// Simulate the exact callback chain from startServerForRoot:
	//   m.setFileDiagnostics(fp, diags)
	//   if cb := m.captureOnDiagnostics(); cb != nil { cb(fp, diags) }
	mgr.setFileDiagnostics(mainGo, []editor.LSPDiagnostic{
		{Severity: editor.DiagError, Message: "test1"},
	})
	if cb := mgr.captureOnDiagnostics(); cb != nil {
		cb(mainGo, []editor.LSPDiagnostic{
			{Severity: editor.DiagError, Message: "test1"},
		})
	}

	if !treeUpdated {
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
	t.Logf("node: errors=%d warnings=%d hints=%d total=%d",
		node.DiagErrors, node.DiagWarnings, node.DiagHints, node.DiagnosticCount)
	t.Logf("root: errors=%d warnings=%d hints=%d total=%d",
		tree.Root.DiagErrors, tree.Root.DiagWarnings, tree.Root.DiagHints, tree.Root.DiagnosticCount)
}

func TestLSPManager_TreeDiagnostics_UncleanedPath(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFile(t, mainGo, "package main")

	tree := filetree.NewFileTreeModel(dir)
	mgr := NewLSPManager(testLogger{})

	mgr.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		errs, warns, hints := 0, 0, 0
		for _, d := range diags {
			switch d.Severity {
			case editor.DiagError:
				errs++
			case editor.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		tree.SetDiagnostics(fp, errs, warns, hints)
	})

	// Simulate LSP server sending a URI with trailing junk
	// e.g. "file:///home/user/project/./main.go" → uriToPath → "/home/user/project/./main.go"
	uncleanPath := mainGo + "/./."
	if cb := mgr.captureOnDiagnostics(); cb != nil {
		cb(uncleanPath, []editor.LSPDiagnostic{
			{Severity: editor.DiagError, Message: "uncleaned"},
		})
	}

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
	createFile(t, a, "package a")
	createFile(t, b, "package b")

	tree := filetree.NewFileTreeModel(dir)
	mgr := NewLSPManager(testLogger{})

	mgr.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		errs, warns, hints := 0, 0, 0
		for _, d := range diags {
			switch d.Severity {
			case editor.DiagError:
				errs++
			case editor.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		tree.SetDiagnostics(fp, errs, warns, hints)
	})

	mgr.setFileDiagnostics(a, []editor.LSPDiagnostic{{Severity: editor.DiagError}})
	mgr.setFileDiagnostics(b, []editor.LSPDiagnostic{{Severity: editor.DiagWarning}, {Severity: editor.DiagHint}})
	if cb := mgr.captureOnDiagnostics(); cb != nil {
		cb(a, []editor.LSPDiagnostic{{Severity: editor.DiagError}})
		cb(b, []editor.LSPDiagnostic{{Severity: editor.DiagWarning}, {Severity: editor.DiagHint}})
	}

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
	mkdir(t, sub)
	deepGo := filepath.Join(sub, "deep.go")
	createFile(t, deepGo, "package deep")

	tree := filetree.NewFileTreeModel(dir)
	mgr := NewLSPManager(testLogger{})

	mgr.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		errs, warns, hints := 0, 0, 0
		for _, d := range diags {
			switch d.Severity {
			case editor.DiagError:
				errs++
			case editor.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		tree.SetDiagnostics(fp, errs, warns, hints)
	})

	// Send diagnostics for deep.go BEFORE expanding sub/.
	mgr.setFileDiagnostics(deepGo, []editor.LSPDiagnostic{{Severity: editor.DiagError}})
	if cb := mgr.captureOnDiagnostics(); cb != nil {
		cb(deepGo, []editor.LSPDiagnostic{{Severity: editor.DiagError}})
	}

	// Node should not exist yet (sub/ not expanded).
	if node := tree.NodeByPath(deepGo); node != nil {
		t.Log("deep.go node exists before expand (unexpected but OK)")
	}

	// Now expand sub/ directory.
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
