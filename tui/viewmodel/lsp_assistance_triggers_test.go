package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/types"
	"strings"
	"testing"
)

func TestLSPAdvertisedCompletionCharacters(t *testing.T) {
	for _, test := range []struct {
		key  string
		want bool
	}{{":", true}, {"@", true}, {".", false}, {"(", false}} {
		t.Run(test.key, func(t *testing.T) {
			app, port := completionTestApp(t, 100, 32, "unit")
			port.service.aggregateNotReady = true // another shell may focus a file with no ready server
			port.service.assistance = &types.LSPAssistanceCapabilities{Completion: true, CompletionTriggers: []string{":", "@"}}
			ed := app.EditorState.Editor
			completionTestKey(t, app, tea.KeyPressMsg{Code: []rune(test.key)[0], Text: test.key})
			if got := app.completionIntent != nil; got != test.want {
				t.Fatalf("advertised trigger %q: intent=%t want %t", test.key, got, test.want)
			}
			if !test.want {
				return
			}
			source := acknowledgeCompletionEdit(t, ed, 41)
			port.set.Boundary.Source = source
			port.set.Line, port.set.Col = ed.LSPCursorPosition()
			_, cmd := app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			if !strings.Contains(completionPaintedText(app), "duplicate") || app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
				t.Fatal("advertised input did not produce readable passive suggestions")
			}
		})
	}
}

func TestLSPAdvertisedSignatureCharacters(t *testing.T) {
	for _, test := range []struct {
		key  string
		want bool
	}{{"?", true}, {"(", false}, {",", false}} {
		t.Run(test.key, func(t *testing.T) {
			app, service := signatureTestApp(t, 100)
			service.aggregateNotReady = true
			service.assistance = &types.LSPAssistanceCapabilities{SignatureHelp: true, SignatureTriggers: []string{"?"}}
			ed := app.EditorState.Editor
			ed.SetCursorPosition(0, len(ed.Value()))
			completionTestKey(t, app, tea.KeyPressMsg{Code: []rune(test.key)[0], Text: test.key})
			if got := app.signatureHelpIntent != nil; got != test.want {
				t.Fatalf("advertised signature trigger %q: intent=%t want %t", test.key, got, test.want)
			}
			if !test.want {
				return
			}
			change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "custom-trigger", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 1, Revision: 2, Accepted: true}); err != nil {
				t.Fatal(err)
			}
			service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
			_, cmd := app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			if !strings.Contains(signaturePaintedText(app), "join(first string,") || app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
				t.Fatal("advertised input did not show passive parameter information")
			}
		})
	}
}

func TestSignatureRetriggerKeepsOriginalAnswerAndUsesAdvertisedCharacter(t *testing.T) {
	app, service, opening := automaticSignaturePending(t)
	automaticSignatureAcknowledge(t, app, opening)
	ed := app.EditorState.Editor
	service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
	original := []byte(`{"signatures":[{"label":"join(first string, count int)","documentation":{"kind":"markdown","value":"**Original**"}}],"activeSignature":0}`)
	service.answer.Original = original
	service.assistance = &types.LSPAssistanceCapabilities{SignatureHelp: true, SignatureTriggers: []string{"("}, SignatureRetriggers: []string{";"}}
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if !app.overlayHost.IsOpen(signatureHelpID) {
		t.Fatal("initial parameter help is absent")
	}
	for i, key := range []string{"x", ";"} {
		completionTestKey(t, app, tea.KeyPressMsg{Code: []rune(key)[0], Text: key})
		intent := app.signatureHelpIntent
		want := types.LSPSignatureContentChange
		if key == ";" {
			want = types.LSPSignatureTriggerCharacter
		}
		if intent == nil || !intent.trigger.IsRetrigger || intent.trigger.TriggerKind != want || string(intent.trigger.ActiveSignatureHelp) != string(original) {
			t.Fatalf("new argument lost original signature context: %+v", intent)
		}
		change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "retrigger-"+key, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: uint64(i + 2), Revision: uint64(i + 3), Accepted: true}); err != nil {
			t.Fatal(err)
		}
		service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
		_, cmd = app.Update(struct{}{})
		runAppCommand(t, app, cmd)
		got := service.lastTrigger.Load()
		if got == nil || got.TriggerKind != want || !got.IsRetrigger || string(got.ActiveSignatureHelp) != string(original) || !app.overlayHost.IsOpen(signatureHelpID) {
			t.Fatal("retrigger context or current help was lost after acknowledgement", got)
		}
	}
}

