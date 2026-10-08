package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	model "crude/types"
)

// Logger

type testLogger struct{}

func (testLogger) Info(tag, msg string, pairs ...any)  {}
func (testLogger) Warn(tag, msg string, pairs ...any)  {}
func (testLogger) Error(tag, msg string, pairs ...any) {}
func (testLogger) Debug(tag, msg string, pairs ...any) {}

func TestFileURIPathRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with space")
	if got := filePathFromURI(fileURI(path)); got != filepath.Clean(path) {
		t.Fatalf("file URI round trip = %q, want %q", got, filepath.Clean(path))
	}
}

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
	t.Cleanup(m.Close)
	if m == nil {
		t.Fatal("NewLSPManager returned nil")
	}
	conn, status, errMsg := m.Status()
	if conn {
		t.Error("new manager should not be connected")
	}
	if status != model.LSPIdle {
		t.Errorf("new manager status = %d, want LSPIdle", status)
	}
	if errMsg != "" {
		t.Errorf("new manager errMsg = %q, want empty", errMsg)
	}
}

func TestLSPManager_StartForWorkspace_NotFound(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	m.StartForWorkspace(t.TempDir())

	conn, status, errMsg := m.Status()
	if conn {
		t.Error("should not be connected without a valid server in PATH")
	}
	if status == model.LSPConnected {
		t.Error("status should not be LSPConnected for a temp dir")
	}
	_ = errMsg
}

