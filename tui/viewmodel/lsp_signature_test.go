package viewmodel

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/shellport"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/termdock"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type signatureTestService struct {
	shellport.LSPService
	context   uint64
	answer    types.LSPSignatureHelp
	err       error
	calls     atomic.Int64
	cancelled atomic.Bool
	notReady  bool
}

func (s *signatureTestService) ServerReady() bool { return !s.notReady }
func (s *signatureTestService) ContextCursor() (string, uint64) {
	return "signature-owner", s.context
}
func (s *signatureTestService) RequestSignatureHelp(ctx context.Context, _ string, _, _ int) (types.LSPSignatureHelp, error) {
	s.calls.Add(1)
	s.cancelled.Store(ctx.Err() != nil)
	return s.answer, s.err // deliberately ignores cancellation to test the UI fence
}

type signatureTestWorkspace struct {
	shellport.Workspace
	service *signatureTestService
}

func (w *signatureTestWorkspace) LSPMgr() shellport.LSPService { return w.service }

func signatureTestApp(t *testing.T, width int) (*App, *signatureTestService) {
	t.Helper()
	app, ws := newRealShellApp(t)
	path := filepath.Join(ws.RootPath, "main.go")
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	snapshot := document.Snapshot{ID: "signature-document", Path: path, Revision: 1,
		Text: []byte("join(\"x\", 2)"), Saved: []byte("join(\"x\", 2)"),
		AnalysisGeneration: "signature-owner", AnalysisLifetime: "signature-open"}
	if err := app.EditorState.Editor.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	app.EditorState.Editor.SetCursorPosition(0, 10)
	source, current := app.EditorState.Editor.CurrentInsightSource()
	if !current {
		t.Fatal("fixture failed to install canonical source metadata")
	}
	service := &signatureTestService{LSPService: app.ws.LSPMgr(), context: 7, answer: types.LSPSignatureHelp{
		Boundary: types.InsightBoundary{Source: source, Context: types.InsightContext{Generation: 7, UnknownInputs: []string{"build flags are unknown"}},
			Producer: types.InsightProducer{InstanceID: "instance", Provider: "fixture-server"}},
		Signatures: []types.LSPSignature{{Label: "join(first string, count int)", Documentation: "Independent signature fixture.",
			ActiveParameter: 1, Parameters: []types.LSPSignatureParameter{{Label: "first string"}, {Label: "count int", Documentation: "Number of copies."}}}},
	}}
	original := app.ws
	app.ws = &signatureTestWorkspace{Workspace: original, service: service}
	t.Cleanup(func() { app.closeSignatureHelp(); app.ws = original })
	app.width, app.height = width, 32
	app.Input.SetInput("keep the private draft")
	app.setFocusEditor()
	app.paintShellFrame()
	return app, service
}

func signaturePaintedText(app *App) string {
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	fw := app.overlayHost.Window(signatureHelpID)
	if fw == nil {
		return ""
	}
	w, h := fw.Size()
	surf := surface.NewSurface("signature-test", w, h, app.mainSurf.Buffer().Get(0, 0))
	fw.Content.Render(surf, app.renderCtx)
	var text strings.Builder
	for y := range h {
		text.WriteString(shellchat.PlainRow(surf.Buffer(), y, w))
		text.WriteByte('\n')
	}
	return text.String()
}

func TestSignatureEditorEntryPaintAndEscapePreserveText(t *testing.T) {
	for _, width := range []int{160, 80, 40} {
		app, service := signatureTestApp(t, width)
		before := app.EditorState.Editor.Value()
		_, cmd := app.dispatchEditorKey(tea.KeyPressMsg{Code: 'i', Mod: tea.ModAlt})
		if cmd == nil || app.signatureHelp == nil {
			t.Fatal("registry-backed Alt+I did not request parameter information")
		}
		app.Update(cmd())
		if signatureHasNotice(app, "Requesting parameter information") {
			t.Fatal("completed parameter reader retained a loading notice")
		}
		text := signaturePaintedText(app)
		for _, marker := range []string{"Parameter information", "join(first string,", "count int", "Active parameter 2 of 2", "Number of copies."} {
			// The 40-column reader wraps labels, not an invented wider surface.
			if !strings.Contains(text, marker) {
				t.Fatalf("width=%d missing %q in painted parameter reader:\n%s", width, marker, text)
			}
		}
		if service.calls.Load() != 1 {
			t.Fatal("parameter information launched duplicate requests")
		}
		body := app.overlayHost.Window(signatureHelpID).Content.(*ui.WindowFrameWidget).Body().(*ui.TextArea)
		cursor := body.CursorFlat()
		app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		if body.Value() == "" || body.CursorFlat() <= cursor || app.EditorState.Editor.Value() != before {
			t.Fatal("reader navigation changed source or lost its text")
		}
		app.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModShift})
		if !body.HasActiveSelection() {
			t.Fatal("read-only parameter text did not support keyboard selection")
		}
		textBefore := body.Value()
		app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
		if body.Value() != textBefore || app.EditorState.Editor.Value() != before {
			t.Fatal("typing into the reader changed read-only text or source")
		}
		app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if app.overlayHost.IsOpen(signatureHelpID) || !app.EditorState.Editor.IsOpen() ||
			app.EditorState.Editor.Value() != before || app.Input.Value() != "keep the private draft" {
			t.Fatal("Escape dismissed source/draft instead of the parameter reader")
		}
	}
}

