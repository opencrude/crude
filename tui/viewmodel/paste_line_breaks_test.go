package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/workspace"
)

// Windows Terminal and xterm send the line breaks of a bracketed paste as CR.
// The composer joined those lines into one, and the editor kept bare CR bytes.
func TestTerminalPasteKeepsItsLinesInComposerAndEditor(t *testing.T) {
	t.Run("composer", func(t *testing.T) {
		app := newFullTestApp(t)
		wsOf(app).Logger = stubLogger{}
		app.AddOrchestrator("paste-owner")
		app.ActiveSession = workspace.NewSession("paste-owner", t.TempDir(), nil, stubLogger{})
		app.maybeHydrateComposerDraft()
		_, cmd := app.Update(tea.PasteMsg{Content: "first line\rsecond line\r\nthird line"})
		runAppCommand(t, app, cmd)
		if got := app.Input.Value(); got != "first line\nsecond line\nthird line" {
			t.Fatalf("pasted lines were joined or changed: %q", got)
		}
	})
	t.Run("editor", func(t *testing.T) {
		app, _ := completionTestApp(t, 100, 32, "head")
		ed := app.EditorState.Editor
		_, cmd := app.Update(tea.PasteMsg{Content: " {\r  \"ios\": true\r}"})
		runAppCommand(t, app, cmd)
		if got := ed.Value(); got != "head {\n  \"ios\": true\n}" || ed.LineCount() != 3 {
			t.Fatalf("pasted lines did not become document lines: %q", got)
		}
	})
}
