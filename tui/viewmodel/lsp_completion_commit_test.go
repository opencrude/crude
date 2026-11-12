package viewmodel

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/types"
)

// completionAcknowledge commits pending local edits as the document owner
// would, then replays input that waited behind them.
func completionAcknowledge(t *testing.T, app *App) {
	t.Helper()
	ed := app.EditorState.Editor
	before := ed.DocumentState().Revision
	change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "typing-test", Epoch: 1}, fmt.Sprintf("ack-%d", before), "")
	if err != nil || change.DocumentID == "" {
		t.Fatal("no edit to acknowledge", change, err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID,
		Before: before, Revision: before + 1, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	ed.DrainDocumentUndo()
}

func committing(row types.LSPCompletionEntry, characters ...string) types.LSPCompletionEntry {
	row.CommitCharacters = characters
	return row
}

// renderRow is a sole visible row that is not a literal continuation, so the
// list is shown instead of inline text.
func renderRow(characters ...string) types.LSPCompletionEntry {
	row := committing(lowProfileEntry("render", "printValue", "a"), characters...)
	row.Insertion.TextEdit.NewText = "renderValue()"
	return row
}

func TestCommitCharacterAcceptsChosenRowThenTypesIt(t *testing.T) {
	app, port := automaticCompletionAfterTyping(t,
		committing(lowProfileEntry("print", "print", "a"), "."), committing(lowProfileEntry("println", "println", "b"), "."))
	ed := app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
	completionTestKey(t, app, tea.KeyPressMsg{Code: '.', Text: "."})
	reads := port.reads()
	if len(reads) != 1 || reads[0].item != "print" || reads[0].resolve {
		t.Fatal("commit character did not accept the exact chosen row", reads)
	}
	if ed.Value() != "print" || app.completion != nil || !ed.HoldsDocumentInput() {
		t.Fatal("the character was typed before the insertion committed", ed.Value())
	}
	completionAcknowledge(t, app)
	if ed.Value() != "print." || ed.CursorCol() != len("print.") || ed.HoldsDocumentInput() {
		t.Fatal("commit character did not follow the accepted row", ed.Value())
	}
}

func TestCommitCharacterAcceptsSoleVisibleRow(t *testing.T) {
	app, port := automaticCompletionAfterTyping(t, renderRow(".", ";"))
	ed := app.EditorState.Editor
	if app.completion.screen == nil || app.completion.screen.SelectedID() != "" {
		t.Fatal("sole row must be visible and passive")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: ';', Text: ";"})
	completionAcknowledge(t, app)
	if ed.Value() != "renderValue();" || len(port.reads()) != 1 || port.reads()[0].item != "render" {
		t.Fatal("commit character did not accept the sole visible row", ed.Value(), port.reads())
	}
}

func TestFailedCommitAcceptanceStillTypesTheCharacter(t *testing.T) {
	app, port := automaticCompletionAfterTyping(t, renderRow("."))
	port.inspectErr = errors.New("provider gone")
	ed := app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: '.', Text: "."})
	ed.DrainDocumentUndo()
	if ed.Value() != "pri." || ed.HoldsDocumentInput() {
		t.Fatal("a failed acceptance lost the typed character", ed.Value())
	}
}

func TestCommitCharactersStayOrdinaryTypingWithoutAClearRow(t *testing.T) {
	typedRow := lowProfileEntry("typed", "pri", "z")
	for name, setup := range map[string]struct {
		rows   []types.LSPCompletionEntry
		accept string
		key    string
	}{
		"not a commit character":     {rows: []types.LSPCompletionEntry{renderRow(".")}, key: ","},
		"no commit characters":       {rows: []types.LSPCompletionEntry{renderRow()}, key: "."},
		"enter-only preference":      {rows: []types.LSPCompletionEntry{renderRow(".")}, accept: "enter", key: "."},
		"typed word is a suggestion": {rows: []types.LSPCompletionEntry{renderRow("."), committing(typedRow, ".")}, key: "."},
		"several unchosen rows": {rows: []types.LSPCompletionEntry{renderRow("."),
			committing(lowProfileEntry("private", "privateValue", "b"), ".")}, key: "."},
	} {
		t.Run(name, func(t *testing.T) {
			app, port := automaticCompletionAfterTyping(t, setup.rows...)
			if setup.accept != "" {
				settings := types.DefaultLSPSettings()
				settings.CompletionAccept = setup.accept
				port.service.settings = &settings
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: []rune(setup.key)[0], Text: setup.key})
			if ed := app.EditorState.Editor; ed.Value() != "pri"+setup.key || len(port.reads()) != 0 || ed.HoldsDocumentInput() {
				t.Fatal("ordinary typing accepted a suggestion", ed.Value(), port.reads())
			}
		})
	}
}

// Keys typed between Tab and the row's answer keep their order: the
// suggestion goes in first, as if acceptance were immediate.
func TestTypingAfterTabWaitsForTheAcceptedSuggestion(t *testing.T) {
	app, _ := automaticCompletionAfterTyping(t, renderRow())
	ed := app.EditorState.Editor
	_, read := app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if read == nil || !ed.HoldsDocumentInput() {
		t.Fatal("Tab did not start an ordered acceptance")
	}
	for _, r := range "x;" {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if ed.Value() != "pri" || app.completion == nil {
		t.Fatal("typing overtook or cancelled the acceptance", ed.Value())
	}
	runAppCommand(t, app, read)
	completionAcknowledge(t, app)
	if ed.Value() != "renderValue()x;" || ed.HoldsDocumentInput() {
		t.Fatal("typing after Tab was lost or reordered", ed.Value())
	}
}
