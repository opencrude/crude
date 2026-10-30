package workspace

import (
	"context"
	"os"
	"path/filepath"
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

	s = model.DefaultLSPSettings()
	s.Languages["go"] = model.LSPLanguageSettings{Enabled: false, Command: "gopls"}
	m.SetSettings(s)
	if m.Settings().LanguageEnabled(".go") {
		t.Fatal("disabled go language must not start")
	}
}

func TestLSPManager_QueueFileUpdateCoalescesWithoutBlocking(t *testing.T) {
	m := NewLSPManager(nil)
	path := filepath.Join(t.TempDir(), "queued.go")
	editorSource(t, m, path, "initial")
	t.Cleanup(m.Close)
	editorSource(t, m, path, "first")
	editorSource(t, m, path, "latest")

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

func TestLSPManager_CloseFile_NoClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseFile with no client must not panic.
	m.CloseFile(ctx, "/tmp/test.go")
}

func TestLSPManager_Stop_Idempotent(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	m.Stop()
	m.Stop()
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

func TestCloseFile_WrongClient(t *testing.T) {
	m := NewLSPManager(testLogger{})
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseFile for a file whose root has no client must not panic.
	m.CloseFile(ctx, "/tmp/test.go")
}
