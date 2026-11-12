package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/inlay"
	"github.com/charmbracelet/x/ansi"
)

func TestInlayPeerFocusKeepsTheOtherFileHintsAndEditing(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	plain := filepath.Join(filepath.Dir(f.engine.main), "notes.txt")
	if err := os.WriteFile(plain, []byte("no provider here\n"), 0600); err != nil {
		t.Fatal(err)
	}
	liveTestOpen(t, f.shells[1].app, plain)
	remoteJourneyUntil(t, f.shells, func() bool {
		lsp := f.shells[0].app.ws.LSPMgr()
		return !lsp.ServerReady() && lsp.FileProducer(f.engine.main).InstanceID != ""
	})
	remoteJourneyUntil(t, f.shells, func() bool { return f.main.HasInlayProjection() })
	f.main.SetCursorPosition(2, 13)
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, f.shells, func() bool {
		return strings.Contains(f.main.Value(), "1X") && f.peer.Value() == f.main.Value() && f.main.HasInlayProjection()
	})
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, f.shells, func() bool {
		return f.main.Value() == inlayConnectedSource && f.peer.Value() == inlayConnectedSource && f.main.HasInlayProjection()
	})
	if f.shells[0].app.Input.Value() != "private first inlay draft" || f.shells[1].app.EditorState.Editor.Value() != "no provider here\n" {
		t.Fatal("peer focus altered another draft or file")
	}
	if disk, err := os.ReadFile(f.engine.main); err != nil || string(disk) != inlayConnectedSource {
		t.Fatal("inlay interaction wrote disk", err)
	}
}

// Native Close Tab and a normal reopen must keep the peer's source and hints
// intact, then restore fresh hints without a second shared document owner.
func TestInlayCloseAndReopenWhilePeerKeepsFile(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	one := f.shells[0]
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	if one.app.EditorState.Tabs[f.engine.main] != nil {
		t.Fatal("native Close Tab left the file open")
	}
	if !f.peer.HasInlayProjection() || f.peer.Value() != inlayConnectedSource {
		t.Fatal("closing one view changed the peer")
	}
	reopened := liveTestOpen(t, one.app, f.engine.main)
	one.app.setFocusEditor()
	remoteJourneyUntil(t, f.shells, func() bool { return reopened.HasInlayProjection() && f.peer.HasInlayProjection() })
	reopened.SetCursorPosition(2, 13)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, f.shells, func() bool {
		return strings.Contains(reopened.Value(), "1X") && f.peer.Value() == reopened.Value() && reopened.HasInlayProjection()
	})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, f.shells, func() bool {
		return reopened.Value() == inlayConnectedSource && f.peer.Value() == inlayConnectedSource && reopened.HasInlayProjection()
	})
	if one.app.Input.Value() != "private first inlay draft" || f.shells[1].app.Input.Value() != "private second inlay draft" {
		t.Fatal("close/reopen consumed a private draft")
	}
	if disk, err := os.ReadFile(f.engine.main); err != nil || string(disk) != inlayConnectedSource {
		t.Fatal("close/reopen wrote disk", err)
	}
}

// A terminal correction can make an already-rendered label unrepresentable.
// The open list or pending tooltip read must become an actionable error, not
// continue showing a result whose inline labels have disappeared.
func TestInlayOpenReaderReportsReprojectionFailure(t *testing.T) {
	for _, state := range []string{"list", "reading"} {
		t.Run(state, func(t *testing.T) {
			ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 1}})
			t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
			f := newInlayConnectedFixture(t, true, `[{"position":{"line":2,"character":9},"label":"☰","kind":1}]`)
			one := f.shells[0]
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
			remoteJourneyUntil(t, f.shells, func() bool { return one.app.inlayInspector != nil && one.app.inlayInspector.screen.InList() })
			s := one.app.inlayInspector
			var gate, cancelled chan struct{}
			if state == "reading" {
				renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
				remoteJourneyUntil(t, f.shells, func() bool { return s.screen.InDetail() && !s.reading })
				gate = make(chan struct{})
				entered, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
				cancelled = stopped
				f.engine.mu.Lock()
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
				one.app.runDetachedAsyncCmd(one.app.inspectInlayHintCmd(intent))
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("tooltip read did not start")
				}
			}
			ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 0}})
			_, cmd := one.app.Update(tea.TerminalWidthProfileMsg{Measured: 1, Changed: true})
			runAppCommand(t, one.app, cmd)
			r := one.app.inlayViews[f.main].request
			if r == nil || r.err == nil || f.main.HasInlayProjection() {
				t.Fatal("fixture did not reproduce failed reprojection")
			}
			if one.app.inlayInspector != s || s.screen.InList() || s.screen.InDetail() || !strings.Contains(inlayVisualReader(s), "Could not load inlay hints") {
				t.Fatalf("open reader still shows success after failed reprojection: list=%t text=%q", s.screen.InList(), inlayVisualReader(s))
			}
			if state == "reading" {
				select {
				case <-cancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("failed reprojection did not cancel the tooltip read")
				}
				close(gate)
				if s.reading || s.readCancel != nil {
					t.Fatal("failed reprojection retained the selected reader")
				}
			}
			if f.main.Value() != inlayConnectedSource || one.app.Input.Value() != "private first inlay draft" {
				t.Fatal("reprojection changed source or draft")
			}
		})
	}
}

// A timed-out request remains observable as an error while its exact view is
// current. Cancelling the provider must not close that error before the user
// can follow its recovery instruction.
func TestInlayTimedOutRequestKeepsActionableReader(t *testing.T) {
	app, ed, _ := inlayRetryFixture(t, nil)
	app.openInlayInspectorCmd()
	s := app.inlayInspector
	if s == nil || s.request == nil {
		t.Fatal("reader not opened")
	}
	r := s.request
	r.started = true
	r.deadline = time.Now().Add(-time.Second)
	app.syncInlayHints()
	text := inlayVisualReader(s)
	if app.inlayInspector != s || !s.shown || !strings.Contains(text, "Refresh Inlay Hints") {
		t.Fatal("timed-out request discarded its recovery reader", text)
	}
	if r.ctx.Err() == nil || ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("timeout did not cancel provider or changed source")
	}
}
