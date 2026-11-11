package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/shellport"
	"crude/snippet"
	"crude/tui/ui"
	"crude/types"
)

func TestParameterHelpFollowsArgumentTypingAndDeletion(t *testing.T) {
	app, service, change := automaticSignaturePending(t)
	automaticSignatureAcknowledge(t, app, change)
	ed := app.EditorState.Editor
	service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if !strings.Contains(signaturePaintedText(app), "join(first string,") {
		t.Fatal("initial parameter help did not appear")
	}

	for i, step := range []struct {
		key  tea.KeyPressMsg
		text string
	}{
		{automaticSignatureRune('x'), "join(x)"},
		{tea.KeyPressMsg{Code: tea.KeyBackspace}, "join()"},
		{automaticSignatureRune(','), "join(,)"},
		{automaticSignatureRune('2'), "join(,2)"},
	} {
		_, cmd = app.Update(step.key)
		runAppCommand(t, app, cmd)
		if ed.Value() != step.text {
			t.Fatalf("argument input changed the wrong source: %q, want %q", ed.Value(), step.text)
		}
		if app.signatureHelpIntent == nil {
			t.Fatalf("parameter help stopped following argument input %q", step.key.String())
		}
		if app.overlayHost.IsOpen(signatureHelpID) {
			t.Fatal("old parameter information remained visible over unacknowledged argument text")
		}
		change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1},
			fmt.Sprintf("argument-%d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID,
			Before: uint64(i + 2), Revision: uint64(i + 3), Accepted: true}); err != nil {
			t.Fatal(err)
		}
		service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
		service.answer.Signatures = append([]types.LSPSignature(nil), service.answer.Signatures...)
		service.answer.Signatures[0].Label = fmt.Sprintf("join(first string, count int) · answer %d", i+1)
		_, cmd = app.Update(struct{}{})
		runAppCommand(t, app, cmd)
		want := fmt.Sprintf("answer %d", i+1)
		if text := signaturePaintedText(app); !strings.Contains(text, want) {
			t.Fatalf("current argument signature was not painted: want %q in\n%s", want, text)
		}
		if app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
			t.Fatal("refreshing parameter help took editor focus")
		}
	}
	if service.calls.Load() != 5 {
		t.Fatalf("argument changes requested %d answers, want exactly 5", service.calls.Load())
	}
}

func TestParameterHelpCoalescesArgumentsBeforeAcknowledgement(t *testing.T) {
	app, service, opening := automaticSignaturePending(t)
	ed := app.EditorState.Editor
	for _, r := range "xy" {
		_, cmd := app.Update(automaticSignatureRune(r))
		runAppCommand(t, app, cmd)
	}
	if ed.Value() != "join(xy)" || service.calls.Load() != 0 || app.signatureHelpIntent == nil {
		t.Fatal("rapid argument typing lost its latest intent or queried speculative text")
	}
	automaticSignatureAcknowledge(t, app, opening)
	arguments, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "rapid-arguments", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: arguments.DocumentID, RequestID: arguments.RequestID,
		Before: 2, Revision: 3, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	source, ready := ed.CurrentInsightSource()
	if !ready {
		t.Fatal("fixture did not acknowledge all argument edits")
	}
	service.answer.Boundary.Source = source
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if service.calls.Load() != 1 || app.signatureHelp == nil || app.signatureHelp.source != source ||
		!strings.Contains(signaturePaintedText(app), "join(first string,") {
		t.Fatal("acknowledgement did not produce exactly one answer for the latest argument")
	}
}

func TestParameterHelpFollowsMultilineAndIndentedArguments(t *testing.T) {
	for _, step := range []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"new line", tea.KeyPressMsg{Code: tea.KeyEnter}, "join(\n    \n)"},
		{"indent", tea.KeyPressMsg{Code: tea.KeyTab}, "join(   )"},
	} {
		t.Run(step.name, func(t *testing.T) {
			app, service, opening := automaticSignaturePending(t)
			automaticSignatureAcknowledge(t, app, opening)
			ed := app.EditorState.Editor
			service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
			_, cmd := app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			_, cmd = app.Update(step.key)
			runAppCommand(t, app, cmd)
			if ed.Value() != step.want || app.signatureHelpIntent == nil {
				t.Fatalf("native %s lost argument help or changed source: %q", step.name, ed.Value())
			}
			change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "layout-arguments", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID,
				Before: 2, Revision: 3, Accepted: true}); err != nil {
				t.Fatal(err)
			}
			service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
			_, cmd = app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			if !strings.Contains(signaturePaintedText(app), "join(first string,") || service.calls.Load() != 2 ||
				app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
				t.Fatal("parameter help did not follow the acknowledged argument layout")
			}
		})
	}
}

func TestParameterHelpRefreshesAtNativeArgumentPositions(t *testing.T) {
	app, service := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	app.queueAutomaticSignatureHelp(ed, "")
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	before := ed.Value()
	source, _ := ed.CurrentInsightSource()
	for _, key := range []rune{tea.KeyLeft, tea.KeyRight} {
		_, cmd = app.Update(tea.KeyPressMsg{Code: key})
		runAppCommand(t, app, cmd)
		line, col := ed.LSPCursorPosition()
		if app.signatureHelp == nil || app.signatureHelp.line != line || app.signatureHelp.col != col ||
			app.signatureHelp.source != source || !strings.Contains(signaturePaintedText(app), "join(first string,") {
			t.Fatal("native caret navigation lost current-position parameter information")
		}
		if ed.Value() != before || app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
			t.Fatal("argument navigation changed source or editor focus")
		}
	}
	if service.calls.Load() != 3 {
		t.Fatal("caret navigation did not request exactly one current-position answer per key")
	}
	ed.SetCursorPosition(0, 0)
	_, cmd = app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if app.signatureHelp != nil || app.signatureHelpIntent != nil || service.calls.Load() != 3 {
		t.Fatal("an API caret change manufactured fresh interactive help")
	}
}