func TestSignatureRepliesCannotFollowAnInvalidatedInvocation(t *testing.T) {
	for _, change := range []string{"cursor", "edit", "workspace", "reopen", "escape", "timeout"} {
		t.Run(change, func(t *testing.T) {
			app, _ := signatureTestApp(t, 100)
			command := app.requestSignatureHelpCmd()
			if command == nil {
				t.Fatal("missing request")
			}
			switch change {
			case "cursor":
				app.EditorState.Editor.SetCursorPosition(0, 0)
			case "edit":
				app.EditorState.Editor.InsertRune('X')
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "reopen":
				snapshot := app.EditorState.Editor.DocumentSnapshot()
				snapshot.AnalysisLifetime = "another-open"
				if err := app.EditorState.Editor.InstallDocument(snapshot); err != nil {
					t.Fatal(err)
				}
			case "escape":
				app.dispatchEditorKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			case "timeout":
				app.signatureHelp.deadline = time.Now().Add(-time.Second)
			}
			app.syncSignatureHelp()
			app.Update(command())
			if app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("late semantic reply reopened a retired parameter reader")
			}
			if change == "timeout" && !signatureHasNotice(app, "Parameter information timed out or its context could not be observed") {
				t.Fatal("unobserved reply deadline was silently retired")
			}
		})
	}
}

func TestSignatureContextPublicationBarrierAndRetirement(t *testing.T) {
	app, service := signatureTestApp(t, 100)
	service.context = 6 // current replica precedes the result's context 7
	cmd := app.requestSignatureHelpCmd()
	app.Update(cmd())
	if app.signatureHelp == nil || app.signatureHelp.shown || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("result painted against an older replica context")
	}
	service.context = 7
	app.syncSignatureHelp()
	if !app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("exact context publication did not release its retained result")
	}
	service.context = 8
	app.syncSignatureHelp()
	if app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("parameter reader survived changed build/provider context")
	}
}

func TestSignatureSupersedingRequestCannotReuseOldToken(t *testing.T) {
	app, _ := signatureTestApp(t, 100)
	first := app.requestSignatureHelpCmd()
	old := app.signatureHelp
	second := app.requestSignatureHelpCmd()
	app.Update(first())
	if app.signatureHelp == old || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("old invocation overwrote a newer one at identical source/caret")
	}
	app.Update(second())
	if !app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("new exact invocation did not open")
	}
}

func TestSignaturePendingSourceUnsupportedAndEmptyStayDistinct(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		app, service := signatureTestApp(t, 100)
		app.EditorState.Editor.InsertRune('X')
		if cmd := app.requestSignatureHelpCmd(); cmd != nil || service.calls.Load() != 0 {
			t.Fatal("unacknowledged optimistic text queried the canonical provider")
		}
	})
	for _, kind := range []string{"unsupported", "empty"} {
		t.Run(kind, func(t *testing.T) {
			app, service := signatureTestApp(t, 100)
			if kind == "unsupported" {
				service.err = errors.New("provider does not advertise signature help")
			} else {
				service.answer.Signatures = nil
			}
			cmd := app.requestSignatureHelpCmd()
			app.Update(cmd())
			if app.overlayHost.IsOpen(signatureHelpID) || app.signatureHelp != nil {
				t.Fatal("absent/unsupported information fabricated a semantic reader")
			}
			want := "No parameter information at this position"
			if kind == "unsupported" {
				want = "Parameter information: provider does not advertise signature help"
			}
			if !signatureHasNotice(app, want) || signatureHasNotice(app, "Requesting parameter information") {
				t.Fatal("supported absence, refusal, or loading status lost its distinction", app.ws.Status().Notifications(false))
			}
		})
	}
}

func signatureHasNotice(app *App, summary string) bool {
	for _, notice := range app.ws.Status().Notifications(false) {
		if notice.Summary == summary {
			return true
		}
	}
	return false
}

