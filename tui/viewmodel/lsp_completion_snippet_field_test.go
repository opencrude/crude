package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/snippet"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
)

// acknowledgeCompletionEdit gives the editor the canonical receipt of its
// pending local edit, as the connected document owner does.
func acknowledgeCompletionEdit(t *testing.T, ed *editor.Editor, before uint64) types.InsightSource {
	t.Helper()
	change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "typing-test", Epoch: 1}, "typed", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: before, Revision: before + 1, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	source, current := ed.CurrentInsightSource()
	if !current {
		t.Fatal("fixture did not acknowledge the edit")
	}
	return source
}

// argumentFieldJourney accepts a function suggestion with one argument field,
// types "n" over the field's default and answers the automatic request with
// entries. It returns after the suggestions for the typed argument are shown.
func argumentFieldJourney(t *testing.T, entries ...types.LSPCompletionEntry) (*App, *editor.Editor) {
	t.Helper()
	text, plan, err := snippet.Parse("print(${1:value})$0")
	if err != nil {
		t.Fatal(err)
	}
	row := lowProfileEntry("print", "print", "a")
	row.Insertion.TextEdit.NewText, row.Insertion.InsertText, row.Insertion.Snippet = text, text, plan
	app, port := automaticCompletionAfterTyping(t, row)
	ed := app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "print(value)" || !ed.SnippetActive() || ed.SelectedText() != "value" {
		t.Fatalf("fixture did not accept the function suggestion: %q selected=%q", ed.Value(), ed.SelectedText())
	}
	if app.signatureHelpIntent == nil {
		t.Fatal("accepting a suggestion that opens an argument list did not ask for parameter information")
	}
	acknowledgeCompletionEdit(t, ed, 42)
	completionTestKey(t, app, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if ed.Value() != "print(n)" || !ed.SnippetActive() {
		t.Fatalf("typing did not replace the argument default: %q", ed.Value())
	}
	if app.completionIntent == nil {
		t.Fatal("typing inside the argument list asked for no suggestions")
	}
	source := acknowledgeCompletionEdit(t, ed, 43)
	port.set.Boundary.Source, port.set.Col, port.set.Entries = source, 7, entries
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	app.View()
	if app.completion == nil || !app.completion.shown {
		t.Fatal("no suggestions were shown inside the argument list")
	}
	if !ed.SnippetActive() {
		t.Fatal("showing suggestions ended the snippet")
	}
	return app, ed
}

func argumentEntry(id, label, sortText string) types.LSPCompletionEntry {
	return types.LSPCompletionEntry{ID: id, Insertion: types.CompletionItem{Label: label,
		TextEdit: &types.LSPTextEdit{Line: 0, Col: 6, EndLine: 0, EndCol: 7, NewText: label}},
		Prefix: "n", FilterText: label, SortText: sortText}
}

// The reported case: after a function suggestion is accepted with Tab, typing
// inside its parentheses showed no suggestions until the snippet ended.
func TestSuggestionsWorkInsideAnAcceptedArgumentList(t *testing.T) {
	app, ed := argumentFieldJourney(t, argumentEntry("name", "name", "a"))
	if _, ghost, ok := ed.GhostText(); !ok || ghost != "ame" {
		t.Fatalf("the obvious suggestion is not shown inline in the field: %q %v", ghost, ok)
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "print(name)" || app.completion != nil {
		t.Fatalf("Tab did not accept the inline suggestion: %q", ed.Value())
	}
	if !ed.SnippetActive() {
		t.Fatal("accepting a suggestion inside the field ended the snippet")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.SnippetActive() || ed.HasSelection() || ed.CursorCol() != len("print(name)") || ed.Value() != "print(name)" {
		t.Fatalf("the next Tab did not leave the argument list: active=%t col=%d %q", ed.SnippetActive(), ed.CursorCol(), ed.Value())
	}
}

// With a real choice nothing is chosen for the user. Tab still moves to the
// next field, and a row the user chose is accepted without ending the snippet.
func TestTabPrefersAChosenRowThenTheNextField(t *testing.T) {
	entries := []types.LSPCompletionEntry{argumentEntry("name", "name", "a"), argumentEntry("number", "number", "b")}
	t.Run("no choice", func(t *testing.T) {
		app, ed := argumentFieldJourney(t, entries...)
		if app.completion.screen == nil || !app.overlayHost.IsOpen(ui.OverlayCompletion) {
			t.Fatal("two choices need the list")
		}
		if _, _, inline := ed.GhostText(); inline || app.completion.screen.SelectedID() != "" {
			t.Fatal("a suggestion was chosen for the user")
		}
		completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
		if ed.Value() != "print(n)" || ed.SnippetActive() || ed.CursorCol() != len("print(n)") || app.completion != nil {
			t.Fatalf("Tab without a choice did not move to the next field: %q active=%t col=%d", ed.Value(), ed.SnippetActive(), ed.CursorCol())
		}
	})
	t.Run("chosen row", func(t *testing.T) {
		app, ed := argumentFieldJourney(t, entries...)
		completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
		completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
		completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
		if ed.Value() != "print(number)" || app.completion != nil {
			t.Fatalf("Enter did not accept the chosen row: %q", ed.Value())
		}
		if !ed.SnippetActive() {
			t.Fatal("accepting a chosen row inside the field ended the snippet")
		}
		completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
		if !ed.HoldsDocumentInput() || !ed.SnippetActive() || ed.CursorCol() != len("print(number") {
			t.Fatalf("Tab did not wait with the argument field intact: held=%t active=%t col=%d", ed.HoldsDocumentInput(), ed.SnippetActive(), ed.CursorCol())
		}
		completionAcknowledge(t, app)
		if ed.SnippetActive() || ed.CursorCol() != len("print(number)") || ed.Value() != "print(number)" {
			t.Fatalf("the queued Tab did not leave the argument list: active=%t col=%d value=%q", ed.SnippetActive(), ed.CursorCol(), ed.Value())
		}
	})
}

// Multiple keys queued behind a chosen completion must replay inside the same
// snippet field, in order, before Tab moves to the final stop.
func TestTypingAfterHeldCompletionKeepsTheArgumentField(t *testing.T) {
	entries := []types.LSPCompletionEntry{argumentEntry("name", "name", "a"), argumentEntry("number", "number", "b")}
	app, ed := argumentFieldJourney(t, entries...)
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "xy" {
		completionTestKey(t, app, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if ed.Value() != "print(number)" || !ed.HoldsDocumentInput() || !ed.SnippetActive() {
		t.Fatal("queued typing changed or retired the argument field", ed.Value())
	}
	completionAcknowledge(t, app)
	if ed.Value() != "print(numberxy)" || !ed.SnippetActive() {
		t.Fatal("queued typing did not replay inside the argument field", ed.Value())
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "print(numberxy)" || ed.SnippetActive() || ed.CursorCol() != len("print(numberxy)") {
		t.Fatal("Tab did not finish the argument field after replay", ed.Value(), ed.CursorCol())
	}
}
