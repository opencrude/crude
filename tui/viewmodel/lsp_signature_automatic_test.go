package viewmodel

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/document"
)

// This regression enters through the real editor key router. Its canonical
// receipt is fixture input, not a synthetic request or feature action.
func TestAutomaticSignatureTypingWaitsForExactSourceWithoutStealingEditor(t *testing.T) {
	for _, tc := range []struct{ before, key, after string }{
		{"join", "(", "join()"},
		{`join("x"`, ",", `join("x",`},
	} {
		t.Run(tc.key, func(t *testing.T) {
			app, service := signatureTestApp(t, 100)
			ed := app.EditorState.Editor
			initial := ed.DocumentSnapshot()
			initial.Text, initial.Saved = []byte(tc.before), []byte(tc.before)
			if err := ed.InstallDocument(initial); err != nil {
				t.Fatal(err)
			}
			ed.SetCursorPosition(0, len(tc.before))
			app.setFocusEditor()
			app.paintShellFrame()
			_, keyCommand := app.dispatchEditorKey(tea.KeyPressMsg{Code: rune(tc.key[0]), Text: tc.key})
			if ed.Value() != tc.after {
				t.Fatalf("native trigger changed source unexpectedly: %q", ed.Value())
			}
			if service.calls.Load() != 0 || app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("automatic parameter information queried unacknowledged source")
			}
			runAppCommand(t, app, keyCommand)
			change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "typed-trigger", "")
			if err != nil || len(change.Edits) == 0 {
				t.Fatal("fixture did not capture native typing", change, err)
			}
			if err = ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
				t.Fatal(err)
			}
			source, current := ed.CurrentInsightSource()
			if !current {
				t.Fatal("fixture did not acknowledge the typed source")
			}
			service.answer.Boundary.Source = source
			// A normal non-recurring product event lets the Update owner act on
			// the now-current source. No parameter-information shortcut is sent.
			_, command := app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
			runAppCommand(t, app, command)
			if service.calls.Load() != 1 || app.signatureHelp == nil {
				t.Fatal("typing a call delimiter did not request source-qualified parameter help after acknowledgement")
			}
			if text := signaturePaintedText(app); !strings.Contains(text, "join(first string,") {
				t.Fatalf("automatic parameter help was not painted: %s", text)
			}
			if app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
				t.Fatal("automatic parameter help stole typing focus from the editor")
			}
			if ed.Value() != tc.after || app.Input.Value() != "keep the private draft" {
				t.Fatal("automatic parameter help changed source or the private composer")
			}
		})
	}
}

func automaticSignaturePending(t *testing.T) (*App, *signatureTestService, document.Change) {
	t.Helper()
	app, service := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	initial := ed.DocumentSnapshot()
	initial.Text, initial.Saved = []byte("join"), []byte("join")
	if err := ed.InstallDocument(initial); err != nil {
		t.Fatal(err)
	}
	ed.SetCursorPosition(0, 4)
	app.paintShellFrame()
	_, cmd := app.dispatchEditorKey(tea.KeyPressMsg{Code: '(', Text: "("})
	runAppCommand(t, app, cmd)
	if app.signatureHelpIntent == nil || app.signatureHelp != nil || service.calls.Load() != 0 {
		t.Fatal("native intent queried before canonical acknowledgement")
	}
	change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "pending-delimiter", "")
	if err != nil || len(change.Edits) == 0 {
		t.Fatal(change, err)
	}
	return app, service, change
}

