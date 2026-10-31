package viewmodel

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core"
	"crude/tui/editor"
	"crude/tui/screens/inlay"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

const inlayConnectedSource = "package journey\n\nvar value = 1\n"
const inlayConnectedOriginal = `{"position":{"line":2,"character":9},"label":": int","kind":1,"data":{"opaque":9007199254740993,"fraction":1.2300e+09},"textEdits":[{"range":{"start":{"line":2,"character":9},"end":{"line":2,"character":9}},"newText":"NEVER INSERT"}]}`
const inlayConnectedAnswer = `[` + inlayConnectedOriginal + `,{"position":{"line":2,"character":9},"label":" note","kind":2}]`

type inlayConnectedCall struct {
	path, text string
	wire       int
	span       types.LSPInlayRange
}

type inlayConnectedRuntime struct {
	renameConnectedRuntime
	answer                        string
	unsupported, resolveSupported bool
	calls                         []inlayConnectedCall
	resolves                      int
	original                      []byte
	resolveGate, resolveEntered   chan struct{}
	resolveCancelled              chan struct{}
}

type inlayConnectedClient struct {
	*renameConnectedClient
	engine *inlayConnectedRuntime
}

func (r *inlayConnectedRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	base := r.renameConnectedRuntime.NewClient(spec, root, admit).(*renameConnectedClient)
	return &inlayConnectedClient{renameConnectedClient: base, engine: r}
}

func (c *inlayConnectedClient) RequestInlayHints(_ context.Context, path string, span types.LSPInlayRange) (types.LSPInlayResponse, error) {
	r := c.engine
	r.mu.Lock()
	defer r.mu.Unlock()
	doc := c.docs[path]
	r.calls = append(r.calls, inlayConnectedCall{path: path, text: doc.text, wire: doc.version, span: span})
	raw := []byte(r.answer)
	if r.unsupported {
		raw = nil
	}
	return types.LSPInlayResponse{JSON: raw, WireVersion: doc.version,
		Unsupported: r.unsupported, ResolveSupported: r.resolveSupported && !r.unsupported}, nil
}

func (c *inlayConnectedClient) ResolveInlayHint(ctx context.Context, path string, wire int, original []byte) (types.LSPInlayResponse, error) {
	r := c.engine
	r.mu.Lock()
	r.resolves++
	r.original = append([]byte(nil), original...)
	gate, entered, cancelled := r.resolveGate, r.resolveEntered, r.resolveCancelled
	r.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			if cancelled != nil {
				select {
				case cancelled <- struct{}{}:
				default:
				}
			}
			<-gate // deliberately late provider: the real owner must fence it
		}
	}
	raw := strings.TrimSuffix(string(original), "}") + `,"tooltip":{"kind":"plaintext","value":"literal tooltip only"}}`
	return types.LSPInlayResponse{JSON: []byte(raw), WireVersion: wire, ResolveSupported: true}, nil
}

type inlayConnectedFixture struct {
	runtime *core.Runtime
	shells  []remoteJourneyShell
	ws      *workspace.Workspace
	engine  *inlayConnectedRuntime
	main    *editor.Editor
	peer    *editor.Editor
}

func newInlayConnectedFixture(t *testing.T, enabled bool, answer string) inlayConnectedFixture {
	t.Helper()
	root := t.TempDir()
	engine := &inlayConnectedRuntime{renameConnectedRuntime: renameConnectedRuntime{main: filepath.Join(root, "source.go")},
		answer: answer, resolveSupported: true}
	for path, text := range map[string]string{engine.main: inlayConnectedSource, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete, settings.LSP.InlayHints = false, false, enabled
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: executable, RootMarkers: []string{"go.mod"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, engine.main), liveTestOpen(t, two.app, engine.main)
	f := inlayConnectedFixture{runtime, []remoteJourneyShell{one, two}, owned, engine, a, b}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, source, err := owned.LSPMgr.ProbeHover(ctx, engine.main, types.LSPSourcePosition{Line: 1, Column: 1}); err != nil || source.Source.Kind != "canonical" {
		t.Fatal("inlay fixture source did not synchronize", source, err)
	}
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	a.SetCursorPosition(2, 4)
	b.SetCursorPosition(0, 7)
	one.app.Input.SetInput("private first inlay draft")
	two.app.Input.SetInput("private second inlay draft")
	for _, shell := range f.shells {
		shell.app.View().RenderContent()
		// Drive a finite normal layout event. Feeding the recurring clock to
		// runAppCommand would ask that finite helper to join an endless timer.
		_, cmd := shell.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		runAppCommand(t, shell.app, cmd)
	}
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.ws.LSPMgr().ServerReady() && two.app.ws.LSPMgr().ServerReady()
	})
	if enabled {
		f.waitPainted(t)
	}
	return f
}

