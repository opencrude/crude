package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/types"
)

func TestAutomaticCompletionWaitsPastDelayForAcknowledgement(t *testing.T) {
	app, port := completionTestApp(t, 100, 32, "pr")
	ed := app.EditorState.Editor
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	runAppCommand(t, app, cmd) // the debounce expires before the withheld receipt
	if app.completionIntent == nil || app.completion != nil || len(port.requests) != 0 {
		t.Fatal("typing intent was dropped or unacknowledged source was queried")
	}
	change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "typing-test", Epoch: 1}, "typed", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 41, Revision: 42, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	source, current := ed.CurrentInsightSource()
	if !current {
		t.Fatal("fixture did not acknowledge the edit")
	}
	port.set.Boundary.Source, port.set.Col = source, 3
	_, cmd = app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if app.completion == nil || app.completion.screen == nil || len(port.requests) != 1 ||
		port.requests[0].source != source || port.requests[0].col != 3 {
		t.Fatal("acknowledgement did not open suggestions over the typed source")
	}
}

func TestAutomaticCompletionIntentRetiresWithInputOwner(t *testing.T) {
	for _, mode := range []string{"escape", "blur", "caret", "disabled", "automatic off", "non-typing edit", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			app, port := completionTestApp(t, 100, 32, "pr")
			ed := app.EditorState.Editor
			_, cmd := app.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
			runAppCommand(t, app, cmd)
			if app.completionIntent == nil {
				t.Fatal("native typing did not retain its pending intent")
			}
			switch mode {
			case "escape":
				app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			case "blur":
				app.Update(tea.BlurMsg{})
			case "caret":
				app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			case "disabled":
				port.service.enabled = false
			case "automatic off":
				settings := types.DefaultLSPSettings()
				settings.AutoComplete = false
				port.service.settings = &settings
			case "non-typing edit":
				ed.InsertRune('x') // a non-typing mutation cannot inherit intent
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			}
			runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
			if app.completionIntent != nil || app.completion != nil || len(port.requests) != 0 || !ed.IsOpen() {
				t.Fatal("retired input queried, reopened a chooser or closed the editor")
			}
		})
	}
}

func TestCompletionAcceptancePreferenceLeavesOtherKeyAsEditing(t *testing.T) {
	for _, tc := range []struct {
		mode string
		key  rune
		want string
	}{
		{"tab", tea.KeyTab, "first"}, {"enter", tea.KeyEnter, "first"},
		{"tab", tea.KeyEnter, "pr\n"}, {"enter", tea.KeyTab, "pr    "},
	} {
		t.Run(tc.mode+"/"+tea.KeyPressMsg{Code: tc.key}.String(), func(t *testing.T) {
			app, port := completionTestApp(t, 100, 32, "pr")
			settings := types.DefaultLSPSettings()
			settings.CompletionAccept, settings.AutoComplete = tc.mode, false
			port.service.settings = &settings
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			completionRequireText(t, app, []string{map[string]string{"tab": "Tab: accept", "enter": "Enter: accept"}[tc.mode]}, nil)
			completionTestKey(t, app, tea.KeyPressMsg{Code: tc.key})
			got := app.EditorState.Editor.Value()
			if got != tc.want {
				t.Fatalf("%s key did not follow acceptance preference: %q, want %q", tc.mode, got, tc.want)
			}
			if app.completion != nil || app.Input.Value() != "keep completion private draft" {
				t.Fatal("completion key leaked to another input owner")
			}
		})
	}
}
