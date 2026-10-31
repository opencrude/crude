package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/document"
	model "crude/types"
)

func probeSources(t *testing.T, manager *LSPManager, root string) *DocumentService {
	t.Helper()
	sources := NewDocumentService(root, nil, manager)
	t.Cleanup(sources.Close)
	return sources
}

func probeManager(t *testing.T) (*LSPManager, *lifecycleRuntime, string) {
	t.Helper()
	manager, runtime, root := lifecycleManager(t)
	probeSources(t, manager, root)
	return manager, runtime, root
}

func probeEditor(t *testing.T, manager *LSPManager, path, text string) {
	t.Helper()
	owner := document.Owner{ClientID: "probe-editor", Epoch: 1}
	snapshot, err := manager.sources.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot.Text) != text {
		documentEdit(t, manager.sources, owner, snapshot.ID, "replace-"+text, snapshot.Revision, 0, len(snapshot.Text), text)
	}
}

func editorDisabledSettings() model.LSPSettings {
	settings := model.DefaultLSPSettings()
	settings.Enabled = false
	return settings
}

// An agent probe works while the editor's own LSP is off, starts only the one
// server it needs, and closes the document it borrowed afterwards.
func TestProbeServesAgentsWhileEditorLSPIsOff(t *testing.T) {
	m, r, root := probeManager(t)
	m.SetSettings(editorDisabledSettings())
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\nvar x = y\n")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	diags, settled, view, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(diags.Diagnostics) != 1 || view.BufferDiffers {
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
	m, r, root := probeManager(t)
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	probeEditor(t, m, path, "package main\nfunc unsaved() {}\n")
	awaitLSP(t, func() bool { cs := r.snapshot(); return len(cs) == 1 && len(cs[0].snapshot()) > 0 })
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
	m, r, root := probeManager(t)
	m.SetSettings(editorDisabledSettings())
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	probeEditor(t, m, path, "package main\n")
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
	m, r, root := probeManager(t)
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
	if _, _, err := m.ProbeNavigation(t.Context(), path, model.LSPNavigationReferences, model.LSPSourcePosition{}, true); err == nil || !strings.Contains(err.Error(), "Navigation is turned off") {
		t.Fatalf("gated references = %v", err)
	}
	if _, _, _, err := m.ProbeDiagnostics(t.Context(), filepath.Join(root, "notes.unknownext")); err == nil || !strings.Contains(err.Error(), "no enabled language server") {
		t.Fatalf("unsupported language = %v", err)
	}
}

func TestProbeNavigationKindsAndAnswersWhileEditorDisabled(t *testing.T) {
	f := newNavigationFixture(t, "// 😀 y\r\n")
	m := f.w.LSPMgr
	m.SetSettings(editorDisabledSettings())
	for _, kind := range []string{"definition", "type-definition", "implementation", "references"} {
		for _, answer := range []string{"null", "empty", "unsupported", "targets"} {
			t.Run(kind+"/"+answer, func(t *testing.T) {
				raw := map[string]string{"null": "null", "empty": "[]", "unsupported": "",
					"targets": "[" + navigationLocation(navigationTestURI(f.path)) + "]"}[answer]
				f.client.navMu.Lock()
				f.client.raw, f.client.unsupported = raw, answer == "unsupported"
				f.client.navMu.Unlock()
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				result, view, err := m.ProbeNavigation(ctx, f.path, kind, model.LSPSourcePosition{Line: 1, Symbol: "y"}, kind == "references")
				if err != nil {
					t.Fatal(err)
				}
				if result.Kind != kind || result.Answer != answer || result.Line != 0 || result.Col != 6 ||
					result.IncludeDeclaration != (kind == "references") || result.WireVersion <= 0 ||
					result.Boundary.Source != view.Source || result.Boundary.Source.DocumentID == "" ||
					result.Boundary.Producer.InstanceID == "" || result.Boundary.Context.Generation == 0 ||
					len(result.Boundary.Context.UnknownInputs) == 0 || view.SourceText != "// 😀 y\r\n" {
					t.Fatalf("probe lost source/query/qualification: %+v view=%+v", result, view)
				}
				f.client.navMu.Lock()
				call := f.client.requests[len(f.client.requests)-1]
				f.client.navMu.Unlock()
				if call != (navigationCall{f.path, kind, 0, 6, kind == "references"}) {
					t.Fatal("probe changed literal UTF-16 source position or flag", call)
				}
				if answer == "targets" {
					if len(result.Targets) != 1 {
						t.Fatal("positive answer lost targets", result)
					}
					target := result.Targets[0]
					if target.Path != f.path || target.URI != navigationTestURI(f.path) || target.UnavailableReason != "" ||
						target.External || target.Link || target.Origin != nil ||
						target.Range != navigationTestRange(0, 1, 3) || target.Selection != navigationTestRange(0, 1, 3) {
						t.Fatal("probe lost target metadata", target)
					}
				} else if len(result.Targets) != 0 {
					t.Fatal("negative answer carried target payload", result)
				}
			})
		}
	}
}

