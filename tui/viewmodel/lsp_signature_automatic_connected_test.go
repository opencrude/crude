package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

const automaticSignatureSource = "package journey\n\nvar _ = join\n"

type automaticSignatureCall struct {
	path      string
	line, col int
	document  renameConnectedDocument
	ctx       context.Context
}

// Reuse the retained provider's real OpenFile/UpdateFile wire clock. The answer
// below is independent literal provider evidence, not a product formatting helper.
type automaticSignatureRuntime struct {
	base     renameConnectedRuntime
	calls    []automaticSignatureCall // guarded by base.mu
	mode     string
	release  chan struct{}
	released sync.Once
}

type automaticSignatureClient struct {
	*renameConnectedClient
	owner *automaticSignatureRuntime
}

func (r *automaticSignatureRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return &automaticSignatureClient{r.base.NewClient(spec, root, admit).(*renameConnectedClient), r}
}

func (c *automaticSignatureClient) RequestSignatureHelp(ctx context.Context, path string, line, col int) (types.LSPSignatureHelp, error) {
	r := c.owner
	r.base.mu.Lock()
	d := c.docs[path]
	r.calls = append(r.calls, automaticSignatureCall{path, line, col, d, ctx})
	r.base.mu.Unlock()
	if r.release != nil {
		// Deliberately ignore cancellation. The coordinator and UI must reject
		// this late positive reply without relying on provider cooperation.
		<-r.release
	}
	if r.mode == "unsupported" {
		return types.LSPSignatureHelp{}, errors.New("provider does not advertise signature help")
	}
	if r.mode == "empty" {
		return types.LSPSignatureHelp{}, nil
	}
	active := 0
	if col == 15 {
		active = 1
	}
	return types.LSPSignatureHelp{Signatures: []types.LSPSignature{{
		Label: "join(first string, count int)", Documentation: "Independent connected provider evidence.",
		ActiveParameter: active, Parameters: []types.LSPSignatureParameter{
			{Label: "first string", Documentation: "Text to repeat."},
			{Label: "count int", Documentation: "Number of copies."},
		},
	}}}, nil
}

func (r *automaticSignatureRuntime) observed() []automaticSignatureCall {
	r.base.mu.Lock()
	defer r.base.mu.Unlock()
	return append([]automaticSignatureCall(nil), r.calls...)
}

func (r *automaticSignatureRuntime) unblock() {
	if r.release != nil {
		r.released.Do(func() { close(r.release) })
	}
}

type automaticSignatureFixture struct {
	shells []remoteJourneyShell
	eds    []*editor.Editor
	ws     *workspace.Workspace // passive canonical reads only after construction
	engine *automaticSignatureRuntime
	path   string
}

func newAutomaticSignatureFixture(t *testing.T, mode string, blocked bool) automaticSignatureFixture {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	for file, text := range map[string]string{path: automaticSignatureSource, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	engine := &automaticSignatureRuntime{mode: mode}
	if blocked {
		engine.release = make(chan struct{})
	}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: mode != "missing", Command: executable, RootMarkers: []string{"go.mod"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	// Registered last: unblock before shell/core shutdown even after an assertion.
	t.Cleanup(engine.unblock)
	f := automaticSignatureFixture{[]remoteJourneyShell{one, two}, []*editor.Editor{a, b}, owned, engine, path}
	if mode != "missing" {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, view, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 3, Column: 1})
		cancel()
		if err != nil || view.Source.Kind != "canonical" || view.SourceText != automaticSignatureSource {
			t.Fatalf("initial retained source synchronization: %+v %v", view, err)
		}
	}
	remoteJourneyUntil(t, f.shells, func() bool {
		_, aReady := a.CurrentInsightSource()
		_, bReady := b.CurrentInsightSource()
		return aReady && bReady && (mode == "missing" || one.app.ws.LSPMgr().ServerReady())
	})
	a.SetCursorPosition(2, 12)
	b.SetCursorPosition(0, 0)
	one.app.Input.SetInput("private first shell draft")
	two.app.Input.SetInput("private second shell draft")
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	return f
}

func (f automaticSignatureFixture) key(t *testing.T, key tea.KeyPressMsg) {
	t.Helper()
	renameConnectedKey(t, f.shells[0], key)
}

func automaticSignatureRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func (f automaticSignatureFixture) assertSource(t *testing.T, index int, text string, col int) {
	t.Helper()
	calls := f.engine.observed()
	if len(calls) != index+1 {
		t.Fatalf("provider requests=%d, want exactly %d: %+v", len(calls), index+1, calls)
	}
	call := calls[index]
	snapshot, err := f.ws.LiveDocuments.Snapshot(f.eds[0].DocumentState().DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	source, ready := f.eds[0].CurrentInsightSource()
	if !ready || call.path != f.path || call.line != 2 || call.col != col ||
		call.document.text != text || string(snapshot.Text) != text || call.document.version <= 0 ||
		call.document.boundary.Source != source || source.Revision != snapshot.Revision {
		t.Fatalf("request was not the exact acknowledged typed source: call=%+v canonical=%+v local=%+v ready=%v", call, snapshot, source, ready)
	}
	if index > 0 && call.document.version <= calls[index-1].document.version {
		t.Fatal("retrigger did not synchronize a newer actual provider wire version")
	}
}

func (f automaticSignatureFixture) assertPrivate(t *testing.T) {
	t.Helper()
	one, two := f.shells[0].app, f.shells[1].app
	line, col := f.eds[1].CursorLine(), f.eds[1].CursorCol()
	if line != 0 || col != 0 || two.signatureHelp != nil || two.overlayHost.IsOpen(signatureHelpID) ||
		one.Input.Value() != "private first shell draft" || two.Input.Value() != "private second shell draft" {
		t.Fatal("parameter help changed a peer caret, private draft, or peer popup")
	}
	if data, err := os.ReadFile(f.path); err != nil || string(data) != automaticSignatureSource {
		t.Fatalf("typing/help wrote disk: %q %v", data, err)
	}
}

func TestConnectedAutomaticSignatureNativeTriggersStayPassiveAndShellPrivate(t *testing.T) {
	f := newAutomaticSignatureFixture(t, "", false)
	app := f.shells[0].app
	f.key(t, automaticSignatureRune('('))
	remoteJourneyUntil(t, f.shells, func() bool { return app.overlayHost.IsOpen(signatureHelpID) })
	f.assertSource(t, 0, "package journey\n\nvar _ = join()\n", 13)
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	paint := ansi.Strip(app.View().RenderContent())
	for _, marker := range []string{"join(first string, count int)", "Text to repeat.", "Source revision:", "context:"} {
		if !strings.Contains(paint, marker) {
			t.Fatalf("automatic card does not paint qualified provider help %q:\n%s", marker, paint)
		}
	}
	if app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
		t.Fatal("automatic parameter card acquired keyboard focus")
	}
	rect, mounted := app.rootHost.ScreenRect(app.editorScreen.ID())
	if !mounted || rect.Empty() {
		t.Fatal("automatic help's source editor is not mounted")
	}
	caretX, caretY := f.eds[0].CursorScreenPosition(rect.X, rect.Y)
	card := app.overlayHost.Window(signatureHelpID)
	cardX, cardY := card.Position()
	cardW, cardH := card.Size()
	if cardX > caretX || cardX+cardW <= caretX ||
		(cardY != caretY+1 && cardY+cardH != caretY && cardY+cardH+1 != caretY) {
		t.Fatalf("passive card is not adjacent to its actual painted caret: card=%d,%d %dx%d caret=%d,%d", cardX, cardY, cardW, cardH, caretX, caretY)
	}
	f.assertPrivate(t)

	// This must be a native edit, not read-only TextArea input capture.
	f.key(t, automaticSignatureRune('x'))
	if got := f.eds[0].Value(); got != "package journey\n\nvar _ = join(x)\n" {
		t.Fatalf("ordinary next typed character missed the editor: %q", got)
	}
	f.key(t, automaticSignatureRune(','))
	remoteJourneyUntil(t, f.shells, func() bool { return app.overlayHost.IsOpen(signatureHelpID) })
	f.assertSource(t, 1, "package journey\n\nvar _ = join(x,)\n", 15)
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	if paint = ansi.Strip(app.View().RenderContent()); !strings.Contains(paint, "Number of copies.") {
		t.Fatalf("comma did not paint the independently returned second parameter:\n%s", paint)
	}
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEscape})
	if app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) || !f.eds[0].IsOpen() ||
		f.eds[0].Value() != "package journey\n\nvar _ = join(x,)\n" {
		t.Fatal("Escape consumed the editor/tab instead of just help")
	}
	f.assertPrivate(t)

	// Explicit inspection still owns a focusable, selectable full reader.
	f.key(t, tea.KeyPressMsg{Code: 'i', Mod: tea.ModAlt})
	remoteJourneyUntil(t, f.shells, func() bool { return app.overlayHost.IsOpen(signatureHelpID) })
	fw := app.overlayHost.Window(signatureHelpID)
	if app.focusedPopup() != fw {
		t.Fatal("Alt+I did not focus the explicit full reader")
	}
	area := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextArea)
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModShift})
	if !area.HasActiveSelection() {
		t.Fatal("explicit reader lost native text selection")
	}
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEscape})
	f.assertPrivate(t)
}