func automaticSignatureAcknowledge(t *testing.T, app *App, change document.Change) {
	t.Helper()
	if err := app.EditorState.Editor.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID,
		RequestID: change.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticSignatureAcknowledgementCannotReviveDepartedInput(t *testing.T) {
	for _, boundary := range []string{"edit", "caret", "selection", "focus", "reopen", "workspace", "escape", "timeout", "provider", "blur"} {
		t.Run(boundary, func(t *testing.T) {
			app, service, change := automaticSignaturePending(t)
			ed := app.EditorState.Editor
			automaticSignatureAcknowledge(t, app, change)
			switch boundary {
			case "edit":
				ed.InsertRune('x')
			case "caret":
				ed.SetCursorPosition(0, 0)
			case "selection":
				ed.SetSelection(0, 0, 0, 1)
			case "focus":
				app.setFocusTree()
			case "reopen":
				snapshot := ed.DocumentSnapshot()
				snapshot.AnalysisLifetime = "equal-text-reopened-source"
				if err := ed.InstallDocument(snapshot); err != nil {
					t.Fatal(err)
				}
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "escape":
				app.dispatchEditorKey(tea.KeyPressMsg{Code: tea.KeyEscape})
				if !ed.IsOpen() {
					t.Fatal("Escape closed editor before retiring queued help")
				}
			case "timeout":
				app.signatureHelpIntent.deadline = time.Now().Add(-time.Second)
			case "provider":
				service.notReady = true
			case "blur":
				_, cmd := app.Update(tea.BlurMsg{})
				runAppCommand(t, app, cmd)
			}
			service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
			_, cmd := app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
			runAppCommand(t, app, cmd)
			if service.calls.Load() != 0 || app.signatureHelpIntent != nil || app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("canonical acknowledgement revived help after input departure", boundary)
			}
		})
	}
}

func TestAutomaticSignatureWaitsForInitialContextWithoutDroppingNativeIntent(t *testing.T) {
	app, service, change := automaticSignaturePending(t)
	automaticSignatureAcknowledge(t, app, change)
	service.answer.Boundary.Source, _ = app.EditorState.Editor.CurrentInsightSource()
	service.context = 0
	_, cmd := app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	runAppCommand(t, app, cmd)
	if service.calls.Load() != 0 || app.signatureHelpIntent == nil || app.signatureHelp != nil {
		t.Fatal("ready provider without published initial context consumed the native help intent")
	}
	service.context, service.answer.Boundary.Context.Generation = 9, 9
	_, cmd = app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	runAppCommand(t, app, cmd)
	if service.calls.Load() != 1 || !app.overlayHost.IsOpen(signatureHelpID) || app.focusedPopup() != nil {
		t.Fatal("initial context publication did not admit one still-current native input intent")
	}
}

func TestAutomaticSignatureUsesOriginalContextPublicationBarrier(t *testing.T) {
	app, service, change := automaticSignaturePending(t)
	// The input was observed at cursor 7; the request is admitted at that same
	// cursor. Its result may independently lead the replica with context 8.
	automaticSignatureAcknowledge(t, app, change)
	service.answer.Boundary.Source, _ = app.EditorState.Editor.CurrentInsightSource()
	service.answer.Boundary.Context.Generation = 8
	_, cmd := app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	runAppCommand(t, app, cmd)
	if service.calls.Load() != 1 || app.signatureHelp == nil || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("automatic help bypassed its result's original context cursor")
	}
	service.context = 8
	_, cmd = app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	runAppCommand(t, app, cmd)
	if !app.overlayHost.IsOpen(signatureHelpID) || app.focusedPopup() != nil {
		t.Fatal("eligible published automatic result was lost or stole input")
	}
	service.context = 9
	_, cmd = app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	runAppCommand(t, app, cmd)
	if app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("automatic result survived replacement context")
	}
}

func TestAutomaticSignatureCaretCardReanchorsOnFirstResizedFrame(t *testing.T) {
	app, service, change := automaticSignaturePending(t)
	automaticSignatureAcknowledge(t, app, change)
	ed := app.EditorState.Editor
	service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
	_, cmd := app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	runAppCommand(t, app, cmd)
	for _, size := range [][2]int{{160, 40}, {80, 24}, {40, 20}, {100, 32}} {
		_, cmd = app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		runAppCommand(t, app, cmd)
		app.advanceReveals(time.Now().Add(2 * revealDuration))
		app.View()
		fw := app.overlayHost.Window(signatureHelpID)
		if fw == nil {
			t.Fatal("resizing discarded visible same-source help")
		}
		rect, mounted := app.screenRectOf(app.editorScreen.ID())
		caretX, caretY := ed.CursorScreenPosition(rect.X, rect.Y)
		x, y := fw.Position()
		w, h := fw.Size()
		if !mounted || x < 0 || y < 0 || x+w > size[0] || y+h > size[1] || x > caretX || x+w <= caretX ||
			(y != caretY+1 && y+h != caretY) {
			t.Fatalf("first resized frame lost actual caret anchoring: screen=%v caret=%d:%d popup=%d:%d %dx%d",
				size, caretX, caretY, x, y, w, h)
		}
	}
	if service.calls.Load() != 1 {
		t.Fatal("presentation resize repeated a semantic request")
	}
}

