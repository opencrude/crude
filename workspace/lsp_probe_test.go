package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	model "crude/types"
)

func editorDisabledSettings() model.LSPSettings {
	settings := model.DefaultLSPSettings()
	settings.Enabled = false
	return settings
}

// An agent probe works while the editor's own LSP is off, starts only the one
// server it needs, and closes the document it borrowed afterwards.
func TestProbeServesAgentsWhileEditorLSPIsOff(t *testing.T) {
	m, r, root := lifecycleManager(t)
	m.SetSettings(editorDisabledSettings())
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\nvar x = y\n")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	diags, settled, view, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(diags) != 1 || view.BufferDiffers {
		t.Fatalf("ProbeDiagnostics = %+v settled=%v view=%+v err=%v", diags, settled, view, err)
	}
	clients := r.snapshot()
	if len(clients) != 1 {
		t.Fatalf("probe started %d servers", len(clients))
	}
	awaitLSP(t, func() bool { return slices.Contains(clients[0].snapshot(), "close:main.go") })
	if got := clients[0].snapshot(); got[0] != "open:main.go:go:package main\nvar x = y\n" || got[1] != "await:main.go" {
		t.Fatalf("server saw %q", got)
	}
	m.mu.Lock()
	_, retained := m.documents[path]
	m.mu.Unlock()
	if retained {
		t.Fatal("probe-only document outlived its request")
	}
}

// The editor's unsaved buffer is what the agent is told about, and releasing
// the probe never closes the editor's own view.
func TestProbeUsesEditorBufferAndKeepsEditorView(t *testing.T) {
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	m.OpenFile(t.Context(), path, "package main\nfunc unsaved() {}\n")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) == 1 })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, _, view, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !view.BufferDiffers {
		t.Fatalf("probe view=%+v err=%v; want the dirty editor buffer flagged", view, err)
	}
	time.Sleep(20 * time.Millisecond)
	if calls := r.snapshot()[0].snapshot(); slices.Contains(calls, "close:main.go") {
		t.Fatalf("probe release closed the editor's document: %q", calls)
	}
}

// With the editor's LSP off, a file the editor has open is opened for the
// probe and closed again, so the server can go idle.
func TestProbeClosesEditorHeldViewWhenEditorLSPIsOff(t *testing.T) {
	m, r, root := lifecycleManager(t)
	m.SetSettings(editorDisabledSettings())
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	m.OpenFile(t.Context(), path, "package main\n")
	time.Sleep(20 * time.Millisecond)
	if len(r.snapshot()) != 0 {
		t.Fatal("editor document started a server while editor LSP was off")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, _, _, err := m.ProbeDiagnostics(ctx, path); err != nil {
		t.Fatal(err)
	}
	c := r.snapshot()[0]
	awaitLSP(t, func() bool { return slices.Contains(c.snapshot(), "close:main.go") })
	m.mu.Lock()
	doc := m.documents[path]
	m.mu.Unlock()
	if doc == nil || !doc.editor || doc.key != "" || doc.opened {
		t.Fatalf("editor document after probe = %+v", doc)
	}
}

func TestProbeRespectsHumanStopAndFeatureGates(t *testing.T) {
	m, r, root := lifecycleManager(t)
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	m.Stop()
	if _, _, _, err := m.ProbeDiagnostics(t.Context(), path); !errors.Is(err, ErrLSPStopped) {
		t.Fatalf("probe after a human stop = %v", err)
	}
	if len(r.snapshot()) != 0 {
		t.Fatal("probe restarted servers the human stopped")
	}
	m.StartForWorkspace(root)
	settings := model.DefaultLSPSettings()
	settings.References = false
	m.SetSettings(settings)
	if _, _, err := m.ProbeReferences(t.Context(), path, 0, 0); err == nil || !strings.Contains(err.Error(), "Find references is turned off") {
		t.Fatalf("gated references = %v", err)
	}
	if _, _, _, err := m.ProbeDiagnostics(t.Context(), filepath.Join(root, "notes.unknownext")); err == nil || !strings.Contains(err.Error(), "no enabled language server") {
		t.Fatalf("unsupported language = %v", err)
	}
}

// A failed start is reported at once, not after the caller's deadline.
func TestProbeReportsStartFailureImmediately(t *testing.T) {
	m, _, root := lifecycleManager(t)
	m.SetRuntime(failingRuntime{})
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	_, _, _, err := m.ProbeDiagnostics(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "failed to start") || time.Since(start) > 3*time.Second {
		t.Fatalf("start failure = %v after %s", err, time.Since(start))
	}
}

type failingRuntime struct{}

func (failingRuntime) NewClient(model.LSPServerSpec, string) LSPClient {
	return &failingClient{reapableClient{done: make(chan struct{})}}
}

type failingClient struct{ reapableClient }

func (*failingClient) Start(context.Context) error {
	return errors.New("exec: \"gopls\": executable file not found in $PATH")
}

type probeSyncClient struct {
	reapableClient
	mu      sync.Mutex
	text    string
	opened  bool
	entered chan string
	release chan struct{}
	closed  chan struct{}
}

func (c *probeSyncClient) send(ctx context.Context, operation, text string) {
	c.entered <- operation + ":" + text
	select {
	case <-c.release:
	case <-ctx.Done():
		return
	}
	c.mu.Lock()
	c.text, c.opened = text, true
	c.mu.Unlock()
}

func (c *probeSyncClient) OpenFile(ctx context.Context, _, _, text string) {
	c.send(ctx, "open", text)
}

func (c *probeSyncClient) UpdateFile(ctx context.Context, _, text string) {
	c.send(ctx, "change", text)
}

func (c *probeSyncClient) CloseFile(context.Context, string) {
	c.mu.Lock()
	c.opened = false
	c.mu.Unlock()
	if c.closed != nil {
		c.closed <- struct{}{}
	}
}

func (c *probeSyncClient) AwaitDiagnostics(context.Context, string) ([]LSPDiagnostic, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return nil, false
	}
	return []LSPDiagnostic{{Message: c.text}}, true
}

