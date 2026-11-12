package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// An installed provider's occurrences of a parameter paint at its use while
// the caret rests on its declaration.
func TestInstalledProviderPaintsOccurrencesOfTheSymbolAtTheCaret(t *testing.T) {
	f := newInstalledSemanticFixture(t)
	shell, ed, source := f.shell, f.editor, installedSemanticSource
	shell.app.setFocusEditor()
	shell.app.Input.SetInput("private occurrence draft")
	declaration := strings.Index(source, "name string") - strings.Index(source, "func")
	use := strings.LastIndex(source, "name") - strings.Index(source, "func")
	ed.SetCursorPosition(2, declaration+1)
	tokens := ui.NewTokenSet(shell.app.Theme)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		_, cmd := shell.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		runAppCommand(t, shell.app, cmd)
		if !ed.OccurrencesCoverCaret() {
			return false
		}
		shell.app.View()
		r := shell.app.editorRect
		x := r.X + ed.PadL + ed.GutterWidth()
		buf := shell.app.mainSurf.Buffer()
		at, plain := buf.Get(x+use, r.Y+2), buf.Get(x+use-2, r.Y+2)
		filled := at.BG.Equal(tokens.OccurrenceFill(false)) || at.BG.Equal(tokens.OccurrenceFill(true))
		return at.Char == 'n' && filled && !plain.BG.Equal(at.BG)
	})
	if ed.Value() != source || ed.Modified() || ed.HasSelection() || shell.app.Input.Value() != "private occurrence draft" {
		t.Fatal("occurrences altered source, selection or the private draft")
	}
	if data, err := os.ReadFile(f.path); err != nil || string(data) != source {
		t.Fatal("occurrences wrote disk", err)
	}
}