func TestProbeNavigationDefinitionFamilyAndReferencesGates(t *testing.T) {
	f := newNavigationFixture(t, "var y = 1\n")
	for _, enabled := range []string{"definition", "references"} {
		settings := editorDisabledSettings()
		settings.Definition, settings.References = enabled == "definition", enabled == "references"
		f.w.LSPMgr.SetSettings(settings)
		for _, kind := range []string{"definition", "type-definition", "implementation", "references"} {
			t.Run(enabled+"/"+kind, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				result, _, err := f.w.LSPMgr.ProbeNavigation(ctx, f.path, kind, model.LSPSourcePosition{Line: 1, Symbol: "y"}, kind == "references")
				allowed := (kind == "references") == (enabled == "references")
				if allowed && (err != nil || result.Answer != "empty") ||
					!allowed && (err == nil || !reflect.DeepEqual(result, model.LSPNavigation{})) {
					t.Fatalf("gate %s kind %s: %+v err=%v", enabled, kind, result, err)
				}
			})
		}
	}
}

func TestProbeNavigationErrorsNeverReturnAbsence(t *testing.T) {
	f := newNavigationFixture(t, "var y = 1\n")
	f.w.LSPMgr.SetSettings(editorDisabledSettings())
	for _, kind := range []string{"definition", "type-definition", "implementation", "references"} {
		for _, failure := range []string{"transport", "malformed", "symbol", "include-declaration"} {
			if failure == "include-declaration" && kind == "references" {
				continue
			}
			t.Run(kind+"/"+failure, func(t *testing.T) {
				f.client.navMu.Lock()
				f.client.err, f.client.raw = nil, "[]"
				if failure == "transport" {
					f.client.err = errors.New("provider transport failed")
				}
				if failure == "malformed" {
					f.client.raw = `{"uri":"file:///bad","range":{}}`
				}
				f.client.navMu.Unlock()
				symbol := "y"
				if failure == "symbol" {
					symbol = "absent"
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				result, _, err := f.w.LSPMgr.ProbeNavigation(ctx, f.path, kind, model.LSPSourcePosition{Line: 1, Symbol: symbol}, failure == "include-declaration")
				if err == nil || !reflect.DeepEqual(result, model.LSPNavigation{}) {
					t.Fatalf("error became absence: %+v err=%v", result, err)
				}
			})
		}
	}
	for _, kind := range []string{"typeDefinition", "Definition", "unknown"} {
		result, _, err := f.w.LSPMgr.ProbeNavigation(t.Context(), f.path, kind, model.LSPSourcePosition{Line: 1, Symbol: "y"}, false)
		if err == nil || !reflect.DeepEqual(result, model.LSPNavigation{}) {
			t.Fatalf("unrecognized kind %q returned answer: %+v err=%v", kind, result, err)
		}
	}
}

// A failed start is reported at once, not after the caller's deadline.
func TestProbeReportsStartFailureImmediately(t *testing.T) {
	m, _, root := probeManager(t)
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

func (failingRuntime) NewClient(model.LSPServerSpec, string, func() error) LSPClient {
	return &failingClient{reapableClient{done: make(chan struct{})}}
}

type failingClient struct{ reapableClient }

func (*failingClient) Start(context.Context) error {
	return errors.New("exec: \"gopls\": executable file not found in $PATH")
}

type probeSyncClient struct {
	reapableClient
	mu          sync.Mutex
	text        string
	opened      bool
	boundary    model.InsightBoundary
	version     int
	publication model.InsightDiagnosticPublication
	entered     chan string
	release     chan struct{}
	closed      chan struct{}
}

func (c *probeSyncClient) send(ctx context.Context, operation, text string, boundary model.InsightBoundary) (int, error) {
	c.entered <- operation + ":" + text
	select {
	case <-c.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	c.mu.Lock()
	c.text, c.opened = text, true
	c.boundary = boundary
	c.version++
	v := c.version
	c.publication = model.InsightDiagnosticPublication{Boundary: boundary, Sequence: uint64(v), WireVersion: v, ReportedVersion: &v,
		Diagnostics: []LSPDiagnostic{{Message: text, Severity: model.DiagError}}}
	c.mu.Unlock()
	return v, nil
}

func (c *probeSyncClient) OpenFile(ctx context.Context, _, _, text string, boundary model.InsightBoundary) (int, error) {
	return c.send(ctx, "open", text, boundary)
}

func (c *probeSyncClient) UpdateFile(ctx context.Context, _, text string, boundary model.InsightBoundary) (int, error) {
	return c.send(ctx, "change", text, boundary)
}

func (c *probeSyncClient) CloseFile(context.Context, string) {
	c.mu.Lock()
	c.opened = false
	c.mu.Unlock()
	if c.closed != nil {
		c.closed <- struct{}{}
	}
}

func (c *probeSyncClient) AwaitDiagnostics(context.Context, string) (model.InsightDiagnosticPublication, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return model.InsightDiagnosticPublication{}, false
	}
	return c.publication, true
}

type probeSyncRuntime struct{ client *probeSyncClient }

func (r probeSyncRuntime) NewClient(model.LSPServerSpec, string, func() error) LSPClient {
	return r.client
}

// A connected process is not enough: probes must wait until the transport has
// installed the document's exact text. Gate sends to reproduce both an absent
// transport document on open and stale diagnostics during an update. Edits
// arriving during either send must remain pending, not inherit its readiness.
func TestProbeWaitsForDocumentSynchronization(t *testing.T) {
	root := t.TempDir()
	c := &probeSyncClient{entered: make(chan string, 8), release: make(chan struct{})}
	m := NewLSPManager(nil, probeSyncRuntime{c})
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	t.Cleanup(func() { close(c.release) })
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	path := filepath.Join(root, "main.go")
	initial := "package main\nvar initial = missing\n"
	latest := "package main\nvar latest = missing\n"
	newest := "package main\nvar newest = missing\n"
	createFile(t, path, initial)
	probeEditor(t, m, path, initial)
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
		if !errors.Is(err, context.DeadlineExceeded) || settled || len(diags.Diagnostics) != 0 {
			t.Fatalf("probe escaped pending transport send: diagnostics=%+v settled=%v err=%v", diags, settled, err)
		}
	}
	awaitSend("open:" + initial)
	assertProbeWaits()
	probeEditor(t, m, path, latest)
	c.release <- struct{}{}
	awaitSend("change:" + latest)
	assertProbeWaits()
	probeEditor(t, m, path, newest)
	c.release <- struct{}{}
	awaitSend("change:" + newest)
	assertProbeWaits()
	c.release <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	diags, settled, view, err := m.ProbeDiagnostics(ctx, path)
	if err != nil || !settled || len(diags.Diagnostics) != 1 || diags.Diagnostics[0].Message != newest || !view.BufferDiffers {
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
	probeSources(t, m, root)
	t.Cleanup(func() { close(c.release) })
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
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
	probeSources(t, m, root)
	t.Cleanup(func() { close(old.release); close(next.release) })
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	path := filepath.Join(root, "main.go")
	text := "package main\nvar x = missing\n"
	createFile(t, path, text)
	probeEditor(t, m, path, text)
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
	if err != nil || !settled || len(diags.Diagnostics) != 1 || diags.Diagnostics[0].Message != text {
		t.Fatalf("replacement probe: diagnostics=%+v settled=%v err=%v", diags, settled, err)
	}
}