func TestPendingCompletionDoesNotInheritReplacementServer(t *testing.T) {
	app, port := completionTestApp(t, 100, 32, "unit")
	port.service.assistance = &types.LSPAssistanceCapabilities{Completion: true, CompletionTriggers: []string{":"}, Producer: types.InsightProducer{InstanceID: "old"}}
	completionTestKey(t, app, tea.KeyPressMsg{Code: ':', Text: ":"})
	if app.completionIntent == nil {
		t.Fatal("no pending intent")
	}
	port.service.assistance.Producer.InstanceID = "new"
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if app.completionIntent != nil || len(port.requests) != 0 {
		t.Fatal("old trigger reached the replacement server")
	}
}

func TestSignaturePendingRetriggerPreservesOriginalContext(t *testing.T) {
	app, service, opening := automaticSignaturePending(t)
	automaticSignatureAcknowledge(t, app, opening)
	ed := app.EditorState.Editor
	original := []byte(`{"signatures":[{"label":"join(first string, count int)"}]}`)
	service.answer.Original = original
	service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	completionTestKey(t, app, tea.KeyPressMsg{Code: 'x', Text: "x"})
	change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "automatic-signature-fixture", Epoch: 1}, "first-argument", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 2, Revision: 3, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	service.answer.Boundary.Source, _ = ed.CurrentInsightSource()
	_, pending := app.Update(struct{}{}) // keep the new request unanswered while typing continues
	if app.signatureHelp == nil || app.signatureHelp.result != nil {
		t.Fatal("no unanswered retrigger")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: 'y', Text: "y"})
	intent := app.signatureHelpIntent
	if intent == nil || !intent.trigger.IsRetrigger || string(intent.trigger.ActiveSignatureHelp) != string(original) {
		t.Fatal("typing during the pending request forgot the original active signature", intent)
	}
	runAppCommand(t, app, pending) // an old result may not paint against the new edit
	if app.overlayHost.IsOpen(signatureHelpID) || ed.Value() != "join(xy)" {
		t.Fatal("late response changed current source or revived old help")
	}
}

func TestExplicitAssistanceSurvivesCapabilityPublicationLag(t *testing.T) {
	t.Run("completion", func(t *testing.T) {
		app, port := completionTestApp(t, 100, 32, "pr")
		port.service.assistance = &types.LSPAssistanceCapabilities{} // source is acknowledged; its capability topic has not arrived
		completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
		if len(port.requests) != 1 || !strings.Contains(completionPaintedText(app), "duplicate") {
			t.Fatal("Ctrl+Space was discarded before the authoritative LSP request")
		}
	})
	t.Run("parameters", func(t *testing.T) {
		app, service := signatureTestApp(t, 100)
		service.assistance = &types.LSPAssistanceCapabilities{}
		completionTestKey(t, app, tea.KeyPressMsg{Code: 'i', Mod: tea.ModAlt})
		if service.calls.Load() != 1 || !strings.Contains(signaturePaintedText(app), "join(first string,") {
			t.Fatal("Alt+I was discarded before the authoritative LSP request")
		}
	})
}

func TestAutomaticCompletionWaitsForInitializingFileCapabilities(t *testing.T) {
	for _, test := range []struct {
		before, key string
		kind        int
	}{{"du", "p", 1}, {"unit", ":", 2}, {"unit", "#", 0}} {
		t.Run(test.key, func(t *testing.T) {
			app, port := completionTestApp(t, 100, 32, test.before)
			ed := app.EditorState.Editor
			port.service.assistance = &types.LSPAssistanceCapabilities{}
			completionTestKey(t, app, tea.KeyPressMsg{Code: []rune(test.key)[0], Text: test.key})
			if app.completionIntent == nil || len(port.requests) != 0 {
				t.Fatal("typing while the file server initializes lost its one pending intent")
			}
			source := acknowledgeCompletionEdit(t, ed, 41)
			port.set.Boundary.Source = source
			port.set.Line, port.set.Col = ed.LSPCursorPosition()
			for i := range port.set.Entries {
				port.set.Entries[i].FilterText = port.set.Entries[i].Insertion.Label
			}
			_, cmd := app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			if app.completionIntent == nil || len(port.requests) != 0 {
				t.Fatal("source acknowledgement consumed intent before server capabilities arrived")
			}
			port.service.assistance = &types.LSPAssistanceCapabilities{Completion: true, CompletionTriggers: []string{":"}, Producer: types.InsightProducer{InstanceID: "initialized"}}
			port.set.Boundary.Producer = port.service.assistance.Producer
			_, cmd = app.Update(struct{}{})
			runAppCommand(t, app, cmd)
			if test.kind == 0 {
				if len(port.requests) != 0 || app.completionIntent != nil || app.completion != nil {
					t.Fatal("unsupported startup punctuation was guessed into a request")
				}
				return
			}
			if len(port.requests) != 1 || port.requests[0].trigger.TriggerKind != test.kind || port.requests[0].trigger.Producer.InstanceID != "initialized" || !strings.Contains(completionPaintedText(app), "duplicate") {
				t.Fatal("server readiness did not honor the pending native input", port.requests)
			}
		})
	}
}
