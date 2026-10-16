package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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