func (f inlayConnectedFixture) waitPainted(t *testing.T) {
	t.Helper()
	remoteJourneyUntil(t, f.shells, func() bool {
		return f.main.HasInlayProjection() && f.peer.HasInlayProjection()
	})
}

func (f inlayConnectedFixture) unchanged(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(f.engine.main)
	if err != nil || string(raw) != inlayConnectedSource || f.main.Value() != inlayConnectedSource || f.peer.Value() != inlayConnectedSource ||
		f.main.Modified() || f.peer.Modified() || f.main.DocumentSnapshot().Revision != 1 || f.peer.DocumentSnapshot().Revision != 1 {
		t.Fatal("read-only inlay operation changed source/disk/revision/savepoint", string(raw), err)
	}
	if f.shells[0].app.Input.Value() != "private first inlay draft" || f.shells[1].app.Input.Value() != "private second inlay draft" {
		t.Fatal("inlay workflow consumed a private composer draft")
	}
}

func (f inlayConnectedFixture) palette(t *testing.T, label string) {
	t.Helper()
	one := f.shells[0]
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt})
	for _, r := range label {
		renameConnectedKey(t, one, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, label) {
		t.Fatalf("real palette did not expose %q:\n%s", label, paint)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func (f inlayConnectedFixture) open(t *testing.T) *inlayInspectorState {
	t.Helper()
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	remoteJourneyUntil(t, f.shells, func() bool {
		s := f.shells[0].app.inlayInspector
		return s != nil && s.shown && s.screen.InList()
	})
	s := f.shells[0].app.inlayInspector
	if paint := ansi.Strip(f.shells[0].app.View().RenderContent()); !strings.Contains(paint, "Inlay hints") || !strings.Contains(paint, ": int") {
		t.Fatalf("Alt+J did not paint real inlay rows:\n%s", paint)
	}
	return s
}

func TestConnectedInlayDefaultOffAndTwoShellViewLocalShowHide(t *testing.T) {
	f := newInlayConnectedFixture(t, false, inlayConnectedAnswer)
	f.palette(t, "Show Inlay Hints")
	f.engine.mu.Lock()
	calls := len(f.engine.calls)
	f.engine.mu.Unlock()
	if calls != 0 || f.main.HasInlayProjection() || f.peer.HasInlayProjection() {
		t.Fatal("view-local Show bypassed the default-Off feature gate")
	}
	settings := f.ws.AppDefaults()
	settings.LSP.InlayHints = true
	f.ws.SetAppDefaults(settings)
	f.waitPainted(t)
	for _, shell := range f.shells {
		paint := ansi.Strip(shell.app.View().RenderContent())
		if !strings.Contains(paint, "value: int note = 1") {
			t.Fatalf("acknowledged source did not paint stable same-anchor labels:\n%s", paint)
		}
	}
	f.engine.mu.Lock()
	actual := append([]inlayConnectedCall(nil), f.engine.calls...)
	f.engine.mu.Unlock()
	if len(actual) < 2 {
		t.Fatal("both independent visible views did not query shared producer")
	}
	for _, call := range actual {
		if call.path != f.engine.main || call.text != inlayConnectedSource || call.wire <= 0 ||
			call.span.Start != (types.LSPInlayPosition{}) || call.span.End != (types.LSPInlayPosition{Line: 3}) {
			t.Fatal("visible range request lost synchronized source/wire/EOF anchors", call)
		}
	}
	f.palette(t, "Hide Inlay Hints")
	remoteJourneyUntil(t, f.shells, func() bool { return !f.main.HasInlayProjection() })
	if !f.peer.HasInlayProjection() || !f.ws.LSPMgr.Settings().InlayHints {
		t.Fatal("one private Hide changed shared settings or another shell")
	}
	f.palette(t, "Show Inlay Hints")
	f.waitPainted(t)
	if f.main.CursorLine() != 2 || f.main.CursorCol() != 4 || f.peer.CursorLine() != 0 || f.peer.CursorCol() != 7 {
		t.Fatal("show/hide moved private source carets")
	}
	f.unchanged(t)
}

func TestConnectedInlayNativeInspectorResolveBackCloseAndSemanticReceipt(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	one := f.shells[0]
	s := f.open(t)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool { return s.screen.InDetail() && !s.reading })
	// Default Enter must be Close, never Resolve, edit or label-location Open.
	f.engine.mu.Lock()
	resolves := f.engine.resolves
	f.engine.mu.Unlock()
	if resolves != 0 {
		t.Fatal("Inspect automatically resolved opaque provider data")
	}
	result := journeySettingsControl(t, f.runtime, f.shells, one, "inlay/actions/resolve", ui.ControlOperation{Verb: "activate"})
	if result.State != control.Applied || !reflect.DeepEqual(result.ResourceIDs, []string{s.request.result.ID}) {
		t.Fatal("semantic tooltip read did not await its exact original mounted result", result)
	}
	f.engine.mu.Lock()
	resolves, original := f.engine.resolves, append([]byte(nil), f.engine.original...)
	f.engine.mu.Unlock()
	if resolves != 1 || string(original) != inlayConnectedOriginal || !json.Valid(original) {
		t.Fatal("selected Resolve did not retain original opaque JSON", resolves, string(original))
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyTab}) // native reader
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "9007199254740993") || !strings.Contains(paint, "NEVER INSERT") {
		t.Fatalf("literal original evidence is not actually scrollable/rendered:\n%s", paint)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	remoteJourneyUntil(t, f.shells, func() bool { return s.screen.InList() && !s.reading })
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	if one.app.inlayInspector != nil || !f.main.HasInlayProjection() || f.shells[1].app.inlayInspector != nil {
		t.Fatal("Close did not dismiss only this read-only inspector")
	}
	f.unchanged(t)
}

