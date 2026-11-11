package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/snippet"
	"crude/tui/cell"
	"crude/tui/ui"
	"crude/types"
)

func lowProfileEntry(id, label, sortText string) types.LSPCompletionEntry {
	return types.LSPCompletionEntry{ID: id, Insertion: types.CompletionItem{Label: label,
		TextEdit: &types.LSPTextEdit{Line: 0, Col: 0, EndLine: 0, EndCol: 3, NewText: label}},
		Prefix: "pri", FilterText: label, SortText: sortText}
}

// automaticCompletionAfterTyping types "i" after "pr", acknowledges the edit
// and lets the debounced automatic request answer with entries.
func automaticCompletionAfterTyping(t *testing.T, entries ...types.LSPCompletionEntry) (*App, *completionTestPort) {
	t.Helper()
	app, port := completionTestApp(t, 100, 32, "pr")
	ed := app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: 'i', Text: "i"})
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
	port.set.Boundary.Source, port.set.Col, port.set.Entries = source, 3, entries
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	app.View()
	if app.completion == nil || !app.completion.shown {
		t.Fatal("automatic suggestions were not presented")
	}
	return app, port
}

// Typing must stay with the editor: the list chooses nothing, starts no
// documentation read, keeps the editor focused (caret visible) and offers the
// obvious top suggestion as faded text that Tab accepts.
func TestAutomaticCompletionIsLowProfileWithInlineSuggestion(t *testing.T) {
	app, port := automaticCompletionAfterTyping(t,
		lowProfileEntry("print", "print", "a"), lowProfileEntry("println", "println", "b"),
		lowProfileEntry("typed", "pri", "c"))
	s, ed := app.completion, app.EditorState.Editor
	if s.screen == nil || !app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatal("two real choices need the list")
	}
	if s.screen.SelectedID() != "" || len(port.reads()) != 0 {
		t.Fatal("automatic suggestions chose the first row or read its documentation", s.screen.SelectedID(), port.reads())
	}
	if app.focusTarget() != FocusEditor || app.focusedPopup() != nil {
		t.Fatal("suggestions took keyboard focus and hid the caret")
	}
	// Frame zero is the visible phase of the shared blink clock. Check the
	// actual composed source cell, not only the logical focus enum.
	app.SpinFrame = 0
	app.paintShellFrame()
	rect, mounted := app.screenRectOf(app.editorScreen.ID())
	x, y := ed.CursorScreenPosition(rect.X, rect.Y)
	if !mounted || !app.mainSurf.Buffer().Get(x, y).Attr.Has(cell.AttrReverse) {
		t.Fatal("the passive suggestion list hid the painted editor caret")
	}
	if _, ok := s.screen.Entry("typed"); ok {
		t.Fatal("suggestion that only repeats the typed text is listed")
	}
	if _, text, ok := ed.GhostText(); !ok || text != "nt" {
		t.Fatalf("obvious suggestion is not shown inline: %q %v", text, ok)
	}
	frame := completionPaintedText(app)
	for _, absent := range []string{"Completions ·", "Esc: close", "No completion selected."} {
		if strings.Contains(frame, absent) {
			t.Fatalf("low-profile list paints %q:\n%s", absent, frame)
		}
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "print" || app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatalf("Tab did not accept the inline suggestion: %q", ed.Value())
	}
}