func TestParameterHelpFollowsSnippetFieldsAndFinalLanding(t *testing.T) {
	app, service := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	text, fields, err := snippet.Parse("join(${1:first}, ${2:count})$0")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.ApplyCompletion(types.CompletionItem{TextEdit: &types.LSPTextEdit{
		Line: 0, Col: 0, EndLine: 0, EndCol: 12, NewText: text,
	}, Snippet: fields}); err != nil {
		t.Fatal(err)
	}
	app.adoptEditorSnippet(ed)
	app.queueSignatureHelpAfterInsertion(ed)
	service.answer.Boundary.Source = acknowledgeCompletionEdit(t, ed, 1)
	service.answer.Signatures[0].ActiveParameter = 0
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if ed.Value() != "join(first, count)" || ed.SelectedText() != "first" ||
		!strings.Contains(signaturePaintedText(app), "Active parameter 1 of 2") {
		t.Fatal("the accepted first argument field lost its parameter information")
	}
	service.answer.Signatures = append([]types.LSPSignature(nil), service.answer.Signatures...)
	service.answer.Signatures[0].ActiveParameter = 1
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	runAppCommand(t, app, cmd)
	if ed.SelectedText() != "count" || !ed.SnippetActive() ||
		!strings.Contains(signaturePaintedText(app), "Active parameter 2 of 2") {
		t.Fatal("Tab did not refresh parameter information for the next argument field")
	}
	service.answer.Signatures = nil // the provider reports no call after its closer
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	runAppCommand(t, app, cmd)
	if ed.SnippetActive() || ed.HasSelection() || ed.CursorCol() != len("join(first, count)") ||
		app.signatureHelp != nil || app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("the final field landing retained an old argument signature")
	}
	if ed.Value() != "join(first, count)" || service.calls.Load() != 3 {
		t.Fatal("field navigation edited source or repeated a semantic request")
	}
}

type signatureCompletionService struct {
	*completionTestService
	answer types.LSPSignatureHelp
}

func (s *signatureCompletionService) RequestSignatureHelp(context.Context, string, int, int, types.LSPSignatureContext) (types.LSPSignatureHelp, error) {
	return s.answer, nil
}

type signatureCompletionPort struct {
	*completionTestPort
	signatures *signatureCompletionService
}

func (p *signatureCompletionPort) LSPMgr() shellport.LSPService { return p.signatures }

func TestCompletionAndParameterHelpRemainReadableTogether(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app, port := completionTestApp(t, size[0], size[1], "join")
			service := &signatureCompletionService{completionTestService: port.service}
			app.ws = &signatureCompletionPort{completionTestPort: port, signatures: service}
			completionTestKey(t, app, automaticSignatureRune('('))
			source := acknowledgeCompletionEdit(t, app.EditorState.Editor, 41)
			port.set.Boundary.Source, port.set.Col = source, 5
			service.answer = types.LSPSignatureHelp{Boundary: port.set.Boundary, Signatures: []types.LSPSignature{{
				Label: "join(value)", ActiveParameter: 0,
				Parameters: []types.LSPSignatureParameter{{Label: "value", Documentation: "Argument to repeat."}},
			}}}
			_, cmd := app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			if !strings.Contains(signaturePaintedText(app), "join(value)") {
				t.Fatal("native opening parenthesis did not show parameter help")
			}

			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			if !strings.Contains(completionPaintedText(app), "duplicate") ||
				!strings.Contains(signaturePaintedText(app), "join(value)") {
				t.Fatal("opening argument suggestions removed the parameter information")
			}
			for _, resized := range [][2]int{size, {80, 24}, {160, 40}} {
				_, cmd = app.Update(tea.WindowSizeMsg{Width: resized[0], Height: resized[1]})
				runAppCommand(t, app, cmd)
				app.View()
				signature, completion := app.overlayHost.Window(signatureHelpID), app.overlayHost.Window(ui.OverlayCompletion)
				if signature == nil || completion == nil {
					t.Fatal("resize lost one of the two editor aids")
				}
				sx, sy := signature.Position()
				sw, sh := signature.Size()
				cx, cy := completion.Position()
				cw, ch := completion.Size()
				if sx < cx+cw && cx < sx+sw && sy < cy+ch && cy < sy+sh {
					t.Fatalf("suggestions cover parameter information at %v: signature=%d,%d %dx%d completion=%d,%d %dx%d",
						resized, sx, sy, sw, sh, cx, cy, cw, ch)
				}
				if sx < 0 || sy < 0 || sx+sw > resized[0] || sy+sh > resized[1] {
					t.Fatal("parameter information left the terminal bounds")
				}
			}
			if app.focusTarget() != FocusEditor || app.focusedPopup() != nil ||
				app.EditorState.Editor.Value() != "join()" {
				t.Fatal("coexisting editor aids changed focus or source")
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})
			if app.overlayHost.IsOpen(ui.OverlayCompletion) || !app.overlayHost.IsOpen(signatureHelpID) {
				t.Fatal("first Escape did not dismiss only the suggestion list")
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})
			if app.overlayHost.IsOpen(signatureHelpID) || !app.EditorState.Editor.IsOpen() {
				t.Fatal("second Escape did not dismiss only the parameter information")
			}
		})
	}
}