func TestConnectedAutomaticSignatureAbsentAnswersStayQuietWhileTyping(t *testing.T) {
	for _, mode := range []string{"empty", "unsupported", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticSignatureFixture(t, mode, false)
			app := f.shells[0].app
			f.key(t, automaticSignatureRune('('))
			liveTestPump(t, []*App{app, f.shells[1].app}, nil)
			if mode != "missing" {
				f.assertSource(t, 0, "package journey\n\nvar _ = join()\n", 13)
			} else if len(f.engine.observed()) != 0 {
				t.Fatal("missing provider received a semantic request")
			}
			if app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("absent parameter information fabricated a card")
			}
			for _, notice := range app.ws.Status().Notifications(true) {
				if notice.Source == "parameter-information" || strings.Contains(strings.ToLower(notice.Summary), "parameter information") {
					t.Fatalf("ordinary typing emitted semantic feedback for %s: %+v", mode, notice)
				}
			}
			f.key(t, automaticSignatureRune('x'))
			if f.eds[0].Value() != "package journey\n\nvar _ = join(x)\n" {
				t.Fatal("quiet absence swallowed subsequent native typing")
			}
			f.assertPrivate(t)
		})
	}
}

// Pending journeys must keep driving Update while the provider is deliberately
// blocked. Commands execute off Update, and only actual results/publications go
// through the normal App path; no synthetic editor ACK or semantic result exists.
type automaticSignatureDriver struct {
	fixture automaticSignatureFixture
	results chan tea.Msg
	pending int
}

func (d *automaticSignatureDriver) launch(cmd tea.Cmd) {
	if cmd != nil {
		d.pending++
		go func() { d.results <- cmd() }()
	}
}

func (d *automaticSignatureDriver) send(msg tea.Msg) {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			d.launch(cmd)
		}
		return
	}
	if msg != nil {
		_, cmd := d.fixture.shells[0].app.Update(msg)
		d.launch(cmd)
	}
}

func (d *automaticSignatureDriver) until(t *testing.T, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case msg := <-d.results:
			d.pending--
			d.send(msg)
		case msg := <-d.fixture.shells[0].fifo:
			d.send(msg)
		case msg := <-d.fixture.shells[1].fifo:
			_, cmd := d.fixture.shells[1].app.Update(msg)
			runAppCommand(t, d.fixture.shells[1].app, cmd)
		case <-tick.C:
		case <-timer.C:
			t.Fatal("pending connected parameter journey did not settle", fmt.Sprint(d.fixture.engine.observed()))
		}
	}
}

func TestConnectedAutomaticSignaturePendingReplyCannotReacquireInput(t *testing.T) {
	for _, change := range []string{"terminal", "edit", "caret", "escape", "stop"} {
		t.Run(change, func(t *testing.T) {
			f := newAutomaticSignatureFixture(t, "", true)
			app := f.shells[0].app
			var sent []shellterm.Command
			if change == "terminal" {
				screen := app.ensureTermScreen("automatic-signature-focus", "Parameter help destination")
				screen.SetControlled(true)
				screen.SetCommand(func(command shellterm.Command) tea.Cmd { sent = append(sent, command); return nil })
				app.termDock.Mount("automatic-signature-focus", screen)
				app.termDock.ShowPanel(termdock.PanelTerminal)
				app.setFocusEditor()
				app.View()
			}
			d := automaticSignatureDriver{fixture: f, results: make(chan tea.Msg, 128)}
			d.send(automaticSignatureRune('('))
			d.until(t, func() bool { return len(f.engine.observed()) == 1 })
			call := f.engine.observed()[0]
			f.assertSource(t, 0, "package journey\n\nvar _ = join()\n", 13)
			switch change {
			case "terminal":
				screen := app.termScreens["automatic-signature-focus"]
				rect, ok := app.rootHost.ScreenRect(screen.ID())
				if !ok || rect.Empty() {
					t.Fatal("terminal destination is not rendered")
				}
				d.send(tea.MouseClickMsg{X: rect.X + 2, Y: rect.Y, Button: tea.MouseLeft})
				d.send(tea.MouseReleaseMsg{X: rect.X + 2, Y: rect.Y, Button: tea.MouseLeft})
			case "edit":
				d.send(automaticSignatureRune('x'))
			case "caret":
				d.send(tea.KeyPressMsg{Code: tea.KeyLeft})
			case "escape":
				d.send(tea.KeyPressMsg{Code: tea.KeyEscape})
			case "stop":
				app.StopAsyncIO()
			}
			d.until(t, func() bool { return app.signatureHelp == nil && call.ctx.Err() != nil })
			f.engine.unblock()
			d.until(t, func() bool { return d.pending == 0 })
			liveTestPump(t, []*App{app, f.shells[1].app}, nil)
			if app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("late positive provider reply revived retired help")
			}
			if change == "terminal" {
				d.send(tea.PasteMsg{Content: "destination input"})
				d.until(t, func() bool { return d.pending == 0 })
				if !app.terminalDockOwnsKeys() || len(sent) != 1 || sent[0].Kind != shellterm.CommandPaste {
					t.Fatal("late reply stole destination terminal input", sent)
				}
			}
			if change == "edit" && f.eds[0].Value() != "package journey\n\nvar _ = join(x)\n" {
				t.Fatal("pending semantic request swallowed the native edit")
			}
			f.assertPrivate(t)
		})
	}
}