// Enter is a new line until the user chooses a row; Down chooses the first.
func TestAutomaticCompletionAcceptsOnlyAChosenRowWithEnter(t *testing.T) {
	app, _ := automaticCompletionAfterTyping(t, lowProfileEntry("print", "print", "a"), lowProfileEntry("println", "println", "b"))
	ed := app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
	if app.completion == nil || app.completion.screen.SelectedID() != "print" {
		t.Fatal("Down did not choose the first row")
	}
	if _, _, ok := ed.GhostText(); ok {
		t.Fatal("inline preview of the top row stayed after a choice")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if ed.Value() != "println" || app.completion != nil {
		t.Fatalf("Enter did not accept the chosen row: %q", ed.Value())
	}

	app, _ = automaticCompletionAfterTyping(t, lowProfileEntry("print", "print", "a"), lowProfileEntry("println", "println", "b"))
	ed = app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.completion != nil || ed.Value() == "print" || ed.Value() == "println" || ed.LineCount() != 2 {
		t.Fatalf("Enter without a choice did not start a new line: %q", ed.Value())
	}
}

// One obvious suggestion needs no list at all.
func TestSingleObviousCompletionIsInlineOnly(t *testing.T) {
	app, _ := automaticCompletionAfterTyping(t, lowProfileEntry("print", "print", "a"))
	ed := app.EditorState.Editor
	if app.completion.screen != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatal("a single obvious suggestion opened a list")
	}
	if _, text, ok := ed.GhostText(); !ok || text != "nt" {
		t.Fatalf("single suggestion is not inline: %q", text)
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
	if _, _, ok := ed.GhostText(); ok || ed.Value() != "pri" || app.completion != nil {
		t.Fatal("Esc did not dismiss the inline suggestion")
	}
}

// Continuing the word keeps the list on screen until the next result
// replaces it, instead of closing and reopening it on every key.
func TestTypingKeepsSuggestionsOnScreen(t *testing.T) {
	app, port := automaticCompletionAfterTyping(t, lowProfileEntry("print", "print", "a"), lowProfileEntry("println", "println", "b"))
	completionTestKey(t, app, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if app.completion == nil || !app.completion.stale || !app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatal("typing the same word closed the suggestions")
	}
	if app.EditorState.Editor.Value() != "prin" {
		t.Fatalf("typing was not forwarded exactly once: %q", app.EditorState.Editor.Value())
	}
	// The acknowledged edit advances the context: the old rows stay on screen
	// until the next result replaces them in place.
	ed := app.EditorState.Editor
	change, err := ed.CaptureDocumentEdits(document.Owner{ClientID: "typing-test", Epoch: 1}, "typed-n", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 42, Revision: 43, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	port.service.context = 8
	_, cmd := app.Update(struct{}{})
	if app.completion == nil || !app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatal("the acknowledged edit closed the suggestions before the next result")
	}
	source, _ := ed.CurrentInsightSource()
	narrowed := lowProfileEntry("println", "println", "b")
	narrowed.Prefix, narrowed.Insertion.TextEdit.EndCol = "prin", 4
	port.set.Boundary.Source, port.set.Boundary.Context.Generation, port.set.Col = source, 8, 4
	port.set.Entries = []types.LSPCompletionEntry{narrowed}
	runAppCommand(t, app, cmd)
	app.View()
	if _, ghost, ok := ed.GhostText(); !ok || ghost != "tln" || app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatalf("the next result did not replace the list with the obvious inline suggestion: %q %v", ghost, ok)
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Text: " "})
	if app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) {
		t.Fatal("leaving the word kept old suggestions")
	}
}

func TestRankCompletionsPrefersTypedPrefixAndDropsNoOps(t *testing.T) {
	row := func(id, filter, sortText string) types.LSPCompletionEntry {
		e := lowProfileEntry(id, filter, sortText)
		e.Prefix = "Pri"
		return e
	}
	keyword := lowProfileEntry("keyword", "const", "0")
	keyword.Prefix = "" // the server's edit does not cover the typed word
	got := rankCompletions([]types.LSPCompletionEntry{
		row("fuzzy", "aPxrxi", "0"), row("lower", "print", "1"), row("exact", "Println", "2"),
		row("same", "Pri", "3"), row("dup", "Println", "4"), keyword,
	}, "Pri")
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if len(ids) != 3 || ids[0] != "exact" || ids[1] != "lower" || ids[2] != "fuzzy" {
		t.Fatalf("ranking = %v", ids)
	}
}

func TestRankCompletionsKeepsDistinctImportAndSnippetEdits(t *testing.T) {
	for _, kind := range []string{"imports", "snippet"} {
		t.Run(kind, func(t *testing.T) {
			first := lowProfileEntry("first", "private", "a")
			second := lowProfileEntry("second", "private", "b")
			if kind == "imports" {
				first.Insertion.AdditionalTextEdits = []types.LSPTextEdit{{NewText: "import \"one\"\n"}}
				second.Insertion.AdditionalTextEdits = []types.LSPTextEdit{{NewText: "import \"two\"\n"}}
			} else {
				text, plan, err := snippet.Parse("pri${1:vate}$0")
				if err != nil || text != "private" {
					t.Fatal("invalid snippet fixture:", text, err)
				}
				second.Insertion.Snippet = plan
			}
			duplicate := first
			duplicate.ID = "duplicate"
			got := rankCompletions([]types.LSPCompletionEntry{first, second, duplicate}, "pri")
			if len(got) != 2 || got[0].ID != "first" || got[1].ID != "second" {
				t.Fatalf("display deduplication lost a different source edit: %+v", got)
			}
		})
	}
}