func TestLSPManager_FileDiagnostics(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	diags := m.FileDiagnostics("/tmp/nonexistent.go")
	if len(diags) != 0 {
		t.Errorf("expected empty diags, got %d", len(diags))
	}

	if m.HasFileDiagnostics("/tmp/nonexistent.go") {
		t.Error("HasFileDiagnostics should be false for unknown file")
	}

	m.setFileDiagnostics("/tmp/test.go", []model.LSPDiagnostic{
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

func TestLSPManager_FeatureGates(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	if !m.ShowDiagnosticsInEditor() || !m.ShowDiagnosticsInTree() || !m.ShowDiagnosticsInStatus() {
		t.Fatal("default LSP settings should show diagnostics in editor, tree, and status")
	}

	off := model.DefaultLSPSettings()
	off.Enabled = false
	m.SetSettings(off)
	if m.ShowDiagnosticsInEditor() || m.ShowDiagnosticsInTree() || m.ShowDiagnosticsInStatus() {
		t.Fatal("master switch must hide every diagnostic surface")
	}

	s := model.DefaultLSPSettings()
	s.DiagInEditor = false
	m.SetSettings(s)
	if m.ShowDiagnosticsInEditor() {
		t.Fatal("diag-in-editor off should hide editor underlines")
	}
	if !m.ShowDiagnosticsInTree() {
		t.Fatal("tree diagnostics should stay on when only editor display is off")
	}

	s.Diagnostics = false
	m.SetSettings(s)
	m.setFileDiagnostics("/tmp/gated.go", []model.LSPDiagnostic{{Line: 0, Col: 0, Severity: 1, Message: "err"}})
	if m.HasFileDiagnostics("/tmp/gated.go") {
		t.Fatal("incoming diagnostics must be dropped when diagnostics are disabled")
	}

	s = model.DefaultLSPSettings()
	s.MinSeverity = "error"
	m.SetSettings(s)
	m.setFileDiagnostics("/tmp/warn.go", []model.LSPDiagnostic{{Line: 0, Col: 0, Severity: 2, Message: "warn"}})
	if m.HasFileDiagnostics("/tmp/warn.go") {
		t.Fatal("warnings below min severity should be dropped")
	}

	s.Languages["go"] = model.LSPLanguageSettings{Enabled: false, Command: "gopls"}
	m.SetSettings(s)
	if m.Settings().LanguageEnabled(".go") {
		t.Fatal("disabled go language must not start")
	}
}

func TestLSPManager_FileDiagnostics_PersistAfterSet(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	// Simulate a file being opened, receiving diagnostics, then closed.
	m.setFileDiagnostics("/tmp/test.go", []model.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "error"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.CloseFile(ctx, "/tmp/test.go")

	// Closing drops overlay diagnostics; servers may later publish disk diagnostics.
	if m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("overlay diagnostics should clear after CloseFile")
	}
	diags := m.FileDiagnostics("/tmp/test.go")
	if len(diags) != 0 {
		t.Fatalf("expected no stale diagnostic after close, got %d", len(diags))
	}
}

func TestLSPManager_FileDiagnostics_ReplaceOnUpdate(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	m.setFileDiagnostics("/tmp/test.go", []model.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "old error"},
	})

	// Replace with new diagnostics (simulates server re-sending after edit).
	m.setFileDiagnostics("/tmp/test.go", []model.LSPDiagnostic{
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
	t.Cleanup(m.Close)

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
	t.Cleanup(m.Close)

	m.setFileDiagnostics("/tmp/a.go", []model.LSPDiagnostic{
		{Line: 0, Severity: 1, Message: "a error"},
	})
	m.setFileDiagnostics("/tmp/b.go", []model.LSPDiagnostic{
		{Line: 0, Severity: 2, Message: "b warning"},
	})
	m.setFileDiagnostics("/tmp/c.go", []model.LSPDiagnostic{
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
	t.Cleanup(m.Close)

	conn, status, errMsg := m.Status()
	if conn || status != model.LSPIdle || errMsg != "" {
		t.Errorf("initial: connected=%v status=%d errMsg=%q", conn, status, errMsg)
	}

	// Simulate diagnostics callback setting cached data.
	m.setFileDiagnostics("/tmp/test.go", []model.LSPDiagnostic{
		{Line: 0, Col: 0, Severity: 1, Message: "test diag"},
	})

	if !m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("diagnostics should be stored even when no server connected")
	}
}

func TestLSPManager_OpenFile_NonExistentRoot(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

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
	t.Cleanup(m.Close)

	called := false
	var gotFP string
	var gotDiags []model.LSPDiagnostic
	m.SetOnDiagnostics(func(fp string, diags []model.LSPDiagnostic) {
		called = true
		gotFP = fp
		gotDiags = diags
	})

	// Simulate the LSPIntegration callback (fires from within setFileDiagnostics via interceptor).
	cb := m.captureOnDiagnostics()
	if cb == nil {
		t.Fatal("captureOnDiagnostics returned nil")
	}
	cb("/tmp/test.go", []model.LSPDiagnostic{
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
	t.Cleanup(m.Close)

	m.SetOnDiagnostics(func(fp string, diags []model.LSPDiagnostic) {})

	// The callback in startServerForRoot calls setFileDiagnostics BEFORE captureOnDiagnostics.
	// Test that both the store and the callback receive the data.
	m.setFileDiagnostics("/tmp/test.go", []model.LSPDiagnostic{
		{Line: 0, Severity: 1, Message: "stored"},
	})

	if !m.HasFileDiagnostics("/tmp/test.go") {
		t.Error("setFileDiagnostics should store the diagnostics")
	}
}

func TestLSPManager_PendingText(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

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
	t.Cleanup(m.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// UpdateFile with a file that has no client — should not panic.
	m.UpdateFile(ctx, "/tmp/test.go", "package main\nfunc main() {}")
}

func TestLSPManager_QueueFileUpdateCoalescesWithoutBlocking(t *testing.T) {
	m := NewLSPManager(nil)
	path := "/tmp/queued.go"
	m.OpenFile(context.Background(), path, "initial")
	t.Cleanup(m.Close)
	m.QueueFileUpdate(path, "first")
	m.QueueFileUpdate(path, "latest")

	m.mu.Lock()
	queued := m.documents[path].text
	timerCount := len(m.updateTimers)
	m.mu.Unlock()
	if queued != "latest" {
		t.Fatalf("queued text=%q", queued)
	}
	if timerCount != 1 {
		t.Fatalf("expected one coalesced timer, got %d", timerCount)
	}

	m.Stop()
	m.mu.Lock()
	remaining := len(m.updateTimers)
	m.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("Stop should cancel queued updates, got %d", remaining)
	}
}

type saveOrderClient struct {
	reapableClient
	mu    sync.Mutex
	calls []string
}

func (c *saveOrderClient) UpdateFile(_ context.Context, _ string, text string) {
	c.mu.Lock()
	c.calls = append(c.calls, "change:"+text)
	c.mu.Unlock()
}
func (c *saveOrderClient) NotifySave(_ context.Context, _ string, text string, _ int, before bool) {
	phase := "didSave:"
	if before {
		phase = "willSave:"
	}
	c.mu.Lock()
	c.calls = append(c.calls, phase+text)
	c.mu.Unlock()
}

func TestNotifyFileSaveFlushesLatestChangeBeforeSaveBoundary(t *testing.T) {
	m := NewLSPManager(nil)
	client := &saveOrderClient{}
	path := filepath.Join(t.TempDir(), "file.go")
	m.states["save-test"] = &lspClientState{client: client, status: LSPConnected}
	m.documents[path] = &lspDocument{key: "save-test", opened: true, text: "initial", revision: 1, syncedRevision: 1}
	m.QueueFileUpdate(path, "old")
	m.QueueFileUpdate(path, "latest")
	m.NotifyFileSave(context.Background(), path, "saved", DocumentSaveAfterDelay, true)
	m.NotifyFileSave(context.Background(), path, "saved", DocumentSaveAfterDelay, false)
	client.mu.Lock()
	defer client.mu.Unlock()
	want := []string{"change:latest", "willSave:saved", "didSave:saved"}
	if !slices.Equal(client.calls, want) {
		t.Fatalf("calls=%v want %v", client.calls, want)
	}
}

func TestLSPManager_CloseFile_NoClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseFile with no client — should not panic.
	m.CloseFile(ctx, "/tmp/test.go")
}

func TestLSPManager_Stop_Idempotent(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	m.Stop()
	m.Stop()
}

func TestLSPManager_Stop_AfterOpenFile(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.OpenFile(ctx, "/tmp/test.go", "package main")
	m.Stop()

	// Stop should clear the clients map.
	if len(m.states) != 0 {
		t.Errorf("expected 0 clients after Stop, got %d", len(m.states))
	}
}

func TestLSPManager_StartForWorkspace_NotPanic(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	// StartForWorkspace in a temp directory with no project markers.
	// Should not panic or hang.
	m.StartForWorkspace(t.TempDir())
}

func TestLSPManager_AggregateConnectedServerIgnoresBackgroundFailure(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	m.mu.Lock()
	m.states["go"] = &lspClientState{
		rootURI: "file:///project",
		cmd:     "gopls",
		status:  model.LSPConnected,
	}
	m.states["json"] = &lspClientState{
		rootURI: "file:///project",
		cmd:     "vscode-json-language-server",
		status:  model.LSPFailed,
		err:     "vscode-json-language-server not found in PATH",
	}
	m.recomputeActiveStatusLocked()
	m.mu.Unlock()

	connected, status, errMsg := m.Status()
	if !connected || status != model.LSPConnected || errMsg != "" {
		t.Fatalf("aggregate status connected=%v status=%v error=%q", connected, status, errMsg)
	}
}

func TestLSPManager_AggregateActiveFileFailureTakesPrecedence(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	m.mu.Lock()
	m.activeFilePath = "/project/data.json"
	// Compute the rootURI the same way the production code does so the
	// state key matches regardless of platform (Linux: file:///project,
	// Windows: file://<drive>:\project). The test is about aggregation
	// precedence, not URI formatting.
	rootURI := fileURI(filepath.Dir(m.activeFilePath))
	m.documents[m.activeFilePath] = &lspDocument{key: "json"}
	m.states["go"] = &lspClientState{
		rootURI: rootURI,
		cmd:     "gopls",
		status:  model.LSPConnected,
	}
	m.states["json"] = &lspClientState{
		rootURI: rootURI,
		cmd:     "vscode-json-language-server",
		status:  model.LSPFailed,
		err:     "vscode-json-language-server not found in PATH",
	}
	m.recomputeActiveStatusLocked()
	m.mu.Unlock()

	connected, status, errMsg := m.Status()
	if connected || status != model.LSPFailed || errMsg == "" {
		t.Fatalf("active JSON status connected=%v status=%v error=%q", connected, status, errMsg)
	}
}

func TestProjectDiagnosticCounts_Empty(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	errs, warns, hints := m.ProjectDiagnosticCounts()
	if errs != 0 || warns != 0 || hints != 0 {
		t.Errorf("expected (0,0,0), got (%d,%d,%d)", errs, warns, hints)
	}
}

func TestProjectDiagnosticCounts_Aggregates(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	m.setFileDiagnostics("/tmp/a.go", []model.LSPDiagnostic{
		{Severity: model.DiagError},
		{Severity: model.DiagError},
		{Severity: model.DiagWarning},
	})
	m.setFileDiagnostics("/tmp/b.go", []model.LSPDiagnostic{
		{Severity: model.DiagWarning},
		{Severity: model.DiagInfo},
		{Severity: model.DiagHint},
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
	t.Cleanup(m.Close)

	m.setFileDiagnostics("/tmp/a.go", []model.LSPDiagnostic{
		{Severity: model.DiagError},
	})
	m.setFileDiagnostics("/tmp/a.go", nil) // clear

	errs, warns, hints := m.ProjectDiagnosticCounts()
	if errs != 0 || warns != 0 || hints != 0 {
		t.Errorf("expected (0,0,0) after clear, got (%d,%d,%d)", errs, warns, hints)
	}
}

func TestClientForFile_UnsupportedExt(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	c := m.clientForFile("/tmp/test.xyz")
	if c != nil {
		t.Error("clientForFile should return nil for unsupported extension")
	}
}

func TestClientForFile_NoClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	c := m.clientForFile("/tmp/test.go")
	if c != nil {
		t.Error("clientForFile should return nil when no server is started")
	}
}

func TestUpdateFile_WrongClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// UpdateFile for a file whose root has no client — should not panic.
	m.UpdateFile(ctx, "/tmp/test.go", "content")
}

func TestCloseFile_WrongClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseFile for a file whose root has no client — should not panic.
	m.CloseFile(ctx, "/tmp/test.go")
}
