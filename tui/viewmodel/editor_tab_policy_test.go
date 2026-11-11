package viewmodel

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/types"
	"crude/workspace"
)

func TestEditorTabPolicySharedCompletionUndoAndSave(t *testing.T) {
	initial := "package p\n\n\tpr"
	shells, eds, _, engine, path := newTypingCompletionFixture(t, initial)
	app := shells[0].app
	eds[0].SetCursorPosition(2, 6)
	linked := eds[0].NewLinkedView()
	t.Cleanup(linked.Close)
	engine.base.mu.Lock()
	engine.response = `[{"label":"printValue","filterText":"printValue","kind":3,"insertTextFormat":2,"textEdit":{"range":{"start":{"line":2,"character":1},"end":{"line":2,"character":4}},"newText":"fmt.Println(${1:value})$0"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\n"}]}]`
	engine.base.mu.Unlock()
	width := func(value int) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		runAppCommand(t, app, app.saveSettingsShortcut(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: raw}}, "Tab size saved.", nil, 0))
		remoteJourneyUntil(t, shells, func() bool {
			return eds[0].TabWidth() == value && eds[1].TabWidth() == value && linked.TabWidth() == value
		})
	}
	width(8)
	if eds[0].CursorCol() != 10 {
		t.Fatal("saved width moved the source caret", eds[0].CursorCol())
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool {
		runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
		return app.completion != nil && app.completion.screen != nil
	})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	accepted := "package p\nimport \"fmt\"\n\n\tfmt.Println(value)"
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == accepted && eds[1].Value() == accepted })
	if !eds[0].SnippetActive() || eds[0].SelectedText() != "value" {
		t.Fatal("width lost imported snippet")
	}
	width(2)
	if !eds[0].SnippetActive() || eds[0].SelectedText() != "value" {
		t.Fatal("policy change lost snippet byte ranges")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == initial+"i" && eds[1].Value() == initial+"i" })
	if eds[0].CursorLine() != 2 || eds[0].CursorCol() != 5 {
		t.Fatal("author Undo restored old display columns", eds[0].CursorLine(), eds[0].CursorCol())
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == accepted && eds[1].Value() == accepted })
	app.requestEditorSaveWithFormat(eds[0], workspace.DocumentSaveManual, false, false)
	liveTestSync(t, app)
	if data, err := os.ReadFile(path); err != nil || string(data) != accepted || eds[0].Modified() {
		t.Fatal("geometry entered saved source", err)
	}
	if app.Input.Value() != "private draft" {
		t.Fatal("geometry changed the composer")
	}
}