func TestAutomaticCompletionDoesNotGuessBetweenImportSources(t *testing.T) {
	first, second := lowProfileEntry("one", "private", "a"), lowProfileEntry("two", "private", "b")
	first.Documentation.Detail, second.Documentation.Detail = "package one", "package two"
	first.Insertion.AdditionalTextEdits = []types.LSPTextEdit{{NewText: "import \"one\"\n"}}
	second.Insertion.AdditionalTextEdits = []types.LSPTextEdit{{NewText: "import \"two\"\n"}}
	app, _ := automaticCompletionAfterTyping(t, first, second)
	if _, _, ok := app.EditorState.Editor.GhostText(); ok {
		t.Fatal("inline text guessed an import source with the same completion name")
	}
	if app.completion.screen == nil || app.completion.screen.SelectedID() != "" {
		t.Fatal("different import sources are not offered as an explicit choice")
	}
}

// Inline text appears only when it is obvious: never for a real choice.
func TestInlineSuggestionOnlyForObviousCandidate(t *testing.T) {
	app, _ := automaticCompletionAfterTyping(t, lowProfileEntry("upper", "priUpper", "a"), lowProfileEntry("lower", "priLower", "b"))
	if _, _, ok := app.EditorState.Editor.GhostText(); ok {
		t.Fatal("inline text guessed between different suggestions")
	}
	if app.completion.screen == nil || app.completion.screen.SelectedID() != "" {
		t.Fatal("the choice is not offered as an unchosen list")
	}
}

// With a list and no chosen row, Tab does not accept a row the user cannot see
// chosen; it keeps its editing meaning.
func TestTabWithoutChoiceDoesNotAcceptHiddenRow(t *testing.T) {
	app, _ := automaticCompletionAfterTyping(t, lowProfileEntry("upper", "priUpper", "a"), lowProfileEntry("lower", "priLower", "b"))
	ed := app.EditorState.Editor
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if strings.Contains(ed.Value(), "Upper") || strings.Contains(ed.Value(), "Lower") {
		t.Fatalf("Tab accepted a row that was not chosen: %q", ed.Value())
	}
}

// A function suggestion with argument fields is previewed inline; Tab accepts
// the real suggestion (its fields), not the previewed characters.
func TestInlinePreviewOfSnippetAcceptsTheSuggestion(t *testing.T) {
	text, plan, err := snippet.Parse("print(${1:value}, $1)$0")
	if err != nil {
		t.Fatal(err)
	}
	row := lowProfileEntry("print", "print", "a")
	row.Insertion.TextEdit.NewText, row.Insertion.InsertText, row.Insertion.Snippet = text, text, plan
	app, _ := automaticCompletionAfterTyping(t, row)
	ed := app.EditorState.Editor
	if _, ghost, ok := ed.GhostText(); !ok || ghost != "nt(value, value)" {
		t.Fatalf("snippet suggestion is not previewed inline: %q %v", ghost, ok)
	}
	if app.completion.screen == nil {
		t.Fatal("a snippet suggestion lost the list that accepts it")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "print(value, value)" || !ed.SnippetActive() || ed.SelectedText() != "value" {
		t.Fatalf("Tab did not accept the suggestion with its fields: %q selected=%q", ed.Value(), ed.SelectedText())
	}
}

// A server may add call syntax to the inserted text; ToUpper is still the
// obvious row when the only other row is ToUpperSpecial.
func TestInlineSuggestionComparesNamesNotCallSyntax(t *testing.T) {
	upper := lowProfileEntry("upper", "priUpper", "a")
	special := lowProfileEntry("special", "priUpperSpecial", "b")
	upper.Insertion.TextEdit.NewText, special.Insertion.TextEdit.NewText = "priUpper()", "priUpperSpecial()"
	app, _ := automaticCompletionAfterTyping(t, upper, special)
	if _, ghost, ok := app.EditorState.Editor.GhostText(); !ok || ghost != "Upper()" {
		t.Fatalf("obvious row with call syntax is not shown inline: %q %v", ghost, ok)
	}
}