func TestConnectedInlayBackCancelsTooltipReaderAndCannotReopenDetail(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	one := f.shells[0]
	s := f.open(t)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool { return s.screen.InDetail() && !s.reading })
	f.engine.mu.Lock()
	gate, entered, cancelled := make(chan struct{}), make(chan struct{}, 1), make(chan struct{}, 1)
	f.engine.resolveGate, f.engine.resolveEntered, f.engine.resolveCancelled = gate, entered, cancelled
	f.engine.mu.Unlock()
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	_, activate := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	intent := captureNavigationCommand[inlay.InspectMsg](t, one, activate)
	// Preserve one Update writer while the selected RPC is deliberately held.
	// The ordinary native intent still enters the exact product read owner.
	one.app.runDetachedAsyncCmd(one.app.inspectInlayHintCmd(intent))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("explicit native Resolve never reached retained original provider")
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	remoteJourneyUntil(t, f.shells, func() bool { return s.screen.InList() && !s.reading })
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Back only abandoned the shell result, not the original provider reader")
	}
	close(gate)
	// A normal later original-row read provides an ordered exchange barrier.
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyDown})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool { return s.screen.InDetail() && !s.reading && s.index == 1 })
	selected, ok := s.screen.SelectedIndex()
	if !ok || selected != 1 {
		t.Fatal("late tooltip result reopened the former row")
	}
	f.unchanged(t)
}

func TestConnectedInlaySettingsEpochRevokesAndNoRestartForViewGate(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	one := f.shells[0]
	old := *one.app.inlayViews[f.main].request.result
	settings := f.ws.AppDefaults()
	before := old.Boundary.Producer
	settings.LSP.InlayHints = false
	f.ws.SetAppDefaults(settings)
	settings.LSP.InlayHints = true
	f.ws.SetAppDefaults(settings)
	// Even if the two Boolean snapshots coalesce, the epoch must revoke old
	// originals and old painted hints without restarting the producer.
	if detail, err := one.app.ws.(interface {
		InspectLSPInlayHint(context.Context, string, string, int, bool) (types.LSPInlayInspection, error)
	}).InspectLSPInlayHint(t.Context(), old.ID, old.Digest, 0, true); err == nil || detail.SetID != "" {
		t.Fatal("disable/re-enable restored old original-object authority")
	}
	remoteJourneyUntil(t, f.shells, func() bool {
		r := one.app.inlayViews[f.main].request
		return r != nil && r.result != nil && r.installed && r.result.ID != old.ID
	})
	after := one.app.inlayViews[f.main].request.result.Boundary.Producer
	if before.InstanceID == "" || before != after {
		t.Fatal("view-only inlay feature toggles restarted the language service", before, after)
	}
	f.unchanged(t)
}