func TestSignatureEmptyEvidenceUsesTheSameSourceContextFence(t *testing.T) {
	for _, change := range []string{"source", "owner", "newer-context", "replica-behind", "exact"} {
		t.Run(change, func(t *testing.T) {
			app, service := signatureTestApp(t, 100)
			service.answer.Signatures = nil
			switch change {
			case "source":
				service.answer.Boundary.Source.Lifetime = "old-open"
			case "owner":
				service.answer.Boundary.Source.WorkspaceGeneration = "replaced-owner"
			case "newer-context":
				service.context = 8
			case "replica-behind":
				service.context = 6
			}
			cmd := app.requestSignatureHelpCmd()
			app.Update(cmd())
			const empty = "No parameter information at this position"
			if change == "exact" {
				if !signatureHasNotice(app, empty) || app.signatureHelp != nil {
					t.Fatal("exact supported empty result lost its distinct status")
				}
				return
			}
			if signatureHasNotice(app, empty) || app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("stale/unobserved empty answer became visible negative semantic evidence")
			}
			if change == "replica-behind" {
				if app.signatureHelp == nil {
					t.Fatal("future-context empty result did not wait for its publication")
				}
				service.context = 7
				app.syncSignatureHelp()
				if !signatureHasNotice(app, empty) || app.signatureHelp != nil {
					t.Fatal("exact published context did not release eligible empty evidence")
				}
			} else if app.signatureHelp != nil {
				t.Fatal("stale empty result remained current")
			}
		})
	}
}

func TestSignaturePendingReaderRetiresOnRenderedDockFocus(t *testing.T) {
	app, service := signatureTestApp(t, 160)
	screen := app.ensureTermScreen("signature-focus", "Signature focus PTY")
	screen.SetControlled(true)
	var sent []shellterm.Command
	screen.SetCommand(func(command shellterm.Command) tea.Cmd { sent = append(sent, command); return nil })
	app.termDock.Mount("signature-focus", screen)
	app.termDock.ShowPanel(termdock.PanelTerminal)
	app.setFocusEditor()
	app.paintShellFrame()
	cmd := app.requestSignatureHelpCmd()
	r, ok := app.rootHost.ScreenRect(screen.ID())
	if !ok || r.Empty() {
		t.Fatal("destination terminal is not actually rendered")
	}
	app.Update(tea.MouseClickMsg{X: r.X + 2, Y: r.Y, Button: tea.MouseLeft})
	app.Update(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y, Button: tea.MouseLeft})
	if !app.terminalDockOwnsKeys() || app.signatureHelp != nil {
		t.Fatal("rendered dock focus retained the pending editor invocation")
	}
	app.Update(cmd()) // a backend that ignores cancellation must not steal focus
	app.Update(tea.PasteMsg{Content: "destination input"})
	if !service.cancelled.Load() || app.overlayHost.IsOpen(signatureHelpID) ||
		!app.terminalDockOwnsKeys() || len(sent) != 1 || sent[0].Kind != shellterm.CommandPaste {
		t.Fatal("late parameter reply stole terminal ownership or the request context survived", sent)
	}
	if signatureHasNotice(app, "Requesting parameter information") {
		t.Fatal("cancelled invocation retained its loading notice")
	}
}

func TestSignaturePendingReaderRetiresOnNonmodalSessionPanel(t *testing.T) {
	app, service := signatureTestApp(t, 160)
	original := app.ws.(*signatureTestWorkspace)
	app.ws = original.Workspace
	editorGroup := app.Win.ActiveGroup()
	sessionGroup := app.Win.SplitGroup(editorGroup, ui.Horizontal, false)
	app.focusMainGroup(sessionGroup)
	selectDeckSession(t, app, &workspace.Session{SessionID: "signature-focus-session",
		Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}, "signature-agent")
	app.openSessionSettings()
	projection := app.activeSessionDeck()
	if projection == nil || app.sessionDeckModal() {
		t.Fatal("expected a nonmodal interactive session panel")
	}
	app.paintShellFrame()
	target := app.sessionDeckRect
	app.focusMainGroup(editorGroup)
	app.ws = original
	app.setFocusEditor()
	app.paintShellFrame()
	cmd := app.requestSignatureHelpCmd()
	if target.Empty() || cmd == nil {
		t.Fatal("split-panel/editor setup did not expose the real invocation")
	}
	app.Update(tea.MouseClickMsg{X: target.X + target.W/2, Y: target.Y + 2, Button: tea.MouseLeft})
	app.Update(tea.MouseReleaseMsg{X: target.X + target.W/2, Y: target.Y + 2, Button: tea.MouseLeft})
	if !app.sessionDeckFocused() || app.signatureHelp != nil {
		t.Fatal("session panel focus retained the editor invocation")
	}
	app.Update(cmd())
	if !service.cancelled.Load() || !app.sessionDeckFocused() || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("late parameter reply stole the nonmodal panel focus")
	}
}

func TestSignatureLoadingNoticeIsExactAndClearsOnRetirement(t *testing.T) {
	app, service := signatureTestApp(t, 100)
	cmd := app.requestSignatureHelpCmd()
	app.Feedback.NotifyInfo("unrelated user feedback")
	app.closeSignatureHelp()
	app.Update(cmd())
	if !service.cancelled.Load() || !signatureHasNotice(app, "unrelated user feedback") ||
		signatureHasNotice(app, "Requesting parameter information") {
		t.Fatal("retirement kept loading feedback or dismissed an unrelated notice")
	}
	service.notReady = true
	if cmd := app.requestSignatureHelpCmd(); cmd != nil ||
		!signatureHasNotice(app, "Parameter information needs a connected language server") {
		t.Fatal("no-server state did not give its distinct refusal")
	}
}
