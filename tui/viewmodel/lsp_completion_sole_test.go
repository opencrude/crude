package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/types"
	"crude/workspace"
	"strings"
	"testing"
	"time"
)

func TestSoleVisibleCompletionAcceptsTabWithoutChoosing(t *testing.T) {
	row := lowProfileEntry("sole", "printValue", "a")
	row.Insertion.TextEdit.NewText = "renderValue()" // not a literal inline continuation
	app, port := automaticCompletionAfterTyping(t, row)
	ed := app.EditorState.Editor
	if app.completion.screen == nil || app.completion.screen.SelectedID() != "" || !strings.Contains(completionPaintedText(app), "printValue") {
		t.Fatal("sole result must be visible and passive")
	}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "renderValue()" || app.completion != nil || len(port.reads()) != 1 || port.reads()[0].resolve || port.reads()[0].item != "sole" {
		t.Fatal("Tab did not accept the exact sole visible row", ed.Value(), port.reads())
	}
}

func TestSoleCompletionRespectsEnterOnlyPreference(t *testing.T) {
	row := lowProfileEntry("sole", "printValue", "a")
	row.Insertion.TextEdit.NewText = "renderValue()"
	app, port := automaticCompletionAfterTyping(t, row)
	settings := types.DefaultLSPSettings()
	settings.CompletionAccept = "enter"
	port.service.settings = &settings
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if app.EditorState.Editor.Value() != "pri " || len(port.reads()) != 0 {
		t.Fatal("Tab ignored acceptance preference", app.EditorState.Editor.Value())
	}
}

func TestSoleCompletionSnippetsImportsAuthorUndoAndSave(t *testing.T) {
	initial := "package p\n\npr"
	shells, eds, _, engine, path := newTypingCompletionFixture(t, initial)
	app := shells[0].app
	eds[0].SetCursorPosition(2, 2)
	engine.base.mu.Lock()
	engine.response = `[{"label":"printValue","filterText":"printValue","kind":3,"insertTextFormat":2,"textEdit":{"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":3}},"newText":"fmt.Println(${1:value})$0"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\n"}]}]`
	engine.base.mu.Unlock()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool {
		runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
		return app.completion != nil && app.completion.screen != nil
	})
	if app.completion.screen.SelectedID() != "" || app.completion.screen.SoleID() == "" {
		t.Fatal("automatic sole row must remain passive")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	want := "package p\nimport \"fmt\"\n\nfmt.Println(value)"
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == want && eds[1].Value() == want })
	if !eds[0].SnippetActive() || eds[0].SelectedText() != "value" || eds[1].SnippetActive() || app.Input.Value() != "private draft" {
		t.Fatal("sole acceptance lost snippet/private ownership")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == initial+"i" && eds[1].Value() == initial+"i" })
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == want && eds[1].Value() == want })
	app.requestEditorSaveWithFormat(eds[0], workspace.DocumentSaveManual, false, false)
	liveTestSync(t, app)
	if eds[0].Modified() {
		t.Fatal("accepted suggestion did not save through the document owner", path)
	}
}

func TestSoleCompletionEnterAndStaleResultsNeverAccept(t *testing.T) {
	for _, mode := range []string{"enter", "stale", "many"} {
		t.Run(mode, func(t *testing.T) {
			row := lowProfileEntry("sole", "printValue", "a")
			row.Insertion.TextEdit.NewText = "render()"
			rows := []types.LSPCompletionEntry{row}
			if mode == "many" {
				other := row
				other.ID = "other"
				other.Insertion.Label = "privateOther"
				edit := *row.Insertion.TextEdit
				edit.NewText = "different()"
				other.Insertion.TextEdit = &edit
				rows = append(rows, other)
			}
			app, port := automaticCompletionAfterTyping(t, rows...)
			if mode == "stale" {
				app.completion.stale = true
			}
			key := tea.KeyTab
			if mode == "enter" {
				key = tea.KeyEnter
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: key})
			if len(port.reads()) != 0 || strings.Contains(app.EditorState.Editor.Value(), "render") {
				t.Fatal("passive or stale row accepted", port.reads(), app.EditorState.Editor.Value())
			}
		})
	}
}