func TestSignatureAndSymbolReadersAnchorAtMountedUnicodeCaret(t *testing.T) {
	for _, kind := range []string{"signature", "hover"} {
		t.Run(kind, func(t *testing.T) {
			app, service := signatureTestApp(t, 160)
			ed := app.EditorState.Editor
			snapshot := ed.DocumentSnapshot()
			snapshot.Text, snapshot.Saved = []byte("\t界🙂 join(x)"), []byte("\t界🙂 join(x)")
			snapshot.DiskHash = sha256.Sum256(snapshot.Saved)
			if err := ed.InstallDocument(snapshot); err != nil {
				t.Fatal(err)
			}
			ed.SetCursorPosition(0, 14)
			app.paintShellFrame()
			source, current := ed.CurrentInsightSource()
			if !current {
				t.Fatal("fixture lost canonical identity")
			}
			service.answer.Boundary.Source = source
			id := signatureHelpID
			if kind == "signature" {
				runAppCommand(t, app, app.requestSignatureHelpCmd())
			} else {
				line, col := ed.LSPCursorPosition()
				app.handleLSPHoverResult(lspHoverResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(),
					path: ed.FilePath(), text: "Literal symbol documentation.", line: line, col: col})
				id = "lsp-hover"
			}
			fw := app.overlayHost.Window(id)
			if fw == nil {
				t.Fatal("explicit reader was not mounted")
			}
			rect, _ := app.screenRectOf(app.editorScreen.ID())
			cx, cy := ed.CursorScreenPosition(rect.X, rect.Y)
			x, y := fw.Position()
			w, h := fw.Size()
			if x > cx || x+w <= cx || y != cy+1 || app.focusedPopup() != fw {
				t.Fatalf("explicit reader was centered instead of caret-anchored: cursor=%d:%d window=%d:%d %dx%d", cx, cy, x, y, w, h)
			}
			app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if !ed.IsOpen() || ed.Value() != "\t界🙂 join(x)" || ed.CursorCol() != 14 {
				t.Fatal("reader changed underlying source/caret")
			}
		})
	}
}

func TestSignatureFullReaderShrinksInsteadOfCoveringItsCaretOnShortGrid(t *testing.T) {
	for _, kind := range []string{"signature", "hover"} {
		t.Run(kind, func(t *testing.T) {
			app, service := signatureTestApp(t, 80)
			ed := app.EditorState.Editor
			snapshot := ed.DocumentSnapshot()
			snapshot.Text = []byte("package journey\n\nvar _ = join(x,)\n")
			snapshot.Saved = append([]byte(nil), snapshot.Text...)
			if err := ed.InstallDocument(snapshot); err != nil {
				t.Fatal(err)
			}
			ed.SetCursorPosition(2, 15)
			app.width, app.height = 80, 24
			app.paintShellFrame()
			service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
			id := signatureHelpID
			if kind == "signature" {
				runAppCommand(t, app, app.requestSignatureHelpCmd())
			} else {
				line, col := ed.LSPCursorPosition()
				app.handleLSPHoverResult(lspHoverResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(),
					path: ed.FilePath(), text: strings.Repeat("Readable symbol documentation.\n", 20), line: line, col: col})
				id = "lsp-hover"
			}
			fw := app.overlayHost.Window(id)
			if fw == nil {
				t.Fatal("short-grid full reader did not open")
			}
			rect, mounted := app.screenRectOf(app.editorScreen.ID())
			cx, cy := ed.CursorScreenPosition(rect.X, rect.Y)
			x, y := fw.Position()
			w, h := fw.Size()
			if !mounted || h >= 18 || x > cx || x+w <= cx || (y != cy+1 && y+h != cy) ||
				y < 0 || y+h > 24 || x < 0 || x+w > 80 {
				t.Fatalf("full reader covered its actual caret instead of shrinking: caret=%d,%d popup=%d,%d %dx%d", cx, cy, x, y, w, h)
			}
			if app.focusedPopup() != fw {
				t.Fatal("shrinking the full reader removed explicit keyboard access")
			}
		})
	}
}