type probeSyncRuntime struct{ client *probeSyncClient }

func (r probeSyncRuntime) NewClient(model.LSPServerSpec, string) LSPClient { return r.client }

// A connected process is not enough: probes must wait until the transport has
// installed the document's exact text. Gate sends to reproduce both an absent
// transport document on open and stale diagnostics during an update. Edits
// arriving during either send must remain pending, not inherit its readiness.
func TestProbeWaitsForDocumentSynchronization(t *testing.T) {
	root := t.TempDir()
	c := &probeSyncClient{entered: make(chan string, 8), release: make(chan struct{})}
	m := NewLSPManager(nil, probeSyncRuntime{c})
	t.Cleanup(m.Close)
	t.Cleanup(func() { close(c.release) })
	m.SetWorkspaceRoot(root)
	path := filepath.Join(root, "main.go")
	initial := "package main\nvar initial = missing\n"
	latest := "package main\nvar latest = missing\n"
	newest := "package main\nvar newest = missing\n"
	createFile(t, path, initial)
	m.OpenFile(t.Context(), path, initial)
	awaitSend := func(want string) {
		t.Helper()
		select {
		case got := <-c.entered:
			if got != want {
				t.Fatalf("transport send = %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("transport never sent %q", want)
		}
	}
	assertProbeWaits := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		diags, settled, _, err := m.ProbeDiagnostics(ctx, path)
		if !errors.Is(err, context.DeadlineExceeded) || settled || len(diags) != 0 {
			t.Fatalf("probe escaped pending transport send: diagnostics=%+v settled=%v err=%v", diags, settled, err)
		}
	}
	awaitSend("open:" + initial)
	assertProbeWaits()
	m.OpenFile(t.Context(), path, latest)
	c.release <- struct{}{}
	awaitSend("change:" + latest)
	assertProbeWaits()
	m.OpenFile(t.Context(), path, newest)
	c.release <- struct{}{}
	awaitSend("change:" + newest)
	assertProbeWaits()
	c.release <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	diags, settled, view, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(diags) != 1 || diags[0].Message != newest || !view.BufferDiffers {
		t.Fatalf("probe after transport sync: diagnostics=%+v settled=%v view=%+v err=%v", diags, settled, view, err)
	}
}

// Deferring readiness must not lose didClose when the last holder leaves during
// didOpen. The worker must release the just-opened transport document even
// though the manager had not yet advertised it as open.
func TestProbeReleasesDocumentWhenCanceledDuringOpen(t *testing.T) {
	root := t.TempDir()
	c := &probeSyncClient{entered: make(chan string, 1), release: make(chan struct{}), closed: make(chan struct{}, 1)}
	m := NewLSPManager(nil, probeSyncRuntime{c})
	t.Cleanup(m.Close)
	t.Cleanup(func() { close(c.release) })
	m.SetWorkspaceRoot(root)
	m.SetSettings(editorDisabledSettings())
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := m.ProbeDiagnostics(ctx, path)
		done <- err
	}()
	select {
	case <-c.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("probe never opened its transport document")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled probe = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe ignored cancellation during document open")
	}
	c.release <- struct{}{}
	select {
	case <-c.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled probe left its in-flight transport document open")
	}
	m.mu.Lock()
	_, retained := m.documents[path]
	m.mu.Unlock()
	if retained {
		t.Fatal("canceled probe retained its document")
	}
}

// Reconfiguration during a send must not mark the retained document ready for
// its replacement client. The replacement still has to receive its own open.
func TestProbeWaitsForReplacementClientDocument(t *testing.T) {
	root := t.TempDir()
	old := &probeSyncClient{entered: make(chan string, 1), release: make(chan struct{})}
	next := &probeSyncClient{entered: make(chan string, 1), release: make(chan struct{})}
	m := NewLSPManager(nil, probeSyncRuntime{old})
	t.Cleanup(m.Close)
	t.Cleanup(func() { close(old.release); close(next.release) })
	m.SetWorkspaceRoot(root)
	path := filepath.Join(root, "main.go")
	text := "package main\nvar x = missing\n"
	createFile(t, path, text)
	m.OpenFile(t.Context(), path, text)
	select {
	case <-old.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("original transport never received the document")
	}
	m.SetRuntime(probeSyncRuntime{next})
	old.release <- struct{}{}
	select {
	case <-next.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement transport never received the document")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, settled, _, err := m.ProbeDiagnostics(ctx, path)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || settled {
		t.Fatalf("replacement inherited original send readiness: settled=%v err=%v", settled, err)
	}
	next.release <- struct{}{}
	ctx, cancel = context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	diags, settled, _, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(diags) != 1 || diags[0].Message != text {
		t.Fatalf("replacement probe: diagnostics=%+v settled=%v err=%v", diags, settled, err)
	}
}
