package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/types"
)

func TestLSPIdleReasonsExplainLazyStartupAndPolicy(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	if got := m.ProjectInsights(); got.IdleStatus != "Waiting for source file" || !strings.Contains(got.IdleReason, "Open a supported source file") {
		t.Fatalf("enabled unused workspace reason=%+v", got)
	}
	settings := types.DefaultLSPSettings()
	settings.Enabled = false
	m.SetSettings(settings)
	if got := m.ProjectInsights(); got.IdleStatus != "Language intelligence off" {
		t.Fatalf("disabled reason=%+v", got)
	}
	path := filepath.Join(root, "main.go")
	editorSource(t, m, path, "package main\n")
	if len(runtime.snapshot()) != 0 {
		t.Fatal("disabled editor launched a server")
	}
	settings.Enabled, settings.StartOnOpen = true, false
	m.SetSettings(settings)
	if got := m.ProjectInsights(); got.IdleStatus != "Automatic start off" || !strings.Contains(got.IdleReason, "Start Language Servers") {
		t.Fatalf("manual startup reason=%+v", got)
	}
	m.StartForWorkspace(root)
	awaitLSP(t, m.ServerReady)
	if got := m.ProjectInsights(); got.IdleStatus != "" || got.IdleReason != "" {
		t.Fatalf("connected server retained idle explanation=%+v", got)
	}
	m.Stop()
	if got := m.ProjectInsights(); got.IdleStatus != "Language servers paused" {
		t.Fatalf("paused reason=%+v", got)
	}
}

func TestLSPSettingsEnableStartsAlreadyOpenDocumentWithoutReopen(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	settings := types.DefaultLSPSettings()
	settings.Enabled = false
	m.SetSettings(settings)
	path := filepath.Join(root, "main.go")
	editorSource(t, m, path, "package main\nvar _ = acceptedUnsavedText\n")
	settings.Enabled = true
	m.SetSettings(settings)
	awaitLSP(t, func() bool {
		clients := runtime.snapshot()
		return len(clients) == 1 && len(clients[0].snapshot()) == 1
	})
	if got := runtime.snapshot()[0].snapshot()[0]; got != "open:main.go:go:package main\nvar _ = acceptedUnsavedText\n" {
		t.Fatalf("enable lost retained buffer=%q", got)
	}
}

func TestLSPIdleResumeWithoutDocumentsRefreshesReason(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	m.Stop()
	m.StartForWorkspace(root)
	if got := m.ProjectInsights(); got.IdleStatus != "Waiting for source file" {
		t.Fatalf("resume retained paused reason=%+v", got)
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatal("resume eagerly launched a server without documents")
	}
}

func TestLSPUnsupportedDocumentExplainsNoMatchingServer(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	editorSource(t, m, filepath.Join(root, "readme.md"), "# No configured Markdown server\n")
	if got := m.ProjectInsights(); got.IdleStatus != "No matching language server" || !strings.Contains(got.IdleReason, "Settings → Language Servers") {
		t.Fatalf("unmatched source reason=%+v", got)
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatal("unsupported file launched an arbitrary server")
	}
}

func assertLSPBroadcast(t *testing.T, m *LSPManager, mutate func()) {
	t.Helper()
	version, wake := m.Changed()
	_, second := m.Changed()
	if wake == nil || second != wake {
		t.Fatal("unchanged LSP owner did not reuse broadcast")
	}
	mutate()
	select {
	case <-wake:
	default:
		t.Fatal("LSP mutation did not broadcast")
	}
	next, nextWake := m.Changed()
	if next <= version || nextWake == wake {
		t.Fatal("LSP mutation did not advance fenced version")
	}
}

func TestLSPChangedDiagnosticsClearAndStop(t *testing.T) {
	m, client, snapshot := diagnosticFixture(t)
	assertLSPBroadcast(t, m, func() {
		client.emit(client.publication(snapshot.Path, []LSPDiagnostic{{Severity: DiagError, Message: "bad"}}))
	})
	if m.ProjectInsights().Errors != 1 {
		t.Fatal("diagnostic absent from projection")
	}
	assertLSPBroadcast(t, m, func() { m.InvalidateFile(snapshot.Path) })
	if batch, ok := m.FileDiagnostics(snapshot.Path); !ok || batch.State != types.InsightStale || m.ProjectInsights().Errors != 0 {
		t.Fatal("diagnostic retirement lost provenance or retained current counts")
	}
	assertLSPBroadcast(t, m, m.Stop)
	if m.ProjectInsights().IdleStatus != "Language servers paused" {
		t.Fatal("stop not projected")
	}
}

func TestLSPChangedBusyCancellationAndPanic(t *testing.T) {
	m := NewLSPManager(nil)
	defer m.Close()
	ctx, cancel := context.WithCancel(t.Context())
	entered, done := make(chan struct{}), make(chan struct{})
	_, wake := m.Changed()
	go func() {
		defer close(done)
		_ = m.trackInflight(func() error { close(entered); <-ctx.Done(); return ctx.Err() })
	}()
	<-entered
	select {
	case <-wake:
	default:
		t.Fatal("query busy did not broadcast")
	}
	if !m.ProjectInsights().ServerBusy {
		t.Fatal("query missing busy state")
	}
	_, wake = m.Changed()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("query cancellation did not finish")
	}
	select {
	case <-wake:
	default:
		t.Fatal("query cancellation did not broadcast")
	}
	if m.ProjectInsights().ServerBusy {
		t.Fatal("cancelled query remained busy")
	}
	_, wake = m.Changed()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("query panic swallowed")
			}
		}()
		_ = m.trackInflight(func() error { panic("query failure") })
	}()
	select {
	case <-wake:
	default:
		t.Fatal("query failure did not broadcast")
	}
	if m.ProjectInsights().ServerBusy {
		t.Fatal("failed query remained busy")
	}
}

type idleSymbolsClient struct{ LSPClient }

func (idleSymbolsClient) Stop() error { return nil }

func (idleSymbolsClient) RequestDocumentSymbols(context.Context, string) []LSPSymbol {
	return []LSPSymbol{{Name: "visibleSymbol"}}
}

func TestLSPChangedSymbolsAndInvalidation(t *testing.T) {
	m := NewLSPManager(nil)
	defer m.Close()
	trustAnalysis(t, m, t.TempDir())
	path := filepath.Clean("symbols.go")
	// Install a connected, already-synchronized transport so the real request
	// path caches symbols, without starting a process or a lifecycle worker.
	m.mu.Lock()
	m.documents[path] = &lspDocument{key: "server", editor: true, opened: true, revision: 1, syncedRevision: 1}
	m.states["server"] = &lspClientState{status: LSPConnected, client: idleSymbolsClient{}}
	m.mu.Unlock()
	assertLSPBroadcast(t, m, func() { m.RequestDocumentSymbols(t.Context(), path) })
	if m.ProjectInsights().SymbolsCount != 1 || m.DocumentSymbols(path)[0].Name != "visibleSymbol" {
		t.Fatal("symbol result not retained")
	}
	assertLSPBroadcast(t, m, func() { m.InvalidateFile(path) })
	if m.ProjectInsights().SymbolsCount != 0 {
		t.Fatal("invalidated symbols remained visible")
	}
}
